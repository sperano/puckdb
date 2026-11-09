package appuser

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A token naming a key the cache lacks refetches the key set: Cloudflare
// rotated its signing key.
func TestKeySet_UnknownKidForcesRefetch(t *testing.T) {
	a, b := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	clk := newClock()
	v := testVerifier(srv, clk)
	ctx := context.Background()
	_, err := v.Verify(ctx, signToken(t, a, kidA, validClaims()))
	require.NoError(t, err)

	srv.set(map[string]*rsa.PublicKey{kidA: &a.PublicKey, kidB: &b.PublicKey}, http.StatusOK)
	clk.Advance(jwksMinRefetchInterval)
	_, err = v.Verify(ctx, signToken(t, b, kidB, validClaims()))
	require.NoError(t, err)
	assert.Equal(t, int32(2), srv.fetches.Load())
}

// Unknown kids cannot make every request fetch.
func TestKeySet_RefetchIsRateLimited(t *testing.T) {
	a, b := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	clk := newClock()
	v := testVerifier(srv, clk)
	ctx := context.Background()
	_, err := v.Verify(ctx, signToken(t, a, kidA, validClaims()))
	require.NoError(t, err)

	clk.Advance(jwksMinRefetchInterval / 2)
	for range 3 {
		_, err = v.Verify(ctx, signToken(t, b, "random-kid", validClaims()))
		require.ErrorIs(t, err, ErrInvalidToken)
		require.ErrorIs(t, err, errUnknownKey)
	}
	assert.Equal(t, int32(1), srv.fetches.Load())
}

// Keys older than jwksMaxAge are refetched, so a retired key stops being
// accepted; a failed refetch keeps the known keys.
func TestKeySet_MaxAge(t *testing.T) {
	a, b := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	clk := newClock()
	v := testVerifier(srv, clk)
	ctx := context.Background()
	tokenA := func() string {
		claims := validClaims()
		claims["exp"] = clk.Now().Add(testTokenLifetime).Unix()
		return signToken(t, a, kidA, claims)
	}
	_, err := v.Verify(ctx, tokenA())
	require.NoError(t, err)

	srv.set(nil, http.StatusInternalServerError)
	clk.Advance(jwksMaxAge)
	_, err = v.Verify(ctx, tokenA())
	require.NoError(t, err, "Cloudflare unreachable: the cached key still verifies")
	assert.Equal(t, int32(2), srv.fetches.Load())

	srv.set(map[string]*rsa.PublicKey{kidB: &b.PublicKey}, http.StatusOK)
	clk.Advance(jwksMinRefetchInterval)
	_, err = v.Verify(ctx, tokenA())
	require.ErrorIs(t, err, ErrInvalidToken, "a key Cloudflare retired is dropped")
	assert.Equal(t, int32(3), srv.fetches.Load())
}

// Failing to fetch the keys is a server error, not an invalid token.
func TestKeySet_FetchFailure(t *testing.T) {
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, nil)
	srv.set(nil, http.StatusBadGateway)
	_, err := testVerifier(srv, newClock()).Verify(context.Background(), signToken(t, a, kidA, validClaims()))
	require.ErrorContains(t, err, "fetch Cloudflare Access signing keys")
	assert.NotErrorIs(t, err, ErrInvalidToken)
}

func TestParseJWKS(t *testing.T) {
	a, _ := signingKeys(t)
	doc := jwksDocument(map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	keyList := doc["keys"].([]map[string]string)
	keyList = append(keyList,
		map[string]string{"kid": "ec", "kty": "EC", "crv": "P-256"},
		map[string]string{"kid": "enc", "kty": "RSA", "use": "enc", "n": keyList[0]["n"], "e": keyList[0]["e"]},
		map[string]string{"kid": "ps256", "kty": "RSA", "alg": "PS256", "n": keyList[0]["n"], "e": keyList[0]["e"]},
	)
	doc["keys"] = keyList
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	keys, err := parseJWKS(body)
	require.NoError(t, err)
	require.Len(t, keys, 1, "only RS256 signing keys are kept")
	assert.True(t, keys[kidA].Equal(&a.PublicKey))

	_, err = parseJWKS([]byte(`{"keys":[{"kid":"ec","kty":"EC"}]}`))
	require.ErrorContains(t, err, "no RS256 signing key")
	_, err = parseJWKS([]byte(`{"keys":[{"kid":"small","kty":"RSA","n":"AQAB","e":"AQAB"}]}`))
	require.ErrorContains(t, err, "modulus")
	_, err = parseJWKS([]byte(`{"keys":[{"kid":"e1","kty":"RSA","n":"` + keyList[0]["n"] + `","e":"AQ"}]}`))
	require.ErrorContains(t, err, "exponent")
}

package appuser

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

// Failing to fetch the keys is a server error, not an invalid token, also
// for the requests the rate limit keeps from fetching again.
func TestKeySet_FetchFailure(t *testing.T) {
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, nil)
	srv.set(nil, http.StatusBadGateway)
	clk := newClock()
	v := testVerifier(srv, clk)
	for range 2 {
		_, err := v.Verify(context.Background(), signToken(t, a, kidA, validClaims()))
		require.ErrorContains(t, err, "fetch Cloudflare Access signing keys")
		assert.NotErrorIs(t, err, ErrInvalidToken)
	}
	assert.Equal(t, int32(1), srv.fetches.Load(), "the second request is rate-limited")

	srv.set(map[string]*rsa.PublicKey{kidA: &a.PublicKey}, http.StatusOK)
	clk.Advance(jwksMinRefetchInterval)
	_, err := v.Verify(context.Background(), signToken(t, a, kidA, validClaims()))
	require.NoError(t, err)
	_, err = v.Verify(context.Background(), signToken(t, a, "random-kid", validClaims()))
	require.ErrorIs(t, err, errUnknownKey, "after a successful fetch, an unknown kid is an invalid token again")
}

const (
	// concurrentVerifiers is how many requests race for one refetch.
	concurrentVerifiers = 16
	// inFlightWaitTimeout is how long a request waits on a stalled fetch.
	inFlightWaitTimeout = 50 * time.Millisecond
)

// Concurrent requests naming a new key share one fetch.
func TestKeySet_ConcurrentRefetchIsShared(t *testing.T) {
	a, b := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	clk := newClock()
	v := testVerifier(srv, clk)
	_, err := v.Verify(context.Background(), signToken(t, a, kidA, validClaims()))
	require.NoError(t, err)

	srv.set(map[string]*rsa.PublicKey{kidA: &a.PublicKey, kidB: &b.PublicKey}, http.StatusOK)
	clk.Advance(jwksMinRefetchInterval)
	token := signToken(t, b, kidB, validClaims())
	errs := make([]error, concurrentVerifiers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = v.Verify(context.Background(), token) })
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), srv.fetches.Load())
}

// A fetch in flight neither delays cached keys, even stale ones, nor ends
// with a waiting request's context: a later request gets its keys.
func TestKeySet_FetchInFlightDoesNotBlock(t *testing.T) {
	a, b := signingKeys(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFetch := func() { releaseOnce.Do(func() { close(release) }) }
	var fetches int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		fetches++
		first := fetches == 1
		mu.Unlock()
		keys := map[string]*rsa.PublicKey{kidA: &a.PublicKey}
		if !first {
			<-release
			keys[kidB] = &b.PublicKey
		}
		_ = json.NewEncoder(w).Encode(jwksDocument(keys))
	}))
	t.Cleanup(srv.Close)
	// Registered after srv.Close so it runs first: Close waits for the
	// stalled handler.
	t.Cleanup(releaseFetch)
	clk := newClock()
	v := newAccessVerifier(testIssuer, testAudience, newKeySet(srv.URL, srv.Client(), clk.Now), clk.Now)
	sign := func(key *rsa.PrivateKey, kid string) string {
		claims := validClaims()
		claims["exp"] = clk.Now().Add(testTokenLifetime).Unix()
		return signToken(t, key, kid, claims)
	}
	_, err := v.Verify(context.Background(), sign(a, kidA))
	require.NoError(t, err)

	clk.Advance(jwksMaxAge)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), inFlightWaitTimeout)
	defer cancelWait()
	_, err = v.Verify(waitCtx, sign(b, kidB))
	require.ErrorIs(t, err, context.DeadlineExceeded, "the waiting request ends with its context")
	staleCtx, cancelStale := context.WithTimeout(context.Background(), inFlightWaitTimeout)
	defer cancelStale()
	_, err = v.Verify(staleCtx, sign(a, kidA))
	require.NoError(t, err, "a cached key past its max age is served while the fetch is in flight")

	releaseFetch()
	_, err = v.Verify(context.Background(), sign(b, kidB))
	require.NoError(t, err, "the fetch outlived the request that started it")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, fetches)
}

// The key set response is bounded.
func TestKeySet_ResponseSizeLimit(t *testing.T) {
	a, _ := signingKeys(t)
	doc, err := json.Marshal(jwksDocument(map[string]*rsa.PublicKey{kidA: &a.PublicKey}))
	require.NoError(t, err)
	serve := func(size int) *keySet {
		body := string(doc) + strings.Repeat(" ", size-len(doc))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return newKeySet(srv.URL, srv.Client(), newClock().Now)
	}

	_, err = serve(maxJWKSBytes).key(context.Background(), kidA)
	require.NoError(t, err, "a key set of exactly the limit is read")
	_, err = serve(maxJWKSBytes+1).key(context.Background(), kidA)
	require.ErrorContains(t, err, "larger than")
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

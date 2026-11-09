package appuser

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTeamDomain = "crapettes.cloudflareaccess.com"
	testIssuer     = "https://" + testTeamDomain
	testAudience   = "aud-tag-of-maurice-crapettes"
	testSubject    = "7335d417-61da-459d-899c-0a01c76a2f94"
	testEmail      = "eric@example.com"
	kidA           = "key-a"
	kidB           = "key-b"
	// testKeyBits is the size of the generated signing keys.
	testKeyBits = 2048
	// testTokenLifetime is how long a test token is valid from testNow.
	testTokenLifetime = time.Hour
)

// testNow is the fixed start of every test clock.
var testNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

var testKeys struct {
	once sync.Once
	a, b *rsa.PrivateKey
	err  error
}

// signingKeys returns two RSA keys shared by the tests (generation is slow).
func signingKeys(t *testing.T) (a, b *rsa.PrivateKey) {
	t.Helper()
	testKeys.once.Do(func() {
		if testKeys.a, testKeys.err = rsa.GenerateKey(rand.Reader, testKeyBits); testKeys.err != nil {
			return
		}
		testKeys.b, testKeys.err = rsa.GenerateKey(rand.Reader, testKeyBits)
	})
	require.NoError(t, testKeys.err)
	return testKeys.a, testKeys.b
}

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: testNow} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// jwksServer serves a settable key set and counts fetches.
type jwksServer struct {
	*httptest.Server
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	status  int
	fetches atomic.Int32
}

func newJWKSServer(t *testing.T, keys map[string]*rsa.PublicKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.fetches.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		_ = json.NewEncoder(w).Encode(jwksDocument(s.keys))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) set(keys map[string]*rsa.PublicKey, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys, s.status = keys, status
}

// jwksDocument is a JWKS shaped like Cloudflare's certs response.
func jwksDocument(keys map[string]*rsa.PublicKey) map[string]any {
	var list []map[string]string
	for kid, k := range keys {
		list = append(list, map[string]string{
			"kid": kid, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes()),
		})
	}
	return map[string]any{"keys": list, "public_cert": map[string]string{"kid": "ignored"}}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// validClaims are the claims of a token Cloudflare issues for the test app.
func validClaims() map[string]any {
	return map[string]any{
		"iss": testIssuer, "aud": []string{testAudience}, "sub": testSubject, "email": testEmail,
		"iat": testNow.Unix(), "nbf": testNow.Unix(), "exp": testNow.Add(testTokenLifetime).Unix(),
		"type": "app", "identity_nonce": "nonce",
	}
}

// signToken returns an RS256 token over claims, signed by key, naming kid.
func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	return signWithHeader(t, key, map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"}, claims)
}

func signWithHeader(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	h, err := json.Marshal(header)
	require.NoError(t, err)
	c, err := json.Marshal(claims)
	require.NoError(t, err)
	input := b64(h) + "." + b64(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return input + "." + b64(sig)
}

// testVerifier verifies against srv's keys with clk.
func testVerifier(srv *jwksServer, clk *clock) *AccessVerifier {
	return newAccessVerifier(testIssuer, testAudience, newKeySet(srv.URL, srv.Client(), clk.Now), clk.Now)
}

func TestAccessVerifier_ValidToken(t *testing.T) {
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	v := testVerifier(srv, newClock())

	claims, err := v.Verify(context.Background(), signToken(t, a, kidA, validClaims()))
	require.NoError(t, err)
	assert.Equal(t, AccessClaims{Subject: testSubject, Email: testEmail}, claims)

	single := validClaims()
	single["aud"] = testAudience
	_, err = v.Verify(context.Background(), signToken(t, a, kidA, single))
	require.NoError(t, err, "aud may be a single string")
	assert.Equal(t, int32(1), srv.fetches.Load(), "known keys are cached")
}

func TestAccessVerifier_RejectsClaims(t *testing.T) {
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	cases := map[string]func(c map[string]any){
		"wrong aud":         func(c map[string]any) { c["aud"] = []string{"another-application"} },
		"no aud":            func(c map[string]any) { delete(c, "aud") },
		"expired":           func(c map[string]any) { c["exp"] = testNow.Add(-testTokenLifetime).Unix() },
		"expired past skew": func(c map[string]any) { c["exp"] = testNow.Add(-tokenClockSkew).Unix() },
		"no exp":            func(c map[string]any) { delete(c, "exp") },
		"not valid yet":     func(c map[string]any) { c["nbf"] = testNow.Add(time.Hour).Unix() },
		"wrong issuer":      func(c map[string]any) { c["iss"] = "https://other.cloudflareaccess.com" },
		"no subject":        func(c map[string]any) { c["sub"] = "" },
		"service token":     func(c map[string]any) { delete(c, "email"); c["common_name"] = "client-id.access" },
		"float exp":         func(c map[string]any) { c["exp"] = 1.5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			mutate(claims)
			_, err := testVerifier(srv, newClock()).Verify(context.Background(), signToken(t, a, kidA, claims))
			require.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

func TestAccessVerifier_ToleratesClockSkew(t *testing.T) {
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	claims := validClaims()
	claims["exp"] = testNow.Add(-tokenClockSkew / 2).Unix()
	claims["nbf"] = testNow.Add(tokenClockSkew / 2).Unix()
	_, err := testVerifier(srv, newClock()).Verify(context.Background(), signToken(t, a, kidA, claims))
	require.NoError(t, err)
}

func TestAccessVerifier_RejectsBadSignatures(t *testing.T) {
	a, b := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	valid := signToken(t, a, kidA, validClaims())
	parts := strings.Split(valid, ".")
	forged := validClaims()
	forged["email"] = "attacker@example.com"
	forgedPayload, err := json.Marshal(forged)
	require.NoError(t, err)

	cases := map[string]string{
		"signed by another key": signToken(t, b, kidA, validClaims()),
		"tampered payload":      parts[0] + "." + b64(forgedPayload) + "." + parts[2],
		"no signature":          parts[0] + "." + parts[1] + ".",
		"alg none":              signWithHeader(t, a, map[string]any{"alg": "none", "kid": kidA}, validClaims()),
		"alg HS256":             signWithHeader(t, a, map[string]any{"alg": "HS256", "kid": kidA}, validClaims()),
		"no kid":                signWithHeader(t, a, map[string]any{"alg": "RS256"}, validClaims()),
		"two parts":             parts[0] + "." + parts[1],
		"padded base64":         parts[0] + "=." + parts[1] + "." + parts[2],
		"not base64":            "%%%." + parts[1] + "." + parts[2],
		"oversized":             strings.Repeat("a", maxTokenBytes+1),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := testVerifier(srv, newClock()).Verify(context.Background(), token)
			require.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

func TestNewAccessVerifier_ValidatesConfig(t *testing.T) {
	_, err := NewAccessVerifier(CloudflareAccessConfig{TeamDomain: testTeamDomain, Audience: testAudience})
	require.NoError(t, err)

	for _, domain := range []string{"", "https://" + testTeamDomain, testTeamDomain + "/", testTeamDomain + "/cdn-cgi", "user@" + testTeamDomain, testTeamDomain + ":8443"} {
		_, err := NewAccessVerifier(CloudflareAccessConfig{TeamDomain: domain, Audience: testAudience})
		require.Error(t, err, domain)
	}
	_, err = NewAccessVerifier(CloudflareAccessConfig{TeamDomain: testTeamDomain, Audience: " "})
	require.ErrorContains(t, err, "audience")
}

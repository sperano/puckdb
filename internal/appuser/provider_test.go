package appuser

import (
	"context"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCloudflareProvider verifies tokens signed by the first test key.
func testCloudflareProvider(t *testing.T) (*CloudflareProvider, *rsa.PrivateKey) {
	t.Helper()
	a, _ := signingKeys(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{kidA: &a.PublicKey})
	return &CloudflareProvider{verifier: testVerifier(srv, newClock())}, a
}

func TestCloudflareProvider_Authenticate(t *testing.T) {
	p, key := testCloudflareProvider(t)
	h := http.Header{}
	h.Set(HeaderCfAccessJWT, signToken(t, key, kidA, validClaims()))
	h.Set("Cf-Access-Authenticated-User-Email", "someone-else@example.com")

	id, err := p.Authenticate(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, Identity{Provider: ProviderCloudflare, Subject: testSubject, Username: testEmail, Email: testEmail}, id,
		"the identity comes from the verified JWT, never from the unsigned email header")
}

func TestCloudflareProvider_RejectsAndIgnores(t *testing.T) {
	p, key := testCloudflareProvider(t)
	expired := validClaims()
	expired["exp"] = testNow.Add(-testTokenLifetime).Unix()
	cases := map[string]struct {
		headers map[string]string
		want    error
	}{
		"no token": {map[string]string{}, ErrUnauthenticated},
		"unsigned email header only": {
			map[string]string{"Cf-Access-Authenticated-User-Email": testEmail}, ErrUnauthenticated},
		"authentik headers are ignored": {
			map[string]string{HeaderUID: "authentik-subject", HeaderUsername: "eric"}, ErrUnauthenticated},
		"expired token": {
			map[string]string{HeaderCfAccessJWT: signToken(t, key, kidA, expired)}, ErrInvalidToken},
		"both identities": {
			map[string]string{HeaderCfAccessJWT: signToken(t, key, kidA, validClaims()), HeaderUID: "authentik-subject"},
			ErrConflictingIdentities},
		"blank authentik header": {
			map[string]string{HeaderCfAccessJWT: signToken(t, key, kidA, validClaims()), HeaderUID: " "}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			id, err := p.Authenticate(context.Background(), h)
			if tc.want == nil {
				require.NoError(t, err, "a blank header is no identity, so nothing conflicts")
				assert.Equal(t, testSubject, id.Subject)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestAuthentikProvider_IgnoresCloudflareHeader(t *testing.T) {
	_, key := testCloudflareProvider(t)
	h := http.Header{}
	h.Set(HeaderCfAccessJWT, signToken(t, key, kidA, validClaims()))
	_, err := AuthentikProvider{}.Authenticate(context.Background(), h)
	require.ErrorIs(t, err, ErrUnauthenticated, "a valid Cloudflare token is no Authentik identity")

	h.Set(HeaderUID, "subject-1")
	_, err = AuthentikProvider{}.Authenticate(context.Background(), h)
	require.ErrorIs(t, err, ErrConflictingIdentities)

	h.Del(HeaderCfAccessJWT)
	id, err := AuthentikProvider{}.Authenticate(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, Identity{Provider: ProviderAuthentik, Subject: "subject-1"}, id)
}

func TestNewProvider(t *testing.T) {
	cf := CloudflareAccessConfig{TeamDomain: testTeamDomain, Audience: testAudience}

	p, err := NewProvider(AuthConfig{Provider: ProviderAuthentik})
	require.NoError(t, err)
	assert.Equal(t, ProviderAuthentik, p.Name())

	p, err = NewProvider(AuthConfig{Provider: ProviderCloudflare, Cloudflare: cf})
	require.NoError(t, err)
	assert.Equal(t, ProviderCloudflare, p.Name())

	_, err = NewProvider(AuthConfig{Provider: ProviderAuthentik, Cloudflare: cf})
	require.ErrorContains(t, err, "cloudflare access settings are set", "a Cloudflare instance left on Authentik would trust its headers")
	_, err = NewProvider(AuthConfig{Provider: ProviderCloudflare})
	require.ErrorContains(t, err, "team domain is empty")
	_, err = NewProvider(AuthConfig{Provider: "oidc"})
	require.ErrorContains(t, err, "unknown login provider")
}

// filtered runs req through IgnoreForeignHeaders(p) and returns the status
// and the headers the next handler saw (nil when it was not called).
func filtered(p Provider, req *http.Request) (int, http.Header) {
	var seen http.Header
	handler := IgnoreForeignHeaders(p)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, seen
}

func TestIgnoreForeignHeaders(t *testing.T) {
	cf, _ := testCloudflareProvider(t)

	req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	req.Header.Set(HeaderUID, "forged")
	req.Header.Set("X-Authentik-Groups", "puckdb-admins")
	req.Header.Set("X-Admin-Token", "kept")
	code, seen := filtered(cf, req)
	assert.Equal(t, http.StatusNoContent, code)
	assert.Empty(t, seen.Get(HeaderUID), "the cloudflare provider drops Authentik headers")
	assert.Empty(t, seen.Get("X-Authentik-Groups"))
	assert.Equal(t, "kept", seen.Get("X-Admin-Token"))
	assert.Equal(t, "forged", req.Header.Get(HeaderUID), "the caller's request is not modified")

	req = httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	req.Header.Set(HeaderCfAccessJWT, "token")
	req.Header.Set(HeaderUID, "subject-1")
	code, seen = filtered(AuthentikProvider{}, req)
	assert.Equal(t, http.StatusUnauthorized, code, "both identities: rejected, not chosen")
	assert.Nil(t, seen)

	req.Header.Set(HeaderUID, " ")
	code, _ = filtered(AuthentikProvider{}, req)
	assert.Equal(t, http.StatusNoContent, code, "a blank header is no identity")

	req.Header.Del(HeaderUID)
	_, seen = filtered(AuthentikProvider{}, req)
	assert.Empty(t, seen.Get(HeaderCfAccessJWT), "the authentik provider drops Cloudflare headers")
}

func TestMiddleware_CloudflareIdentity(t *testing.T) {
	p, key := testCloudflareProvider(t)
	store := &fakeStore{res: Resolution{User: User{ID: "u1", SessionID: "s1"}}}
	req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	req.Header.Set(HeaderCfAccessJWT, signToken(t, key, kidA, validClaims()))
	_, users, errs := serveWith(t, p, store, req, 1)
	require.NoError(t, errs[0])
	assert.Equal(t, User{ID: "u1", SessionID: "s1", Username: testEmail}, users[0], "the user carries the identity's username")
	require.Len(t, store.requests, 1)
	assert.Equal(t, testEmail, store.requests[0].Identity.Email)

	bad := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	bad.Header.Set(HeaderCfAccessJWT, "not-a-token")
	_, _, errs = serveWith(t, p, store, bad, 1)
	require.ErrorIs(t, errs[0], ErrInvalidToken)
	assert.Len(t, store.requests, 1, "a rejected token touches no user")
}

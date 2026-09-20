package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	testUser         = "testuser"
	testUserTokenKey = testUser + "_yahoo_oauth2_token" // config.RedisKeyYahooTokenFmt

	refreshedTokenJSON = `{
		"access_token": "new-access-token",
		"token_type": "Bearer",
		"refresh_token": "new-refresh-token",
		"expires_in": 3600
	}`
	invalidGrantJSON = `{"error": "invalid_grant", "error_description": "refresh token revoked"}`
)

// newRefreshServer serves body to every token request and counts them.
func newRefreshServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv, _ := newCountingRefreshServer(t, http.StatusOK, body)
	return srv
}

func newCountingRefreshServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func storedTokenJSON(t *testing.T, token *oauth2.Token) string {
	t.Helper()
	s, err := cache.TokenAsString(token)
	require.NoError(t, err)
	return s
}

func expiredTokenJSON(t *testing.T) string {
	t.Helper()
	return storedTokenJSON(t, &oauth2.Token{
		AccessToken:  "old-access-token",
		TokenType:    "Bearer",
		RefreshToken: "old-refresh-token",
		Expiry:       time.Now().Add(-time.Hour),
	})
}

func freshTokenJSON(t *testing.T) string {
	t.Helper()
	return storedTokenJSON(t, &oauth2.Token{
		AccessToken:  "fresh-access-token",
		TokenType:    "Bearer",
		RefreshToken: "fresh-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	})
}

// recordArgs keeps the wire arguments of a matched command.
func recordArgs(dst *[]any) func(expected, actual []any) error {
	return func(expected, actual []any) error {
		*dst = append([]any(nil), actual...)
		return nil
	}
}

// expectTokenSaved registers the SET of the refreshed token record (no TTL,
// so the expected shape has none) and returns its recorded arguments.
func expectTokenSaved(mock redismock.ClientMock) *[]any {
	var sent []any
	mock.CustomMatch(recordArgs(&sent)).ExpectSet(testUserTokenKey, "x", 0).SetVal("OK")
	return &sent
}

// expectLockReleased registers redislock's release script (EVALSHA <sha> 1
// <key> <value>); the value is random so only the shape is matched.
func expectLockReleased(mock redismock.ClientMock) {
	mock.CustomMatch(anyArgsMatch).ExpectEvalSha("any", []string{"any"}, "x").SetVal(int64(1))
}

func expectLockObtained(mock redismock.ClientMock) {
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", cache.TokenRefreshLockTTL).SetVal(true)
}

func decodeSavedToken(t *testing.T, sent *[]any) *oauth2.Token {
	t.Helper()
	require.GreaterOrEqual(t, len(*sent), 3, "SET key value")
	raw, ok := (*sent)[2].(string)
	require.True(t, ok)
	var token oauth2.Token
	require.NoError(t, json.Unmarshal([]byte(raw), &token))
	return &token
}

func newTestTokenSource(t *testing.T, stored string, tokenServerURL string) (*refreshingTokenSource, redismock.ClientMock) {
	t.Helper()
	redisClient, mock := redismock.NewClientMock()
	mock.ExpectGet(testUserTokenKey).SetVal(stored)
	ctx := context.WithValue(context.Background(), config.CtxUser, testUser)
	src, err := newRefreshingTokenSource(ctx, redisClient, createTestOAuthConfig(tokenServerURL), testUser)
	require.NoError(t, err)
	return src, mock
}

func TestRefreshingTokenSource_ValidTokenNeedsNoRedis(t *testing.T) {
	t.Parallel()
	srv, calls := newCountingRefreshServer(t, http.StatusOK, refreshedTokenJSON)
	src, mock := newTestTokenSource(t, freshTokenJSON(t), srv.URL)

	token, err := src.Token()

	require.NoError(t, err)
	assert.Equal(t, "fresh-access-token", token.AccessToken)
	assert.Zero(t, calls.Load(), "a valid token is never refreshed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshingTokenSource_RefreshesOnceAndPersists(t *testing.T) {
	t.Parallel()
	srv, calls := newCountingRefreshServer(t, http.StatusOK, refreshedTokenJSON)
	src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
	expectLockObtained(mock)
	mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
	saved := expectTokenSaved(mock)
	expectLockReleased(mock)

	first, err := src.Token()
	require.NoError(t, err)
	// The refreshed token is cached: the next call touches neither Redis
	// nor Yahoo.
	second, err := src.Token()
	require.NoError(t, err)

	assert.Equal(t, "new-access-token", first.AccessToken)
	assert.Same(t, first, second)
	assert.Equal(t, int32(1), calls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "new-refresh-token", decodeSavedToken(t, saved).RefreshToken)
}

func TestRefreshingTokenSource_UsesTokenRefreshedByAnotherProcess(t *testing.T) {
	t.Parallel()
	srv, calls := newCountingRefreshServer(t, http.StatusOK, refreshedTokenJSON)
	src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
	expectLockObtained(mock)
	// While we waited for the lock, another client refreshed and stored a
	// fresh token: adopt it instead of spending our (possibly now rotated
	// away) refresh token on a second refresh. Nothing is written back.
	mock.ExpectGet(testUserTokenKey).SetVal(freshTokenJSON(t))
	expectLockReleased(mock)

	token, err := src.Token()

	require.NoError(t, err)
	assert.Equal(t, "fresh-access-token", token.AccessToken)
	assert.Zero(t, calls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshingTokenSource_RevokedRefreshTokenRequiresLogin(t *testing.T) {
	t.Parallel()
	srv, _ := newCountingRefreshServer(t, http.StatusBadRequest, invalidGrantJSON)
	src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
	expectLockObtained(mock)
	mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
	// The dead record is removed so status reporting no longer claims the
	// user is logged in.
	mock.ExpectDel(testUserTokenKey).SetVal(1)
	expectLockReleased(mock)

	token, err := src.Token()

	require.Error(t, err)
	assert.Nil(t, token)
	var missing *cache.OAuth2TokenMissingError
	require.True(t, errors.As(err, &missing), "a revoked refresh token is a login prompt: %v", err)
	assert.Contains(t, err.Error(), "rejected")
	assert.Contains(t, err.Error(), "/yahoo/login")
	assert.NotContains(t, err.Error(), "old-refresh-token")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshingTokenSource_NonRFCRejectionRequiresLogin(t *testing.T) {
	t.Parallel()
	// Yahoo's error bodies do not always carry an RFC 6749 "error" code; a
	// definitive 4xx is still a rejection, and with records carrying no TTL
	// nothing else would ever clear the dead credential.
	srv, _ := newCountingRefreshServer(t, http.StatusUnauthorized, `{"error":{"description":"refresh token expired"}}`)
	src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
	expectLockObtained(mock)
	mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
	mock.ExpectDel(testUserTokenKey).SetVal(1)
	expectLockReleased(mock)

	_, err := src.Token()

	var missing *cache.OAuth2TokenMissingError
	require.True(t, errors.As(err, &missing), "%v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshingTokenSource_TransientRefreshErrorKeepsRecord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"error":"server_error"}`},
		{"rate limited", http.StatusTooManyRequests, `{"error":"temporarily_unavailable"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := newCountingRefreshServer(t, tc.status, tc.body)
			src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
			expectLockObtained(mock)
			mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
			// No DEL: a Yahoo outage is not a reason to throw the credential away.
			expectLockReleased(mock)

			_, err := src.Token()

			require.Error(t, err)
			var missing *cache.OAuth2TokenMissingError
			assert.False(t, errors.As(err, &missing))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshingTokenSource_LockUnavailable(t *testing.T) {
	t.Parallel()
	srv, calls := newCountingRefreshServer(t, http.StatusOK, refreshedTokenJSON)
	src, mock := newTestTokenSource(t, expiredTokenJSON(t), srv.URL)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", cache.TokenRefreshLockTTL).SetErr(errors.New("network down"))

	_, err := src.Token()

	require.Error(t, err)
	assert.Zero(t, calls.Load(), "never refresh outside the lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The acceptance path: a client built after the access token expired makes
// an authenticated request without any interactive login.
func TestYahooClient_RequestAfterExpiryUsesRefreshedToken(t *testing.T) {
	t.Parallel()
	var authorization string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(api.Close)

	redisClient, mock := redismock.NewClientMock()
	ctx := context.WithValue(context.Background(), config.CtxUser, testUser)
	conf := createTestOAuthConfig(newRefreshServer(t, refreshedTokenJSON).URL)
	mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
	expectLockObtained(mock)
	mock.ExpectGet(testUserTokenKey).SetVal(expiredTokenJSON(t))
	expectTokenSaved(mock)
	expectLockReleased(mock)

	client, err := NewYahooClientWithConfig(ctx, redisClient, conf)
	require.NoError(t, err)
	body, err := client.Download(ctx, api.URL)

	require.NoError(t, err)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, "Bearer new-access-token", authorization)
	require.NoError(t, mock.ExpectationsWereMet())
}

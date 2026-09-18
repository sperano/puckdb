package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestSetNoCacheHeaders(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	setNoCacheHeaders(w)

	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Equal(t, "0", w.Header().Get("Expires"))
}

func TestHandleError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		statusCode     int
		err            error
		expectedBody   string
		expectedStatus int
	}{
		{
			name:           "bad request",
			statusCode:     http.StatusBadRequest,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "internal server error",
			statusCode:     http.StatusInternalServerError,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "forbidden",
			statusCode:     http.StatusForbidden,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleError(w, tt.statusCode, tt.err)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), "error")
		})
	}
}

func TestYahooLandedHandler(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/landed", nil)
	w := httptest.NewRecorder()

	YahooLandedHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "Login Successful")
	assert.Contains(t, w.Body.String(), "/yahoo/login")
}

func TestYahooAuthenticatedHandler_MissingState(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback?code=some-code", nil)
	w := httptest.NewRecorder()

	// nil Redis: the request must be rejected before any storage access.
	handler := YahooAuthenticatedHandler(nil)
	handler(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "missing state")
}

func TestYahooAuthenticatedHandler_MissingCode(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	mock.ExpectDel(loginStateKey(testLoginState)).SetVal(1)

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback?state="+testLoginState, nil)
	req.AddCookie(&http.Cookie{Name: config.YahooLoginStateCookie, Value: testLoginState})
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandler(client)
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing authorization code")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestYahooAuthenticatedHandler_OAuthError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		errorParam  string
		errorDesc   string
		wantContain string
	}{
		{
			name:        "access_denied",
			errorParam:  "access_denied",
			errorDesc:   "User denied access",
			wantContain: "access_denied",
		},
		{
			name:        "invalid_request",
			errorParam:  "invalid_request",
			errorDesc:   "Missing required parameter",
			wantContain: "invalid_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/yahoo/callback?error=" + url.QueryEscape(tt.errorParam) +
				"&error_description=" + url.QueryEscape(tt.errorDesc)
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			w := httptest.NewRecorder()

			handler := YahooAuthenticatedHandler(nil)
			handler(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantContain)
		})
	}
}

func TestYahooAuthenticatedHandler_SetsNoCacheHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback", nil)
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandler(nil)
	handler(w, req)

	// Should set no-cache headers even when returning an error
	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Equal(t, "0", w.Header().Get("Expires"))
}

// Helper functions for yahoo tests

func createYahooTestOAuthConfig(tokenServerURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  tokenServerURL + "/auth",
			TokenURL: tokenServerURL + "/token",
		},
		RedirectURL: "http://localhost:8080/yahoo/callback",
		Scopes:      []string{"openid", "fspt-r"},
	}
}

func TestYahooLoginHandlerWithConfig(t *testing.T) {
	t.Parallel()

	conf := createYahooTestOAuthConfig("https://api.login.yahoo.com")
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", cache.LoginStateTTL).SetVal(true)

	req := httptest.NewRequest(http.MethodGet, "/yahoo/login", nil)
	w := httptest.NewRecorder()

	handler := YahooLoginHandlerWithConfig(client, conf)
	handler(w, req)

	assert.Equal(t, http.StatusFound, w.Code)

	location, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://api.login.yahoo.com/auth", location.Scheme+"://"+location.Host+location.Path)
	assert.Equal(t, "test-client-id", location.Query().Get("client_id"))
	state := location.Query().Get("state")
	assert.NotEmpty(t, state)
	assert.NotEqual(t, "state", state, "state must be random, not a constant")

	// The same state must be bound to the browser via the cookie.
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, config.YahooLoginStateCookie, cookies[0].Name)
	assert.Equal(t, state, cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)

	// Verify no-cache headers are set
	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestYahooLoginHandlerWithConfig_StateStoreFails(t *testing.T) {
	t.Parallel()

	conf := createYahooTestOAuthConfig("https://api.login.yahoo.com")
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", cache.LoginStateTTL).SetErr(redis.ErrClosed)

	w := httptest.NewRecorder()
	YahooLoginHandlerWithConfig(client, conf)(w, httptest.NewRequest(http.MethodGet, "/yahoo/login", nil))

	// Without a registered state the login cannot be verified later, so no
	// redirect and no cookie may be issued.
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, w.Header().Get("Location"))
	assert.Empty(t, w.Result().Cookies())
}

func TestExchangeCodeWithConfig_AlreadyHasValidToken(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig("http://example.com")

	// Create a valid non-expired token
	token := &oauth2.Token{
		AccessToken:  "valid-access-token",
		TokenType:    "Bearer",
		RefreshToken: "valid-refresh-token",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	tokenJSON, _ := cache.TokenAsString(token)

	// Key format: %s_yahoo_oauth2_token
	mock.ExpectGet("testuser_yahoo_oauth2_token").SetVal(tokenJSON)

	err := exchangeCodeWithConfig(ctx, client, conf, "testuser", "some-code")
	assert.NoError(t, err) // Should succeed without exchanging
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExchangeCodeWithConfig_AuthCodeAlreadyUsed(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig("http://example.com")

	// Key format: %s_yahoo_oauth2_token
	mock.ExpectGet("testuser_yahoo_oauth2_token").RedisNil()

	// Key is digest-derived (wildcard-matched here) - SetNX returns false (already exists)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(false)

	err := exchangeCodeWithConfig(ctx, client, conf, "testuser", "already-used-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exchange authorization code")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExchangeCodeWithConfig_TokenExchangeFails(t *testing.T) {
	t.Parallel()

	// Token server that returns an error
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid_grant", "error_description": "Code expired"}`))
	}))
	defer tokenServer.Close()

	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mock.ExpectGet("testuser_yahoo_oauth2_token").RedisNil()

	// Key is digest-derived (wildcard-matched here)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(true)

	err := exchangeCodeWithConfig(ctx, client, conf, "testuser", "expired-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token exchange failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExchangeCodeWithConfig_TokenSaveFails(t *testing.T) {
	t.Parallel()

	// Token server that returns a valid token
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "new-access-token",
			"token_type": "Bearer",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600
		}`))
	}))
	defer tokenServer.Close()

	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mock.ExpectGet("testuser_yahoo_oauth2_token").RedisNil()

	// Key is digest-derived (wildcard-matched here)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(true)

	// Mock: save token fails
	mock.CustomMatch(anyArgsMatch).ExpectSet("testuser_yahoo_oauth2_token", "x", 0).SetErr(redis.ErrClosed)

	err := exchangeCodeWithConfig(ctx, client, conf, "testuser", "valid-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token save failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExchangeCodeWithConfig_Success(t *testing.T) {
	t.Parallel()

	// Token server that returns a valid token
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "new-access-token",
			"token_type": "Bearer",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600
		}`))
	}))
	defer tokenServer.Close()

	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mock.ExpectGet("testuser_yahoo_oauth2_token").RedisNil()

	// Key is digest-derived (wildcard-matched here)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(true)

	// Mock: save token succeeds
	mock.CustomMatch(anyArgsMatch).ExpectSet("testuser_yahoo_oauth2_token", "x", 0).SetVal("OK")

	err := exchangeCodeWithConfig(ctx, client, conf, "testuser", "valid-code")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- YahooLoginHandler (the convenience wrapper around
//     YahooLoginHandlerWithConfig that pulls config from viper) ---

// withEmptyOAuthViper temporarily clears the Yahoo OAuth2 viper keys so
// config.OauthConfig() returns an "is empty" error. Restores originals
// on cleanup. Cannot be t.Parallel() since viper is global state.
func withEmptyOAuthViper(t *testing.T) {
	t.Helper()
	origID := viper.GetString(config.FlagYahooOAuth2ClientID)
	origSecret := viper.GetString(config.FlagYahooOAuth2ClientSecret)
	viper.Set(config.FlagYahooOAuth2ClientID, "")
	viper.Set(config.FlagYahooOAuth2ClientSecret, "")
	t.Cleanup(func() {
		viper.Set(config.FlagYahooOAuth2ClientID, origID)
		viper.Set(config.FlagYahooOAuth2ClientSecret, origSecret)
	})
}

func TestYahooLoginHandler_MissingConfig(t *testing.T) {
	withEmptyOAuthViper(t)

	req := httptest.NewRequest(http.MethodGet, "/yahoo/login", nil)
	w := httptest.NewRecorder()

	YahooLoginHandler(nil)(w, req)

	// OauthConfig returns an error -> handleError writes 500.
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- NewYahooClient (the convenience wrapper around
//     NewYahooClientWithConfig that pulls config from viper) ---

func TestNewYahooClient_MissingConfig(t *testing.T) {
	withEmptyOAuthViper(t)

	client, _ := redismock.NewClientMock()
	got, err := NewYahooClient(context.Background(), client)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "is empty")
}

// --- DownloadYahoo (the wrapper that builds a Yahoo client per call
//     and propagates token-missing errors with a public-URL hint) ---

func TestDownloadYahoo_TokenMissingWraps(t *testing.T) {
	// Ensure viper has valid OAuth2 client config so OauthConfig() succeeds;
	// the test failure must come from the missing token in Redis, not from
	// missing viper config. Save and restore in case of preexisting values.
	origID := viper.GetString(config.FlagYahooOAuth2ClientID)
	origSecret := viper.GetString(config.FlagYahooOAuth2ClientSecret)
	origPublic := viper.GetString(config.FlagPublicURL)
	viper.Set(config.FlagYahooOAuth2ClientID, "id")
	viper.Set(config.FlagYahooOAuth2ClientSecret, "secret")
	viper.Set(config.FlagPublicURL, "http://test.example")
	t.Cleanup(func() {
		viper.Set(config.FlagYahooOAuth2ClientID, origID)
		viper.Set(config.FlagYahooOAuth2ClientSecret, origSecret)
		viper.Set(config.FlagPublicURL, origPublic)
	})

	client, mock := redismock.NewClientMock()
	// SaveToken / LoadToken use config.UserFromContext — provide one.
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	mock.ExpectGet("testuser_yahoo_oauth2_token").RedisNil()

	body, err := DownloadYahoo(ctx, client, "https://api.example/path")

	require.Error(t, err)
	assert.Nil(t, body)
	// Must be the token-missing variant, not the generic-error wrap.
	var missing *cache.OAuth2TokenMissingError
	assert.True(t, errors.As(err, &missing))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDownloadYahoo_GenericErrorWraps(t *testing.T) {
	// Redis returns an unexpected error (not RedisNil) — DownloadYahoo
	// must propagate via the second wrap branch (no PublicURL hint).
	origID := viper.GetString(config.FlagYahooOAuth2ClientID)
	origSecret := viper.GetString(config.FlagYahooOAuth2ClientSecret)
	viper.Set(config.FlagYahooOAuth2ClientID, "id")
	viper.Set(config.FlagYahooOAuth2ClientSecret, "secret")
	t.Cleanup(func() {
		viper.Set(config.FlagYahooOAuth2ClientID, origID)
		viper.Set(config.FlagYahooOAuth2ClientSecret, origSecret)
	})

	client, mock := redismock.NewClientMock()
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	mock.ExpectGet("testuser_yahoo_oauth2_token").SetErr(errors.New("redis down"))

	body, err := DownloadYahoo(ctx, client, "https://api.example/path")

	require.Error(t, err)
	assert.Nil(t, body)
	// Should NOT be wrapped as token-missing — that branch only triggers
	// for RedisNil/OAuth2TokenMissingError specifically.
	var missing *cache.OAuth2TokenMissingError
	assert.False(t, errors.As(err, &missing))
	require.NoError(t, mock.ExpectationsWereMet())
}

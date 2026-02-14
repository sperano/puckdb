package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func TestYahooAuthenticatedHandler_MissingCode(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback", nil)
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandler(nil)
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing authorization code")
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

func createYahooMockStringCmd(val string, err error) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

func createYahooMockBoolCmd(val bool, err error) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

func createYahooMockStatusCmd(val string, err error) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

func TestYahooLoginHandlerWithConfig(t *testing.T) {
	t.Parallel()

	conf := createYahooTestOAuthConfig("https://api.login.yahoo.com")

	req := httptest.NewRequest(http.MethodGet, "/yahoo/login", nil)
	w := httptest.NewRecorder()

	handler := YahooLoginHandlerWithConfig(conf)
	handler(w, req)

	assert.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	assert.Contains(t, location, "https://api.login.yahoo.com/auth")
	assert.Contains(t, location, "client_id=test-client-id")
	assert.Contains(t, location, "state=state")

	// Verify no-cache headers are set
	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
}

func TestYahooAuthenticatedHandlerWithConfig_Success(t *testing.T) {
	t.Parallel()

	// Create a mock OAuth2 token server
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

	conf := createYahooTestOAuthConfig(tokenServer.URL)
	mockRedis := &cache.MockClient{}

	// Key format: %s_yahoo_oauth2_token (config.DefaultUser = "eric")
	mockRedis.On("Get", mock.Anything, "eric_yahoo_oauth2_token").
		Return(createYahooMockStringCmd("", redis.Nil))

	// Key format: yahoo_oauth2_code_%s
	mockRedis.On("SetNX", mock.Anything, "yahoo_oauth2_code_test-auth-code", mock.Anything, mock.Anything).
		Return(createYahooMockBoolCmd(true, nil))

	// Mock: save the new token
	mockRedis.On("Set", mock.Anything, "eric_yahoo_oauth2_token", mock.AnythingOfType("string"), mock.Anything).
		Return(createYahooMockStatusCmd("OK", nil))

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback", nil)
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandlerWithConfig(mockRedis, conf, "http://localhost/success", "test-auth-code")
	handler(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "http://localhost/success", w.Header().Get("Location"))
	mockRedis.AssertExpectations(t)
}

func TestExchangeCodeWithConfig_AlreadyHasValidToken(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
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
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createYahooMockStringCmd(tokenJSON, nil))

	err := exchangeCodeWithConfig(ctx, mockRedis, conf, "testuser", "some-code")
	assert.NoError(t, err) // Should succeed without exchanging
	mockRedis.AssertExpectations(t)
}

func TestExchangeCodeWithConfig_AuthCodeAlreadyUsed(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig("http://example.com")

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createYahooMockStringCmd("", redis.Nil))

	// Key format: yahoo_oauth2_code_%s - SetNX returns false (already exists)
	mockRedis.On("SetNX", mock.Anything, "yahoo_oauth2_code_already-used-code", mock.Anything, mock.Anything).
		Return(createYahooMockBoolCmd(false, nil))

	err := exchangeCodeWithConfig(ctx, mockRedis, conf, "testuser", "already-used-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exchange authorization code")
	mockRedis.AssertExpectations(t)
}

func TestExchangeCodeWithConfig_TokenExchangeFails(t *testing.T) {
	t.Parallel()

	// Token server that returns an error
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid_grant", "error_description": "Code expired"}`))
	}))
	defer tokenServer.Close()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createYahooMockStringCmd("", redis.Nil))

	// Key format: yahoo_oauth2_code_%s
	mockRedis.On("SetNX", mock.Anything, "yahoo_oauth2_code_expired-code", mock.Anything, mock.Anything).
		Return(createYahooMockBoolCmd(true, nil))

	err := exchangeCodeWithConfig(ctx, mockRedis, conf, "testuser", "expired-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token exchange failed")
	mockRedis.AssertExpectations(t)
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

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createYahooMockStringCmd("", redis.Nil))

	// Key format: yahoo_oauth2_code_%s
	mockRedis.On("SetNX", mock.Anything, "yahoo_oauth2_code_valid-code", mock.Anything, mock.Anything).
		Return(createYahooMockBoolCmd(true, nil))

	// Mock: save token fails
	mockRedis.On("Set", mock.Anything, "testuser_yahoo_oauth2_token", mock.AnythingOfType("string"), mock.Anything).
		Return(createYahooMockStatusCmd("", redis.ErrClosed))

	err := exchangeCodeWithConfig(ctx, mockRedis, conf, "testuser", "valid-code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token save failed")
	mockRedis.AssertExpectations(t)
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

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), config.CtxUser, "testuser")
	conf := createYahooTestOAuthConfig(tokenServer.URL)

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createYahooMockStringCmd("", redis.Nil))

	// Key format: yahoo_oauth2_code_%s
	mockRedis.On("SetNX", mock.Anything, "yahoo_oauth2_code_valid-code", mock.Anything, mock.Anything).
		Return(createYahooMockBoolCmd(true, nil))

	// Mock: save token succeeds
	mockRedis.On("Set", mock.Anything, "testuser_yahoo_oauth2_token", mock.AnythingOfType("string"), mock.Anything).
		Return(createYahooMockStatusCmd("OK", nil))

	err := exchangeCodeWithConfig(ctx, mockRedis, conf, "testuser", "valid-code")
	assert.NoError(t, err)
	mockRedis.AssertExpectations(t)
}

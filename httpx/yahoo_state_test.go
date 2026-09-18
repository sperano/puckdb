package httpx

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testLoginState = "test-state-value"

// anyArgsMatch is the wildcard redismock matcher shared by the httpx tests:
// SetNX/Set carry TTLs computed at call time and login states are random, so
// exact argument matching is not practical for those commands.
func anyArgsMatch(expected, actual []any) error { return nil }

func loginStateKey(state string) string {
	return fmt.Sprintf(config.RedisKeyYahooLoginStateFmt, state)
}

// callbackRequest builds a Yahoo callback carrying the given state parameter
// and, when cookieState is non-empty, the login-state cookie.
func callbackRequest(state, cookieState string) *http.Request {
	target := config.YahooAuthCallbackPath + "?code=any-code"
	if state != "" {
		target += "&state=" + state
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookieState != "" {
		req.AddCookie(&http.Cookie{Name: config.YahooLoginStateCookie, Value: cookieState})
	}
	return req
}

func TestYahooCookiePath_CoversCallback(t *testing.T) {
	t.Parallel()

	// RFC 6265 path-match: the browser only sends the cookie to the callback
	// if the callback path is the cookie path or a "/"-separated child of it.
	assert.True(t, strings.HasPrefix(config.YahooAuthCallbackPath, yahooCookiePath+"/"))
	assert.NotEqual(t, "/", yahooCookiePath, "cookie must stay scoped to the OAuth routes")
}

func TestNewLoginStateCookie_Attributes(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, config.YahooLoginPath, nil)
	c := newLoginStateCookie(req, testLoginState)

	assert.Equal(t, config.YahooLoginStateCookie, c.Name)
	assert.Equal(t, testLoginState, c.Value)
	assert.Equal(t, yahooCookiePath, c.Path)
	assert.Equal(t, int(cache.LoginStateTTL.Seconds()), c.MaxAge)
	assert.True(t, c.HttpOnly)
	// Lax, not Strict: the callback is a cross-site top-level navigation.
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.False(t, c.Secure, "plain HTTP request must not produce a Secure cookie")
}

func TestNewLoginStateCookie_SecureOverTLS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{name: "direct TLS", setup: func(r *http.Request) { r.TLS = &tls.ConnectionState{} }},
		{name: "proxy terminated TLS", setup: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }},
		{name: "proxy header case-insensitive", setup: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "HTTPS") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, config.YahooLoginPath, nil)
			tt.setup(req)
			assert.True(t, newLoginStateCookie(req, testLoginState).Secure)
		})
	}
}

func TestClearLoginStateCookie(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, config.YahooAuthCallbackPath, nil)
	c := clearLoginStateCookie(req)

	assert.Equal(t, config.YahooLoginStateCookie, c.Name)
	assert.Empty(t, c.Value)
	assert.Equal(t, -1, c.MaxAge)
	assert.Equal(t, yahooCookiePath, c.Path, "must match the issuing path or the browser keeps the old cookie")
}

func TestValidateLoginState_Valid(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	mock.ExpectDel(loginStateKey(testLoginState)).SetVal(1)

	err := validateLoginState(context.Background(), client, callbackRequest(testLoginState, testLoginState))

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateLoginState_RejectedBeforeRedis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       string
		cookieState string
		wantContain string
	}{
		{name: "missing state parameter", state: "", cookieState: testLoginState, wantContain: "missing state"},
		{name: "missing cookie", state: testLoginState, cookieState: "", wantContain: "missing login state cookie"},
		{name: "mismatch", state: testLoginState, cookieState: "another-browser", wantContain: "does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No expectations: any Redis call is a failure, since a callback
			// that isn't bound to this browser must not consume anything.
			client, mock := redismock.NewClientMock()

			err := validateLoginState(context.Background(), client, callbackRequest(tt.state, tt.cookieState))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantContain)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestValidateLoginState_ExpiredOrReplayed(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	mock.ExpectDel(loginStateKey(testLoginState)).SetVal(0)

	err := validateLoginState(context.Background(), client, callbackRequest(testLoginState, testLoginState))

	require.ErrorIs(t, err, cache.ErrLoginStateInvalid)
}

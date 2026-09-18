package httpx

// End-to-end tests of the Yahoo login flow: /yahoo/login issues a state and
// binds it to the browser, /yahoo/authenticated must be presented with that
// same binding before any authorization code is exchanged.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// startLogin runs the login handler for one "browser" and returns the state
// Yahoo will echo back plus the cookie that binds it to that browser.
func startLogin(t *testing.T, client *redis.Client, mock redismock.ClientMock, conf *oauth2.Config) (string, *http.Cookie) {
	t.Helper()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", cache.LoginStateTTL).SetVal(true)

	req := httptest.NewRequest(http.MethodGet, config.YahooLoginPath, nil)
	w := httptest.NewRecorder()
	YahooLoginHandlerWithConfig(client, conf)(w, req)
	require.Equal(t, http.StatusFound, w.Code)

	location, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	state := location.Query().Get("state")
	require.NotEmpty(t, state)

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == config.YahooLoginStateCookie {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "login must set the state cookie")
	return state, cookie
}

// completeLogin runs the callback handler as a browser presenting cookie,
// with Yahoo having redirected it back with state and code.
func completeLogin(client *redis.Client, conf *oauth2.Config, state string, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
	target := config.YahooAuthCallbackPath + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	w := httptest.NewRecorder()
	resolve := func() (*oauth2.Config, string, error) { return conf, testSuccessURL, nil }
	yahooAuthenticatedHandler(client, resolve)(w, req)
	return w
}

const testSuccessURL = "http://localhost/success"

// newTokenServer is a fake Yahoo token endpoint that counts exchanges, so a
// test can prove a rejected callback never reached Yahoo.
func newTokenServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var exchanges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "new-access-token",
			"token_type": "Bearer",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &exchanges
}

// expectSuccessfulExchange registers the Redis traffic of one complete
// callback: consume the state, find no token, mark the code, save the token.
func expectSuccessfulExchange(mock redismock.ClientMock, state, code string) {
	mock.ExpectDel(loginStateKey(state)).SetVal(1)
	// Key format: %s_yahoo_oauth2_token (config.DefaultUser = "eric")
	mock.ExpectGet("eric_yahoo_oauth2_token").RedisNil()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("yahoo_oauth2_code_"+code, "x", time.Hour).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectSet("eric_yahoo_oauth2_token", "x", time.Hour).SetVal("OK")
}

func TestYahooLoginFlow_ValidStateCompletesOnce(t *testing.T) {
	t.Parallel()

	tokenServer, exchanges := newTokenServer(t)
	conf := createYahooTestOAuthConfig(tokenServer.URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	expectSuccessfulExchange(mock, state, "test-auth-code")

	w := completeLogin(client, conf, state, cookie, "test-auth-code")

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, testSuccessURL, w.Header().Get("Location"))
	assert.Equal(t, int32(1), exchanges.Load())
	require.NoError(t, mock.ExpectationsWereMet())

	// Replay of the identical callback: the state is already consumed.
	mock.ExpectDel(loginStateKey(state)).SetVal(0)
	w = completeLogin(client, conf, state, cookie, "test-auth-code")

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, int32(1), exchanges.Load(), "replayed callback must not reach the token exchange")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestYahooLoginFlow_ExpiredStateRejected(t *testing.T) {
	t.Parallel()

	tokenServer, exchanges := newTokenServer(t)
	conf := createYahooTestOAuthConfig(tokenServer.URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	// Redis already evicted the pending state (TTL elapsed).
	mock.ExpectDel(loginStateKey(state)).SetVal(0)

	w := completeLogin(client, conf, state, cookie, "test-auth-code")

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "expired")
	assert.Equal(t, int32(0), exchanges.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestYahooLoginFlow_StateIsolatedBetweenBrowsers(t *testing.T) {
	t.Parallel()

	tokenServer, exchanges := newTokenServer(t)
	conf := createYahooTestOAuthConfig(tokenServer.URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	stateA, cookieA := startLogin(t, client, mock, conf)
	stateB, cookieB := startLogin(t, client, mock, conf)
	require.NotEqual(t, stateA, stateB)

	// Browser B is handed browser A's callback URL (state A). B's cookie
	// does not match, so nothing is consumed and no exchange happens.
	w := completeLogin(client, conf, stateA, cookieB, "code-a")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "does not match")

	// A callback with A's state but no cookie at all (unsolicited request).
	w = completeLogin(client, conf, stateA, nil, "code-a")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "missing login state cookie")

	assert.Equal(t, int32(0), exchanges.Load())
	require.NoError(t, mock.ExpectationsWereMet(), "no Redis traffic for rejected callbacks")

	// Browser B completing its own login still works.
	expectSuccessfulExchange(mock, stateB, "code-b")
	w = completeLogin(client, conf, stateB, cookieB, "code-b")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, int32(1), exchanges.Load())
	require.NoError(t, mock.ExpectationsWereMet())

	// Browser A's own login is unaffected by B's success: state A is still pending.
	expectSuccessfulExchange(mock, stateA, "code-a")
	w = completeLogin(client, conf, stateA, cookieA, "code-a")
	assert.Equal(t, http.StatusFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestYahooLoginFlow_CallbackClearsCookie(t *testing.T) {
	t.Parallel()

	tokenServer, _ := newTokenServer(t)
	conf := createYahooTestOAuthConfig(tokenServer.URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	expectSuccessfulExchange(mock, state, "test-auth-code")

	w := completeLogin(client, conf, state, cookie, "test-auth-code")

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, config.YahooLoginStateCookie, cookies[0].Name)
	assert.Equal(t, -1, cookies[0].MaxAge, "callback must expire the single-use cookie")
}

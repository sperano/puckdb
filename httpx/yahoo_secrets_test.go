package httpx

// Tests that the Yahoo callback never exposes credentials through logs or the
// HTTP error body. Sentinel secrets are planted in the request, the token
// endpoint and Redis, and must be absent from everything observable.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sentinelAuthCode     = "SENTINEL-AUTH-CODE-42bd"
	sentinelAccessToken  = "SENTINEL-ACCESS-TOKEN-7f3a"
	sentinelRefreshToken = "SENTINEL-REFRESH-TOKEN-9c1e"
	// Key format: %s_yahoo_oauth2_token (config.DefaultUser = "eric")
	defaultUserTokenKey = "eric_yahoo_oauth2_token"
)

// captureLogs redirects the global zerolog logger to a buffer at the most
// verbose level. Callers must NOT be parallel: log.Logger is a process global.
// Go holds t.Parallel tests until every serial test in the package is done, so
// a serial caller never overlaps with them.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	original := log.Logger
	t.Cleanup(func() { log.Logger = original })

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.TraceLevel)
	return &buf
}

func assertNoSecrets(t *testing.T, where, output string) {
	t.Helper()
	for _, secret := range []string{sentinelAuthCode, sentinelAccessToken, sentinelRefreshToken} {
		assert.NotContains(t, output, secret, "%s must not contain credentials", where)
	}
}

// newSentinelTokenServer is a fake Yahoo token endpoint issuing sentinel tokens.
func newSentinelTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "` + sentinelAccessToken + `",
			"token_type": "Bearer",
			"refresh_token": "` + sentinelRefreshToken + `",
			"expires_in": 3600
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestYahooCallback_SuccessfulExchangeLogsNoCredentials(t *testing.T) {
	logs := captureLogs(t)
	conf := createYahooTestOAuthConfig(newSentinelTokenServer(t).URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	expectSuccessfulExchange(mock, state, sentinelAuthCode)

	w := completeLogin(client, conf, state, cookie, sentinelAuthCode)

	// Authentication behaviour is unchanged: the login completes.
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, testSuccessURL, w.Header().Get("Location"))
	require.NoError(t, mock.ExpectationsWereMet())

	assertNoSecrets(t, "callback log", logs.String())
	assertNoSecrets(t, "callback response", w.Body.String())
	assert.Contains(t, logs.String(), "Saving authentication token", "token save is still logged")
}

func TestYahooCallback_ReplayedCodeLeaksNothing(t *testing.T) {
	logs := captureLogs(t)
	conf := createYahooTestOAuthConfig(newSentinelTokenServer(t).URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	mock.ExpectDel(loginStateKey(state)).SetVal(1)
	mock.ExpectGet(defaultUserTokenKey).RedisNil()
	// SetNX reports the code key already exists: the code was used before.
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(false)

	w := completeLogin(client, conf, state, cookie, sentinelAuthCode)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "already used", "error context is retained")
	assertNoSecrets(t, "replay log", logs.String())
	assertNoSecrets(t, "replay response", w.Body.String())
}

func TestYahooCallback_MalformedStoredTokenLeaksNothing(t *testing.T) {
	logs := captureLogs(t)
	conf := createYahooTestOAuthConfig(newSentinelTokenServer(t).URL)
	client, mock := redismock.NewClientMock()
	mock.MatchExpectationsInOrder(false)

	state, cookie := startLogin(t, client, mock, conf)
	mock.ExpectDel(loginStateKey(state)).SetVal(1)
	// A truncated token is in Redis; the handler logs the load failure and
	// carries on with the exchange.
	mock.ExpectGet(defaultUserTokenKey).SetVal(`{"access_token":"` + sentinelAccessToken + `","refresh_token":"` + sentinelRefreshToken)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", time.Hour).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectSet(defaultUserTokenKey, "x", time.Hour).SetVal("OK")

	w := completeLogin(client, conf, state, cookie, sentinelAuthCode)

	assert.Equal(t, http.StatusFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Contains(t, logs.String(), "Failed to check for existing token")
	assert.Contains(t, logs.String(), defaultUserTokenKey, "the failing key is still reported")
	assertNoSecrets(t, "malformed token log", logs.String())
	assertNoSecrets(t, "malformed token response", w.Body.String())
}

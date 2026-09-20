package cache

// Tests that OAuth credentials never reach logs or error messages. Each one
// plants sentinel secrets and asserts they are absent from everything the code
// emits for diagnostics.

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	sentinelAccessToken  = "SENTINEL-ACCESS-TOKEN-7f3a"
	sentinelRefreshToken = "SENTINEL-REFRESH-TOKEN-9c1e"
	sentinelAuthCode     = "SENTINEL-AUTH-CODE-42bd"
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
	for _, secret := range []string{sentinelAccessToken, sentinelRefreshToken, sentinelAuthCode} {
		assert.NotContains(t, output, secret, "%s must not contain credentials", where)
	}
}

func TestSaveToken_DoesNotLogCredentials(t *testing.T) {
	logs := captureLogs(t)
	client, mock := redismock.NewClientMock()
	// A refreshable token is stored without TTL, so the expected SET has no
	// expiry argument; anyArgsMatch ignores the values themselves.
	mock.CustomMatch(anyArgsMatch).ExpectSet(getRedisKeyForToken(oauthTestUser), "x", 0).SetVal("OK")

	expiry := time.Now().Add(time.Hour)
	token := &oauth2.Token{
		AccessToken:  sentinelAccessToken,
		RefreshToken: sentinelRefreshToken,
		TokenType:    "Bearer",
		Expiry:       expiry,
	}
	require.NoError(t, SaveToken(ctxWithUser(oauthTestUser), client, token))
	require.NoError(t, mock.ExpectationsWereMet())

	assertNoSecrets(t, "token save log", logs.String())
	// The non-secret metadata is still there for diagnostics.
	assert.Contains(t, logs.String(), oauthTestUser)
	assert.Contains(t, logs.String(), `"expiry"`)
}

func TestLoadTokenForUser_MalformedTokenDoesNotLeakCredentials(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"truncated", `{"access_token":"` + sentinelAccessToken + `","refresh_token":"` + sentinelRefreshToken},
		{"wrong type", `{"access_token":"` + sentinelAccessToken + `","refresh_token":"` + sentinelRefreshToken + `","token_type":12345}`},
		{"bare payload", sentinelAccessToken},
		{"bad expiry", `{"access_token":"` + sentinelAccessToken + `","expiry":"` + sentinelRefreshToken + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			client, mock := redismock.NewClientMock()
			key := getRedisKeyForToken(oauthTestUser)
			mock.ExpectGet(key).SetVal(tc.payload)

			token, err := LoadTokenForUser(context.Background(), client, oauthTestUser)

			require.Error(t, err)
			assert.Nil(t, token)
			var decodeErr *TokenDecodeError
			require.ErrorAs(t, err, &decodeErr)
			assert.Equal(t, key, decodeErr.Key, "error keeps the key for context")
			assert.NotEmpty(t, decodeErr.Reason)
			assertNoSecrets(t, "token decode error", err.Error())
			assertNoSecrets(t, "token load log", logs.String())
		})
	}
}

func TestMarkAuthCodeAsUsed_DoesNotLogOrStoreRawCode(t *testing.T) {
	logs := captureLogs(t)
	client, mock := redismock.NewClientMock()
	key := getRedisKeyForAuthCode(sentinelAuthCode)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX(key, "used", time.Hour).SetVal(true)

	require.NoError(t, MarkAuthCodeAsUsed(context.Background(), client, sentinelAuthCode))
	require.NoError(t, mock.ExpectationsWereMet())

	assertNoSecrets(t, "auth code redis key", key)
	assertNoSecrets(t, "auth code log", logs.String())
	// Yahoo's codes are short enough to brute-force from an unsalted digest,
	// so the derived key must stay out of the logs too.
	assert.NotContains(t, logs.String(), key, "the digest-derived key is sensitive")
	assert.Contains(t, logs.String(), "Marked authorization code as used")
}

func TestMarkAuthCodeAsUsed_ReplayErrorDoesNotLeakCode(t *testing.T) {
	logs := captureLogs(t)
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX(getRedisKeyForAuthCode(sentinelAuthCode), "used", time.Hour).SetVal(false)

	err := MarkAuthCodeAsUsed(context.Background(), client, sentinelAuthCode)

	require.Error(t, err)
	assertNoSecrets(t, "auth code replay error", err.Error())
	assertNoSecrets(t, "auth code replay log", logs.String())
}

func TestGetRedisKeyForAuthCode_StableAndDistinct(t *testing.T) {
	t.Parallel()
	// Replay protection depends on the same code always mapping to the same
	// key, and on different codes never sharing one.
	assert.Equal(t, getRedisKeyForAuthCode(sentinelAuthCode), getRedisKeyForAuthCode(sentinelAuthCode))
	assert.NotEqual(t, getRedisKeyForAuthCode(sentinelAuthCode), getRedisKeyForAuthCode(sentinelAuthCode+"x"))
}

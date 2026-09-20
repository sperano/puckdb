package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	oauthTestUser     = "testuser"
	oauthTestAuthCode = "code123"
)

// ctxWithUser produces a context that satisfies config.UserFromContext.
// SaveToken and LoadToken read the current user from context and would
// fail before reaching Redis if the value isn't present.
func ctxWithUser(user string) context.Context {
	return context.WithValue(context.Background(), config.CtxUser, user)
}

// anyArgsMatch is the wildcard matcher used throughout the cache_test
// package. SetNX in particular includes a TTL expectation that's hard
// to assert exactly because the production code computes time.Until
// at call time.
func anyArgsMatch(expected, actual []any) error { return nil }

func TestTokenAsString(t *testing.T) {
	t.Parallel()

	token := &oauth2.Token{
		AccessToken:  "abc123",
		TokenType:    "Bearer",
		RefreshToken: "refresh456",
	}

	s, err := TokenAsString(token)
	require.NoError(t, err)
	assert.Contains(t, s, "abc123")
	assert.Contains(t, s, "Bearer")
	assert.Contains(t, s, "refresh456")
}

func TestGetRedisKeyForToken(t *testing.T) {
	t.Parallel()
	key := getRedisKeyForToken(oauthTestUser)
	assert.Contains(t, key, oauthTestUser)
}

func TestGetRedisKeyForAuthCode(t *testing.T) {
	t.Parallel()
	key := getRedisKeyForAuthCode(oauthTestAuthCode)
	// The key is derived from a digest: the code is a credential.
	assert.NotContains(t, key, oauthTestAuthCode)
}

// --- SaveToken ---

// expectTokenSet registers the SET of the user's token record and returns the
// wire arguments actually sent, so a test can assert the stored JSON and the
// TTL rather than accept whatever was written. shapeTTL only picks the shape
// of the expected command (go-redis sends "EX <s>" for a whole-second TTL and
// nothing for 0); the recorded arguments carry the real value.
func expectTokenSet(mock redismock.ClientMock, shapeTTL time.Duration) *[]any {
	var sent []any
	mock.CustomMatch(recordingMatch(&sent)).ExpectSet(getRedisKeyForToken(oauthTestUser), "x", shapeTTL).SetVal("OK")
	return &sent
}

func TestSaveToken_RefreshableRecordOutlivesAccessToken(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	sent := expectTokenSet(mock, 0)

	token := &oauth2.Token{
		AccessToken:  "abc",
		TokenType:    "Bearer",
		RefreshToken: "r1",
		Expiry:       time.Now().Add(time.Hour),
	}
	require.NoError(t, SaveToken(ctxWithUser(oauthTestUser), client, token))
	require.NoError(t, mock.ExpectationsWereMet())

	// The refresh token is what serves the next request after the access
	// token expires, so the record must carry no TTL at all.
	want, err := TokenAsString(token)
	require.NoError(t, err)
	assert.Equal(t, []any{"set", getRedisKeyForToken(oauthTestUser), want}, *sent)
}

func TestSaveToken_UnrefreshableRecordExpiresWithAccessToken(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	sent := expectTokenSet(mock, time.Hour)

	token := &oauth2.Token{AccessToken: "abc", Expiry: time.Now().Add(time.Hour)}
	require.NoError(t, SaveToken(ctxWithUser(oauthTestUser), client, token))
	require.NoError(t, mock.ExpectationsWereMet())

	want, err := TokenAsString(token)
	require.NoError(t, err)
	// time.Until yields a sub-second duration, which go-redis sends as PX
	// milliseconds.
	require.Len(t, *sent, 5)
	assert.Equal(t, []any{"set", getRedisKeyForToken(oauthTestUser), want, "px"}, (*sent)[:4])
	ttl, ok := (*sent)[4].(int64)
	require.True(t, ok, "TTL is sent as an integer")
	assert.InDelta(t, time.Hour.Milliseconds(), ttl, float64((2 * time.Second).Milliseconds()), "TTL follows the access token's expiry")
}

func TestSaveToken_NoExpiryPersists(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	sent := expectTokenSet(mock, 0)

	token := &oauth2.Token{AccessToken: "abc", TokenType: "Bearer"}
	require.NoError(t, SaveToken(ctxWithUser(oauthTestUser), client, token))
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Len(t, *sent, 3, "a token without expiry is stored without TTL")
}

func TestSaveToken_ExpiredUnrefreshableRejected(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()

	// No expectations: a dead credential must never reach Redis. A TTL of
	// 0 would mean "keep forever" to go-redis, the opposite of what an
	// already-expired token deserves.
	expired := &oauth2.Token{AccessToken: "abc", Expiry: time.Now().Add(-1 * time.Hour)}
	err := SaveToken(ctxWithUser(oauthTestUser), client, expired)

	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestSaveTokenForUser_KeyedByGivenUser(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	sent := expectTokenSet(mock, 0)

	// No user in the context: the explicit argument decides the key.
	token := &oauth2.Token{AccessToken: "abc", RefreshToken: "r1"}
	require.NoError(t, SaveTokenForUser(context.Background(), client, oauthTestUser, token))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, getRedisKeyForToken(oauthTestUser), (*sent)[1])
}

func TestSaveToken_MissingUserContext(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()

	token := &oauth2.Token{AccessToken: "abc"}
	err := SaveToken(context.Background(), client, token)

	// No expectations set on the mock — if the function reached Redis
	// this would fail with an unexpected-call error instead.
	require.Error(t, err)
}

// --- LoadToken / LoadTokenForUser ---

func TestLoadTokenForUser(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	tokenJSON := `{"access_token":"abc","token_type":"Bearer","refresh_token":"r1"}`
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(tokenJSON)

	got, err := LoadTokenForUser(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "abc", got.AccessToken)
	assert.Equal(t, "Bearer", got.TokenType)
	assert.Equal(t, "r1", got.RefreshToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadTokenForUser_Missing(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).RedisNil()

	got, err := LoadTokenForUser(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.Nil(t, got)
	var missing *OAuth2TokenMissingError
	assert.True(t, errors.As(err, &missing))
}

func TestLoadTokenForUser_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetErr(errors.New("connection refused"))

	got, err := LoadTokenForUser(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.Nil(t, got)
	var readErr *CacheReadError
	assert.True(t, errors.As(err, &readErr))
}

func TestLoadTokenForUser_InvalidJSON(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal("not json")

	got, err := LoadTokenForUser(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.Nil(t, got)
}

func TestLoadToken_UsesContextUser(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc"}`)

	got, err := LoadToken(ctxWithUser(oauthTestUser), client)

	require.NoError(t, err)
	assert.Equal(t, "abc", got.AccessToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadToken_MissingUserContext(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()

	got, err := LoadToken(context.Background(), client)
	require.Error(t, err)
	assert.Nil(t, got)
}

// --- DeleteTokenForUser ---

func TestDeleteTokenForUser(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectDel(getRedisKeyForToken(oauthTestUser)).SetVal(1)

	err := DeleteTokenForUser(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- MarkAuthCodeAsUsed ---

func TestMarkAuthCodeAsUsed_Fresh(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX(getRedisKeyForAuthCode(oauthTestAuthCode), "used", time.Hour).SetVal(true)

	err := MarkAuthCodeAsUsed(context.Background(), client, oauthTestAuthCode)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAuthCodeAsUsed_AlreadyUsed(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	// SetNX returning false means the key already existed — the second
	// caller of an authorization code must be rejected per OAuth2 spec.
	mock.CustomMatch(anyArgsMatch).ExpectSetNX(getRedisKeyForAuthCode(oauthTestAuthCode), "used", time.Hour).SetVal(false)

	err := MarkAuthCodeAsUsed(context.Background(), client, oauthTestAuthCode)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already used")
}

func TestMarkAuthCodeAsUsed_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX(getRedisKeyForAuthCode(oauthTestAuthCode), "used", time.Hour).SetErr(errors.New("network down"))

	err := MarkAuthCodeAsUsed(context.Background(), client, oauthTestAuthCode)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "already used")
}

// --- NewLoginState / ConsumeLoginState ---

// recordingMatch accepts any command but keeps the actual wire arguments so
// the test can inspect a key it could not know in advance (the state is
// random).
func recordingMatch(dst *[]any) func(expected, actual []any) error {
	return func(expected, actual []any) error {
		*dst = append([]any(nil), actual...)
		return nil
	}
}

func TestNewLoginState_Fresh(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	var sent []any
	mock.CustomMatch(recordingMatch(&sent)).ExpectSetNX("any", "pending", LoginStateTTL).SetVal(true)

	state, err := NewLoginState(context.Background(), client)

	require.NoError(t, err)
	// 32 random bytes, base64url without padding.
	assert.Len(t, state, 43)
	assert.NotContains(t, state, "=")
	// The pending entry must be keyed by the very state handed back to the
	// caller and carry the TTL (go-redis encodes SetNX+TTL as SET ... EX <s> NX).
	assert.Contains(t, sent, getRedisKeyForLoginState(state))
	assert.Contains(t, sent, int64(LoginStateTTL.Seconds()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewLoginState_Unique(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", LoginStateTTL).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", LoginStateTTL).SetVal(true)

	first, err := NewLoginState(context.Background(), client)
	require.NoError(t, err)
	second, err := NewLoginState(context.Background(), client)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
}

func TestNewLoginState_Collision(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", LoginStateTTL).SetVal(false)

	state, err := NewLoginState(context.Background(), client)

	require.Error(t, err)
	assert.Empty(t, state)
}

func TestNewLoginState_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "pending", LoginStateTTL).SetErr(errors.New("network down"))

	state, err := NewLoginState(context.Background(), client)

	require.Error(t, err)
	assert.Empty(t, state)
}

func TestConsumeLoginState_Pending(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectDel(getRedisKeyForLoginState("abc")).SetVal(1)

	require.NoError(t, ConsumeLoginState(context.Background(), client, "abc"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConsumeLoginState_MissingExpiredOrReplayed(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	// DEL removing nothing covers all three cases: never issued, TTL
	// elapsed, or already consumed by a previous callback.
	mock.ExpectDel(getRedisKeyForLoginState("abc")).SetVal(0)

	err := ConsumeLoginState(context.Background(), client, "abc")

	require.ErrorIs(t, err, ErrLoginStateInvalid)
}

func TestConsumeLoginState_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectDel(getRedisKeyForLoginState("abc")).SetErr(errors.New("network down"))

	err := ConsumeLoginState(context.Background(), client, "abc")

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrLoginStateInvalid)
}

// Sanity check: the client returned by redismock satisfies the local
// tokenClient interface. Go's type-system already enforces this at the
// SaveToken etc. call sites above, but pinning it explicitly catches a
// regression where someone shrinks tokenClient and breaks production
// callers (which still use *redis.Client) without breaking the tests.
var _ tokenClient = (*redis.Client)(nil)

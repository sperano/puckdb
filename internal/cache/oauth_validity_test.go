package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// --- HasValidToken ---

func TestHasValidToken_NotExpired(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339Nano)
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc","expiry":"` + future + `"}`)

	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.True(t, valid)
}

func TestHasValidToken_Expired(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc","expiry":"` + past + `"}`)

	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestHasValidToken_NoExpiry(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	// Tokens without an expiry are treated as valid — this matches the
	// production semantics in HasValidToken.
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc"}`)

	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.True(t, valid)
}

func TestHasValidToken_Missing(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).RedisNil()

	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	// Missing token returns (false, nil) — distinct from "Redis errored".
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestHasValidToken_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetErr(errors.New("network down"))

	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.False(t, valid)
}

func TestHasValidToken_ExpiredWithRefreshToken(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc","refresh_token":"r1","expiry":"` + past + `"}`)

	// Only the access token's own window counts here: the login callback
	// uses this to decide whether a fresh code exchange is redundant.
	valid, err := HasValidToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.False(t, valid)
}

// --- TokenUsable / HasUsableToken ---

func TestTokenUsable(t *testing.T) {
	t.Parallel()
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	cases := []struct {
		name  string
		token *oauth2.Token
		want  bool
	}{
		{"fresh access token", &oauth2.Token{AccessToken: "abc", Expiry: future}, true},
		{"no expiry", &oauth2.Token{AccessToken: "abc"}, true},
		{"expired, refreshable", &oauth2.Token{AccessToken: "abc", RefreshToken: "r1", Expiry: past}, true},
		{"expired, unrefreshable", &oauth2.Token{AccessToken: "abc", Expiry: past}, false},
		{"empty access token, refreshable", &oauth2.Token{RefreshToken: "r1"}, true},
		{"empty", &oauth2.Token{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, TokenUsable(tc.token))
		})
	}
}

func TestHasUsableToken_ExpiredWithRefreshToken(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc","refresh_token":"r1","expiry":"` + past + `"}`)

	// An idle user is still logged in: the next request refreshes.
	usable, err := HasUsableToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.True(t, usable)
}

func TestHasUsableToken_ExpiredWithoutRefreshToken(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetVal(`{"access_token":"abc","expiry":"` + past + `"}`)

	usable, err := HasUsableToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.False(t, usable)
}

func TestHasUsableToken_Missing(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).RedisNil()

	usable, err := HasUsableToken(context.Background(), client, oauthTestUser)

	require.NoError(t, err)
	assert.False(t, usable)
}

func TestHasUsableToken_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet(getRedisKeyForToken(oauthTestUser)).SetErr(errors.New("network down"))

	usable, err := HasUsableToken(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.False(t, usable)
}

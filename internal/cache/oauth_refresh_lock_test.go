package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lock value is random, so both the obtain (SET ... EX <s> NX) and the
// release (EVALSHA <script> 1 <key> <value>) are matched by shape and their
// recorded arguments inspected.

func TestLockTokenRefresh_ObtainAndRelease(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	var obtained, released []any
	mock.CustomMatch(recordingMatch(&obtained)).ExpectSetNX("any", "x", TokenRefreshLockTTL).SetVal(true)
	mock.CustomMatch(recordingMatch(&released)).ExpectEvalSha("any", []string{"any"}, "x").SetVal(int64(1))

	release, err := LockTokenRefresh(context.Background(), client, oauthTestUser)
	require.NoError(t, err)
	release()

	require.NoError(t, mock.ExpectationsWereMet())
	key := getRedisKeyForTokenRefreshLock(oauthTestUser)
	assert.Contains(t, obtained, key)
	assert.Contains(t, obtained, int64(TokenRefreshLockTTL.Seconds()), "the lock expires on its own if the holder dies")
	// Release is keyed on the same lock and proves ownership with the value
	// that was set, so a lock re-acquired by someone else is left alone.
	lockValue := obtained[2]
	assert.Contains(t, released, key)
	assert.Contains(t, released, lockValue)
}

func TestLockTokenRefresh_ReleasesAfterCallerContextCancelled(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", TokenRefreshLockTTL).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectEvalSha("any", []string{"any"}, "x").SetVal(int64(1))

	ctx, cancel := context.WithCancel(context.Background())
	release, err := LockTokenRefresh(ctx, client, oauthTestUser)
	require.NoError(t, err)

	// A request deadline firing mid-refresh must not leave the lock to
	// its TTL: the release still reaches Redis.
	cancel()
	release()

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockTokenRefresh_HeldElsewhere(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", TokenRefreshLockTTL).SetVal(false)

	// The waiter gives up when its context ends; a short deadline keeps the
	// test from waiting a full retry interval.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	release, err := LockTokenRefresh(ctx, client, oauthTestUser)

	require.ErrorIs(t, err, ErrTokenRefreshBusy)
	assert.Nil(t, release)
}

func TestLockTokenRefresh_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", TokenRefreshLockTTL).SetErr(errors.New("network down"))

	release, err := LockTokenRefresh(context.Background(), client, oauthTestUser)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTokenRefreshBusy)
	assert.Nil(t, release)
}

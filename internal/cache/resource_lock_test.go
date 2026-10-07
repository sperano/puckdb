package cache

import (
	"context"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resourceLockTestKey       = "resource-test-key"
	resourceLockTestWait      = 10 * time.Millisecond
	resourceLockReleaseResult = int64(1)
)

func TestLockResourcePreservesKeyAndTTL(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	var obtained, released []any
	mock.CustomMatch(recordingMatch(&obtained)).ExpectSetNX("any", "x", resourceCacheLockTTL).SetVal(true)
	mock.CustomMatch(recordingMatch(&released)).ExpectEvalSha("any", []string{"any"}, "x").SetVal(resourceLockReleaseResult)

	release, err := NewGobCache(client).LockResource(context.Background(), resourceLockTestKey)
	require.NoError(t, err)
	release()

	require.NoError(t, mock.ExpectationsWereMet())
	key := resourceCacheLockPrefix + resourceLockTestKey
	assert.Contains(t, obtained, key)
	assert.Contains(t, obtained, int64(resourceCacheLockTTL.Seconds()))
	assert.Contains(t, released, key)
	assert.Contains(t, released, obtained[2])
}

func TestLockResourceMapsContentionWithContext(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", resourceCacheLockTTL).SetVal(false)
	ctx, cancel := context.WithTimeout(context.Background(), resourceLockTestWait)
	defer cancel()

	release, err := NewGobCache(client).LockResource(ctx, resourceLockTestKey)

	require.ErrorIs(t, err, redislock.ErrNotObtained)
	assert.ErrorContains(t, err, "context ended before the lock was available")
	assert.ErrorContains(t, err, resourceLockTestKey)
	assert.Nil(t, release)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockResourceNilCacheIsNoOp(t *testing.T) {
	t.Parallel()
	var cache *GobCache
	release, err := cache.LockResource(context.Background(), resourceLockTestKey)
	require.NoError(t, err)
	assert.NotPanics(t, release)
}

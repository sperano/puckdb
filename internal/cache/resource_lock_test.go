package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sperano/puckdb/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resourceLockTestKey         = "resource-test-key"
	resourceLockTestWait        = 10 * time.Millisecond
	resourceLockReleaseResult   = int64(1)
	redisLockValueArgumentIndex = 4 // EVALSHA sha numkeys key value ...
)

func TestLockResourcePreservesKeyAndTTL(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	var obtained, released []any
	mock.CustomMatch(recordingMatch(&obtained)).ExpectEvalSha("any", []string{"any"}, "x", 0, "ttl").SetVal(int64(1))
	mock.CustomMatch(recordingMatch(&released)).ExpectEvalSha("any", []string{"any"}, "x").SetVal(resourceLockReleaseResult)

	release, err := NewGobCache(client).LockResource(context.Background(), resourceLockTestKey)
	require.NoError(t, err)
	release()

	require.NoError(t, mock.ExpectationsWereMet())
	key := resourceCacheLockPrefix + resourceLockTestKey
	assert.Contains(t, obtained, key)
	assert.Contains(t, obtained, strconv.FormatInt(resourceCacheLockTTL.Milliseconds(), 10))
	assert.Contains(t, released, key)
	require.Greater(t, len(obtained), redisLockValueArgumentIndex)
	assert.Contains(t, released, obtained[redisLockValueArgumentIndex])
}

func TestLockResourceMapsContentionWithContext(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectEvalSha("any", []string{"any"}, "x", 0, "ttl").SetErr(redis.Nil)
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

// The lock set is the mutable league-level resources plus the daily standings
// capture, whose first miss of a day must be the only download.
func TestRequiresCoherentResourceLock(t *testing.T) {
	t.Parallel()
	for _, ft := range []core.FileType{core.League, core.YahooDraftResults, core.YahooLeagueStandings} {
		assert.True(t, RequiresCoherentResourceLock(ft), ft.String())
	}
	for _, ft := range []core.FileType{core.YahooTransactions, core.YahooLeaguePlayers, core.Boxscore} {
		assert.False(t, RequiresCoherentResourceLock(ft), ft.String())
	}
}

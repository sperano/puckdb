package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRedisLockKey            = "puckdb:test-lock"
	testRedisLockTTL            = time.Minute
	testRedisLockRetry          = 5 * time.Millisecond
	testRedisLockReleaseTimeout = 100 * time.Millisecond
	testMinimumReleaseTimeLeft  = testRedisLockReleaseTimeout / 2
	testRedisLockCallTimeout    = time.Second
	testReleaseSuccess          = int64(1)
)

var errTestRedisLockBusy = errors.New("test lock is busy")

func testRedisLockConfig() redisLockConfig {
	return redisLockConfig{
		key:             testRedisLockKey,
		ttl:             testRedisLockTTL,
		retry:           testRedisLockRetry,
		releaseTimeout:  testRedisLockReleaseTimeout,
		busyErr:         errTestRedisLockBusy,
		releaseLogField: "test_lock",
		releaseLogValue: testRedisLockKey,
		releaseLogMsg:   "failed to release test lock",
	}
}

func TestAcquireRedisLockMapsNotObtainedToBusyError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", testRedisLockTTL).SetVal(false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := acquireRedisLock(ctx, client, testRedisLockConfig())

	require.ErrorIs(t, err, errTestRedisLockBusy)
	assert.Nil(t, release)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAcquireRedisLockRetriesWithConfiguredBackoff(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", testRedisLockTTL).SetVal(false)
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", testRedisLockTTL).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectEvalSha("any", []string{"any"}, "x").SetVal(testReleaseSuccess)
	ctx, cancel := context.WithTimeout(context.Background(), testRedisLockCallTimeout)
	defer cancel()

	started := time.Now()
	release, err := acquireRedisLock(ctx, client, testRedisLockConfig())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(started), testRedisLockRetry)
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

type releaseContextClient struct {
	redislock.RedisClient
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (client *releaseContextClient) EvalSha(
	ctx context.Context,
	sha1 string,
	keys []string,
	args ...interface{},
) *redis.Cmd {
	client.err = ctx.Err()
	client.deadline, client.hasDeadline = ctx.Deadline()
	return client.RedisClient.EvalSha(ctx, sha1, keys, args...)
}

func TestAcquireRedisLockReleaseIgnoresCancellationAndHasDeadline(t *testing.T) {
	t.Parallel()
	redisClient, mock := redismock.NewClientMock()
	client := &releaseContextClient{RedisClient: redisClient}
	mock.CustomMatch(anyArgsMatch).ExpectSetNX("any", "x", testRedisLockTTL).SetVal(true)
	mock.CustomMatch(anyArgsMatch).ExpectEvalSha("any", []string{"any"}, "x").SetVal(testReleaseSuccess)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := acquireRedisLock(ctx, client, testRedisLockConfig())
	require.NoError(t, err)

	cancel()
	release()

	assert.NoError(t, client.err)
	require.True(t, client.hasDeadline)
	remaining := time.Until(client.deadline)
	assert.Greater(t, remaining, testMinimumReleaseTimeLeft)
	assert.LessOrEqual(t, remaining, testRedisLockReleaseTimeout)
	require.NoError(t, mock.ExpectationsWereMet())
}

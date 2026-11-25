package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// testDeploymentRate is 2 req/s: one slot every testDeploymentInterval.
	testDeploymentRate     = 2
	testDeploymentInterval = 500 * time.Millisecond
	testDeploymentBurst    = 2
)

// testRedisEpoch is the Redis server time the tests start at, far from the
// test machine's clock: only the server clock may count.
var testRedisEpoch = time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)

// newTestRedis starts a miniredis whose clock (TIME) stands at testRedisEpoch.
func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(testRedisEpoch)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	return server, client
}

func newTestDeploymentLimiter(t *testing.T, client *redis.Client) *DeploymentRateLimiter {
	t.Helper()
	limiter, err := NewDeploymentRateLimiter(client, YahooRateLimit{PerSecond: testDeploymentRate, Burst: testDeploymentBurst})
	require.NoError(t, err)
	return limiter
}

func reserve(t *testing.T, limiter RateLimiter) Reservation {
	t.Helper()
	reservation, err := limiter.Reserve(context.Background())
	require.NoError(t, err)
	return reservation
}

func TestDeploymentRateLimiter_BurstThenSpacesAtRate(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)

	var delays []time.Duration
	for range 5 {
		r := reserve(t, limiter)
		assert.Equal(t, metrics.RateLimiterDeployment, r.Limiter)
		delays = append(delays, r.Delay)
	}

	assert.Equal(t, []time.Duration{0, 0, testDeploymentInterval, 2 * testDeploymentInterval, 3 * testDeploymentInterval}, delays,
		"the burst starts at once, then one slot per interval")
}

func TestDeploymentRateLimiter_ProcessesShareOneBucket(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	first := newTestDeploymentLimiter(t, client)
	second := newTestDeploymentLimiter(t, client)

	reserve(t, first)
	reserve(t, second)

	assert.Equal(t, testDeploymentInterval, reserve(t, first).Delay, "the burst was spent by both processes together")
	assert.Equal(t, 2*testDeploymentInterval, reserve(t, second).Delay)
}

func TestDeploymentRateLimiter_RefillsWithServerTime(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst + 1 {
		reserve(t, limiter)
	}

	server.SetTime(testRedisEpoch.Add(testDeploymentInterval))
	assert.Equal(t, testDeploymentInterval, reserve(t, limiter).Delay, "one interval passed on the server clock")

	server.SetTime(testRedisEpoch.Add(time.Hour))
	assert.Zero(t, reserve(t, limiter).Delay, "an idle bucket refills")
	assert.Zero(t, reserve(t, limiter).Delay, "up to its burst")
	assert.Equal(t, testDeploymentInterval, reserve(t, limiter).Delay)
}

func TestDeploymentRateLimiter_CancelGivesQueuedSlotBack(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst {
		reserve(t, limiter)
	}
	queued := reserve(t, limiter)
	require.Equal(t, testDeploymentInterval, queued.Delay)

	queued.Cancel()

	assert.Equal(t, testDeploymentInterval, reserve(t, limiter).Delay, "the next request takes the canceled slot")
}

func TestDeploymentRateLimiter_CancelKeepsSlotWhoseTimeCame(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	immediate := reserve(t, limiter)
	require.Zero(t, immediate.Delay)

	immediate.Cancel()

	reserve(t, limiter)
	assert.Equal(t, testDeploymentInterval, reserve(t, limiter).Delay, "a used slot is not given back")
}

func TestDeploymentRateLimiter_KeyExpiresWhenBucketIsFull(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	reserve(t, limiter)

	require.True(t, server.Exists(YahooRateLimitKey))
	ttl := server.TTL(YahooRateLimitKey)
	assert.Positive(t, ttl)
	assert.LessOrEqual(t, ttl, testDeploymentInterval+time.Millisecond)
}

func TestDeploymentRateLimiter_FallsBackWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	server.Close()

	var delays []time.Duration
	for range testDeploymentBurst + 1 {
		r := reserve(t, limiter)
		assert.Equal(t, metrics.RateLimiterFallback, r.Limiter)
		delays = append(delays, r.Delay)
	}

	assert.Zero(t, delays[0])
	assert.Zero(t, delays[1])
	assert.InDelta(t, testDeploymentInterval, delays[2], float64(testDeploymentInterval)/10,
		"the fallback limits this process at the configured rate")
	assert.False(t, limiter.redisUsable(), "Redis is not asked again right away")
}

func TestDeploymentRateLimiter_RetriesRedisAfterFailure(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	server.Close()
	require.Equal(t, metrics.RateLimiterFallback, reserve(t, limiter).Limiter)

	require.NoError(t, server.Restart())
	now = now.Add(redisRateLimitRetryAfter - time.Millisecond)
	assert.Equal(t, metrics.RateLimiterFallback, reserve(t, limiter).Limiter, "still within the retry delay")
	now = now.Add(time.Millisecond)
	assert.Equal(t, metrics.RateLimiterDeployment, reserve(t, limiter).Limiter)
}

func TestDeploymentRateLimiter_FallsBackOnScriptError(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	server.HSet(YahooRateLimitKey, "not", "a string")
	limiter := newTestDeploymentLimiter(t, client)

	assert.Equal(t, metrics.RateLimiterFallback, reserve(t, limiter).Limiter)
}

func TestDeploymentRateLimiter_WarnsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	errDown := errors.New("down")

	assert.True(t, limiter.redisFailed(errDown), "the first failure warns")
	now = now.Add(redisRateLimitWarnEvery - time.Second)
	assert.False(t, limiter.redisFailed(errDown))
	now = now.Add(time.Second)
	assert.True(t, limiter.redisFailed(errDown))
}

func TestDeploymentRateLimiter_CanceledContextIsNotARedisFailure(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := limiter.Reserve(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, limiter.redisUsable())
}

func TestNewDeploymentRateLimiter_Validates(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)

	_, err := NewDeploymentRateLimiter(client, YahooRateLimit{PerSecond: 0, Burst: 1})
	require.ErrorIs(t, err, ErrInvalidYahooRateLimit)
	_, err = NewDeploymentRateLimiter(nil, DefaultYahooRateLimit())
	require.ErrorIs(t, err, ErrInvalidYahooRateLimit)
}

func TestRateLimited_WaitsOnDeploymentLimiter(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter, err := NewDeploymentRateLimiter(client, YahooRateLimit{PerSecond: float64(time.Second / testRequestInterval), Burst: 1})
	require.NoError(t, err)
	download := RateLimited(limiter, func(context.Context, string) ([]byte, error) { return nil, nil })

	begin := time.Now()
	for range 3 {
		_, err := download(context.Background(), "https://fantasysports.yahooapis.com/")
		require.NoError(t, err)
	}

	assert.GreaterOrEqual(t, time.Since(begin), 2*testRequestInterval-timerSlack,
		"the miniredis clock stands still, so each download waits one more interval")
}

package shared

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
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
	// testDeploymentMaxWait admits three intervals of queueing.
	testDeploymentMaxWait = 3 * testDeploymentInterval
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

// testDeploymentLimit is 2 req/s, a burst of 2 and three intervals of queueing.
func testDeploymentLimit() YahooRateLimit {
	return YahooRateLimit{PerSecond: testDeploymentRate, Burst: testDeploymentBurst, MaxWait: testDeploymentMaxWait}
}

func newTestDeploymentLimiter(t *testing.T, client *redis.Client) *DeploymentRateLimiter {
	t.Helper()
	limiter, err := NewDeploymentRateLimiter(client, YahooRateLimitKey, testDeploymentLimit())
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

func TestDeploymentRateLimiter_RefusesSlotPastMaxWaitWithoutTakingIt(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range 5 {
		reserve(t, limiter)
	}
	stored := server.HGet(YahooRateLimitKey, "tat")

	_, err := limiter.Reserve(context.Background())

	require.ErrorIs(t, err, ErrYahooRateLimitBusy)
	assert.Equal(t, stored, server.HGet(YahooRateLimitKey, "tat"), "a refused request takes no slot")
	assert.True(t, limiter.redisUsable(), "a busy bucket is not a Redis failure")
	server.SetTime(testRedisEpoch.Add(testDeploymentInterval))
	assert.Equal(t, testDeploymentMaxWait, reserve(t, limiter).Delay, "the queue admits again once it shortened")
}

func TestDeploymentRateLimiter_DraftKeyIsItsOwnBucket(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	workers := newTestDeploymentLimiter(t, client)
	draft, err := NewDeploymentRateLimiter(client, YahooDraftRateLimitKey, testDeploymentLimit())
	require.NoError(t, err)
	for range 5 {
		reserve(t, workers)
	}

	assert.Zero(t, reserve(t, draft).Delay, "draft polls do not queue behind the workers' downloads")
}

func TestDeploymentRateLimiter_StoresItsInterval(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)

	reserve(t, limiter)

	assert.Equal(t, strconv.FormatInt(testDeploymentInterval.Microseconds(), 10), server.HGet(YahooRateLimitKey, "interval"))
	assert.True(t, limiter.mismatchedWarn.last.IsZero(), "the same interval is no mismatch")
}

func TestDeploymentRateLimiter_WarnsOnIntervalMismatch(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	slower, err := NewDeploymentRateLimiter(client, YahooRateLimitKey,
		YahooRateLimit{PerSecond: testDeploymentRate / 2, Burst: testDeploymentBurst, MaxWait: time.Hour})
	require.NoError(t, err)
	limiter := newTestDeploymentLimiter(t, client)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	reserve(t, slower)

	reserve(t, limiter)

	assert.Equal(t, now, limiter.mismatchedWarn.last, "the mismatch is logged")
	assert.Equal(t, metrics.RateLimiterDeployment, reserve(t, limiter).Limiter, "and changes nothing else")
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

func TestDeploymentRateLimiter_CancelOfEarlierReservationLeavesGap(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst {
		reserve(t, limiter)
	}
	middle := reserve(t, limiter)
	last := reserve(t, limiter)
	require.Equal(t, 2*testDeploymentInterval, last.Delay)

	middle.Cancel()

	assert.Equal(t, 3*testDeploymentInterval, reserve(t, limiter).Delay,
		"the next request queues after the last reservation instead of sharing its slot")
}

func TestDeploymentRateLimiter_CancelTwiceGivesBackOneSlot(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst + 1 {
		reserve(t, limiter)
	}
	queued := reserve(t, limiter)
	require.Equal(t, 2*testDeploymentInterval, queued.Delay)

	queued.Cancel()
	queued.Cancel()

	assert.Equal(t, 2*testDeploymentInterval, reserve(t, limiter).Delay, "only the canceled slot is given back")
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
	assert.GreaterOrEqual(t, ttl, testDeploymentInterval, "the bucket state lives until the bucket is full again")
	assert.LessOrEqual(t, ttl, testDeploymentInterval+time.Millisecond)
	server.FastForward(testDeploymentInterval - time.Millisecond)
	assert.True(t, server.Exists(YahooRateLimitKey), "kept before the bucket is full")
	server.FastForward(2 * time.Millisecond)
	assert.False(t, server.Exists(YahooRateLimitKey), "gone once it is")
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
	require.NoError(t, server.Set(YahooRateLimitKey, "not a hash"))
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

	first := now
	limiter.redisFailed(errDown)
	assert.Equal(t, first, limiter.fallbackWarn.last, "the first failure warns")
	now = now.Add(redisRateLimitWarnEvery - time.Second)
	limiter.redisFailed(errDown)
	assert.Equal(t, first, limiter.fallbackWarn.last, "a failure within the minute does not")
	now = now.Add(time.Second)
	limiter.redisFailed(errDown)
	assert.Equal(t, now, limiter.fallbackWarn.last)
}

func TestWarnThrottle_AllowsOncePerInterval(t *testing.T) {
	t.Parallel()
	w := warnThrottle{every: time.Minute}
	start := time.Now()

	assert.True(t, w.allow(start))
	assert.False(t, w.allow(start.Add(time.Minute-time.Nanosecond)))
	assert.True(t, w.allow(start.Add(time.Minute)))
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

	_, err := NewDeploymentRateLimiter(client, YahooRateLimitKey, YahooRateLimit{PerSecond: 0, Burst: 1, MaxWait: time.Second})
	require.ErrorIs(t, err, ErrInvalidYahooRateLimit)
	_, err = NewDeploymentRateLimiter(nil, YahooRateLimitKey, DefaultYahooRateLimit())
	require.ErrorIs(t, err, ErrInvalidYahooRateLimit)
}

func TestRateLimited_WaitsOnDeploymentLimiter(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter, err := NewDeploymentRateLimiter(client, YahooRateLimitKey,
		YahooRateLimit{PerSecond: float64(time.Second / testRequestInterval), Burst: 1, MaxWait: testMaxWait})
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

// lostReplyHook runs the next lost eval-family commands on Redis, then
// returns a timeout instead of their reply, as a reply lost on the network.
type lostReplyHook struct {
	lost atomic.Int32
}

func (*lostReplyHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (*lostReplyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *lostReplyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := next(ctx, cmd); err != nil {
			return err
		}
		if !strings.HasPrefix(cmd.Name(), "eval") || h.lost.Add(-1) < 0 {
			h.lost.Store(0)
			return nil
		}
		err := context.DeadlineExceeded
		cmd.SetErr(err)
		return err
	}
}

func TestDeploymentRateLimiter_LostReplyGivesSlotBack(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	hook := &lostReplyHook{}
	client.AddHook(hook)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst {
		reserve(t, limiter)
	}
	stored := server.HGet(YahooRateLimitKey, "tat")
	hook.lost.Store(1)

	r := reserve(t, limiter)

	assert.Equal(t, metrics.RateLimiterFallback, r.Limiter, "a lost reply is a Redis failure")
	assert.Equal(t, stored, server.HGet(YahooRateLimitKey, "tat"), "the slot the lost call took is given back")
}

func TestRateLimited_CanceledDeploymentWaitGivesRedisSlotBack(t *testing.T) {
	t.Parallel()
	_, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	for range testDeploymentBurst {
		reserve(t, limiter)
	}
	var calls atomic.Int32
	download := RateLimited(limiter, func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(cancelAfter, cancel)

	_, err := download(ctx, "https://fantasysports.yahooapis.com/")

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls.Load())
	assert.Equal(t, testDeploymentInterval, reserve(t, limiter).Delay, "the canceled wait's slot went back to Redis")
}

func TestDeploymentRateLimiter_FallbackRefusesSlotPastMaxWait(t *testing.T) {
	t.Parallel()
	server, client := newTestRedis(t)
	limiter := newTestDeploymentLimiter(t, client)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	server.Close()
	// The burst, then the slots up to testDeploymentMaxWait away.
	admitted := testDeploymentBurst + int(testDeploymentMaxWait/testDeploymentInterval)
	for range admitted {
		require.Equal(t, metrics.RateLimiterFallback, reserve(t, limiter).Limiter)
	}

	_, err := limiter.Reserve(context.Background())

	require.ErrorIs(t, err, ErrYahooRateLimitBusy, "the fallback keeps the maximum wait")
}

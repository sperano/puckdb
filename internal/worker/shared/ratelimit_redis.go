package shared

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/metrics"
	"golang.org/x/time/rate"
)

// Redis keys of the deployment-wide Yahoo token buckets. Each is a hash of
// the bucket's theoretical arrival time (GCRA) in microseconds of Redis
// server time and the emission interval of the process that wrote it last.
// The processes sharing a bucket should run with the same --yahoo-rate-limit
// and --yahoo-rate-burst; a different interval is logged (see
// DeploymentRateLimiter).
const (
	// YahooRateLimitKey is the bucket of the worker replicas' downloads.
	YahooRateLimitKey = "puckdb:ratelimit:yahoo"
	// YahooDraftRateLimitKey is the bucket of the live draft polls (the
	// api's draft watcher, sync draft), so they never queue behind a season
	// sync. Its rate adds to the workers'.
	YahooDraftRateLimitKey = YahooRateLimitKey + ":draft"
)

const (
	// redisRateLimitTimeout bounds one limiter script call, so an
	// unresponsive Redis delays a download by at most this before the
	// fallback limiter takes over.
	redisRateLimitTimeout = time.Second
	// redisRateLimitRetryAfter is how long the limiter uses its fallback
	// after a Redis failure before trying Redis again, so a Redis outage
	// does not cost every download a timeout.
	redisRateLimitRetryAfter = 15 * time.Second
	// redisRateLimitWarnEvery is the shortest gap between two warnings of
	// one kind (fallback, interval mismatch) from one limiter.
	redisRateLimitWarnEvery = time.Minute
	// microsecondsPerSecond converts the limit's rate to the script's unit.
	microsecondsPerSecond = float64(time.Second / time.Microsecond)
)

// reserveScript is GCRA with reservations, the semantics of
// rate.Limiter.Reserve: it takes the next slot and returns how long the
// caller waits for it, unless that wait exceeds the maximum, when it takes
// nothing. KEYS[1] is the bucket hash; ARGV[1] the emission interval,
// ARGV[2] the burst, ARGV[3] the maximum wait and ARGV[4] the reservation's
// ID, stored as the bucket's last reservation for releaseScript. Times are
// microseconds of the Redis server clock (TIME), so clients with skewed
// clocks agree. It returns {delay, taken, stored interval (0: none)}. The
// key expires once the bucket is full again, as an absent key means.
var reserveScript = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local maxWait = tonumber(ARGV[3])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local stored = redis.call('HMGET', KEYS[1], 'tat', 'interval')
local tat = tonumber(stored[1])
local storedInterval = tonumber(stored[2]) or 0
if tat == nil or tat < now then
  tat = now
end
local newTat = tat + interval
local delay = newTat - interval * burst - now
if delay < 0 then
  delay = 0
end
if delay > maxWait then
  return {delay, 0, storedInterval}
end
redis.call('HSET', KEYS[1], 'tat', string.format('%.0f', newTat), 'interval', interval, 'last', ARGV[4])
redis.call('PEXPIRE', KEYS[1], math.ceil((newTat - now) / 1000) + 1)
return {delay, 1, storedInterval}
`)

// releaseScript gives back the slot of reservation ARGV[3] by moving the TAT
// back one interval (ARGV[1]), as if it was never reserved, but only while
// it is the bucket's last reservation and its slot (the TAT minus burst
// ARGV[2] intervals) has not come. A reservation taken after it keeps the
// gap unused instead: moving the TAT back then would give its slot to a
// second request. It returns 1 when it released the slot, 0 otherwise.
var releaseScript = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local stored = redis.call('HMGET', KEYS[1], 'tat', 'last')
local tat = tonumber(stored[1])
if tat == nil or stored[2] ~= ARGV[3] or tat - interval * burst <= now then
  return 0
end
local newTat = tat - interval
redis.call('HSET', KEYS[1], 'tat', string.format('%.0f', newTat))
redis.call('HDEL', KEYS[1], 'last')
redis.call('PEXPIRE', KEYS[1], math.ceil((newTat - now) / 1000) + 1)
return 1
`)

// Elements of reserveScript's reply.
const (
	reserveReplyDelay = iota
	reserveReplyTaken
	reserveReplyStoredInterval
	reserveReplyLen
)

// DeploymentRateLimiter is a RateLimiter shared by every process of the
// deployment: one GCRA token bucket in Redis. While Redis fails
// (unreachable, timing out, a script error) it grants slots from a
// per-process bucket at the same rate, burst and maximum wait instead, so a
// Redis outage lets the deployment exceed its rate (the rate times its
// processes) and delays a request only when a failing call (and the release
// of the slot it may have taken) times out, up to twice
// redisRateLimitTimeout, once per redisRateLimitRetryAfter: it then tries
// Redis again. It logs the fallback at most once per redisRateLimitWarnEvery. A bucket last written with another interval (a
// process configured with another rate) is logged the same way.
type DeploymentRateLimiter struct {
	client   *redis.Client
	key      string
	interval int64 // microseconds between two requests
	burst    int
	maxWait  time.Duration
	fallback *rate.Limiter
	now      func() time.Time

	mu sync.Mutex
	// redisDownTill is when to try Redis again after a failure.
	redisDownTill  time.Time
	fallbackWarn   warnThrottle
	mismatchedWarn warnThrottle
}

// NewDeploymentRateLimiter builds the deployment-wide limiter for l on key
// (YahooRateLimitKey or YahooDraftRateLimitKey) of client (Valkey DB 1 in
// production).
func NewDeploymentRateLimiter(client *redis.Client, key string, l YahooRateLimit) (*DeploymentRateLimiter, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("%w: the deployment scope needs a Redis client", ErrInvalidYahooRateLimit)
	}
	return &DeploymentRateLimiter{
		client:         client,
		key:            key,
		interval:       max(1, int64(math.Round(microsecondsPerSecond/l.PerSecond))),
		burst:          l.Burst,
		maxWait:        l.MaxWait,
		fallback:       newTokenBucket(l),
		now:            time.Now,
		fallbackWarn:   warnThrottle{every: redisRateLimitWarnEvery},
		mismatchedWarn: warnThrottle{every: redisRateLimitWarnEvery},
	}, nil
}

// Reserve implements RateLimiter. Besides ErrYahooRateLimitBusy, it fails
// only when ctx ends during the Redis call; any Redis failure falls back to
// the per-process bucket.
func (l *DeploymentRateLimiter) Reserve(ctx context.Context) (Reservation, error) {
	if l.redisUsable() {
		id := rand.Text()
		reservation, err := l.reserveRedis(ctx, id)
		if err == nil || errors.Is(err, ErrYahooRateLimitBusy) {
			return reservation, err
		}
		// The script may have taken the slot before the reply was lost (a
		// timeout, ctx ending), so give it back by its ID.
		l.release(context.WithoutCancel(ctx), id)
		if ctx.Err() != nil {
			return Reservation{}, ctx.Err()
		}
		l.redisFailed(err)
	}
	metrics.IncYahooRateLimitFallback()
	return reserveLocal(l.fallback, l.maxWait, metrics.RateLimiterFallback)
}

// reserveRedis takes a slot from the Redis bucket as reservation id.
func (l *DeploymentRateLimiter) reserveRedis(ctx context.Context, id string) (Reservation, error) {
	callCtx, cancel := context.WithTimeout(ctx, redisRateLimitTimeout)
	defer cancel()
	reply, err := reserveScript.Run(callCtx, l.client, []string{l.key},
		l.interval, l.burst, l.maxWait.Microseconds(), id).Int64Slice()
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve Yahoo rate limit slot in Redis: %w", err)
	}
	if len(reply) != reserveReplyLen || reply[reserveReplyDelay] < 0 {
		return Reservation{}, fmt.Errorf("reserve Yahoo rate limit slot in Redis: unexpected reply %v", reply)
	}
	l.checkInterval(reply[reserveReplyStoredInterval])
	delay := time.Duration(reply[reserveReplyDelay]) * time.Microsecond
	if reply[reserveReplyTaken] == 0 {
		return Reservation{}, busyError(metrics.RateLimiterDeployment, delay, l.maxWait)
	}
	return Reservation{
		Delay:   delay,
		Limiter: metrics.RateLimiterDeployment,
		// The caller cancels when its ctx ended, so the release must not
		// inherit that cancellation, only ctx's values.
		cancel: func() { l.release(context.WithoutCancel(ctx), id) },
	}, nil
}

// release gives back the Redis slot of reservation id, best effort: a
// failure, or a reservation taken after it, only leaves the slot unused.
func (l *DeploymentRateLimiter) release(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, redisRateLimitTimeout)
	defer cancel()
	if err := releaseScript.Run(ctx, l.client, []string{l.key}, l.interval, l.burst, id).Err(); err != nil {
		log.Debug().Err(err).Str("key", l.key).Msg("Could not give a Yahoo rate limit slot back to Redis")
	}
}

// checkInterval warns, at most once per redisRateLimitWarnEvery, when the
// bucket was last written with another interval (stored; 0 when none).
func (l *DeploymentRateLimiter) checkInterval(stored int64) {
	if stored == 0 || stored == l.interval || !l.mismatchedWarn.allow(l.now()) {
		return
	}
	log.Warn().Str("key", l.key).Int64("interval_us", l.interval).Int64("stored_interval_us", stored).
		Msg("Processes share a Yahoo rate limit bucket with different rates; give them the same --yahoo-rate-limit")
}

// redisUsable reports whether the last Redis failure is old enough to try
// Redis again.
func (l *DeploymentRateLimiter) redisUsable() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.now().Before(l.redisDownTill)
}

// redisFailed switches to the fallback for redisRateLimitRetryAfter and
// warns, at most once per redisRateLimitWarnEvery.
func (l *DeploymentRateLimiter) redisFailed(err error) {
	now := l.now()
	l.mu.Lock()
	l.redisDownTill = now.Add(redisRateLimitRetryAfter)
	l.mu.Unlock()
	if !l.fallbackWarn.allow(now) {
		return
	}
	log.Warn().Err(err).Str("key", l.key).Dur("retry_redis_after", redisRateLimitRetryAfter).
		Msg("Deployment-wide Yahoo rate limiter unavailable, limiting this process on its own at the same rate")
}

// warnThrottle lets one kind of warning through at most once per every.
type warnThrottle struct {
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

// allow reports whether a warning at now may be logged, and if so records it.
func (w *warnThrottle) allow(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.last.IsZero() && now.Sub(w.last) < w.every {
		return false
	}
	w.last = now
	return true
}

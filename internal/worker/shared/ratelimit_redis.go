package shared

import (
	"context"
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

// YahooRateLimitKey is the Redis key of the deployment-wide Yahoo token
// bucket: its theoretical arrival time (GCRA), in microseconds of Redis
// server time. Every process of the deployment (worker replicas, the api,
// sync draft) shares it, so they should all run with the same
// --yahoo-rate-limit and --yahoo-rate-burst.
const YahooRateLimitKey = "puckdb:ratelimit:yahoo"

const (
	// redisRateLimitTimeout bounds one limiter script call, so an
	// unresponsive Redis delays a download by at most this before the
	// fallback limiter takes over.
	redisRateLimitTimeout = time.Second
	// redisRateLimitRetryAfter is how long the limiter uses its fallback
	// after a Redis failure before trying Redis again, so a Redis outage
	// does not cost every download a timeout.
	redisRateLimitRetryAfter = 15 * time.Second
	// redisRateLimitWarnEvery is the shortest gap between two warnings that
	// the limiter runs on its fallback.
	redisRateLimitWarnEvery = time.Minute
	// microsecondsPerSecond converts the limit's rate to the script's unit.
	microsecondsPerSecond = float64(time.Second / time.Microsecond)
)

// reserveScript is GCRA with reservations, the semantics of
// rate.Limiter.Reserve: it always takes the next slot and returns how long
// the caller waits for it. KEYS[1] holds the theoretical arrival time (TAT);
// ARGV[1] is the emission interval and ARGV[2] the burst. Times are
// microseconds of the Redis server clock (TIME), so clients with skewed
// clocks agree. It returns {delay, slot}: the wait and the slot's absolute
// time, which releaseScript needs. The key expires once the bucket is full
// again, as an absent key means.
var reserveScript = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local tat = tonumber(redis.call('GET', KEYS[1]))
if tat == nil or tat < now then
  tat = now
end
local newTat = tat + interval
local slot = newTat - interval * burst
local delay = slot - now
if delay < 0 then
  delay = 0
end
redis.call('SET', KEYS[1], string.format('%.0f', newTat), 'PX', math.ceil((newTat - now) / 1000) + 1)
return {delay, slot}
`)

// releaseScript gives back a slot whose time has not come (ARGV[2]) by
// moving the TAT back one interval (ARGV[1]), as if it was never reserved.
// Reservations made after it keep their slots; the next one takes the freed
// interval. It returns 1 when it released the slot, 0 otherwise.
var releaseScript = redis.NewScript(`
local interval = tonumber(ARGV[1])
local slot = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
if slot <= now then
  return 0
end
local tat = tonumber(redis.call('GET', KEYS[1]))
if tat == nil or tat - interval < now then
  return 0
end
local newTat = tat - interval
redis.call('SET', KEYS[1], string.format('%.0f', newTat), 'PX', math.ceil((newTat - now) / 1000) + 1)
return 1
`)

// DeploymentRateLimiter is a RateLimiter shared by every process of the
// deployment: one GCRA token bucket in Redis (YahooRateLimitKey). While
// Redis fails (unreachable, timing out, a script error) it grants slots
// from a per-process bucket at the same rate and burst instead, so a Redis
// outage slows nothing down but lets the deployment exceed its rate (the
// rate times its processes); it logs that at most once per
// redisRateLimitWarnEvery and tries Redis again after
// redisRateLimitRetryAfter.
type DeploymentRateLimiter struct {
	client   redis.Scripter
	key      string
	interval int64 // microseconds between two requests
	burst    int
	fallback *rate.Limiter
	now      func() time.Time

	mu            sync.Mutex
	redisDownTill time.Time
	lastWarning   time.Time
}

// NewDeploymentRateLimiter builds the deployment-wide limiter for l over
// client (Valkey DB 1 in production).
func NewDeploymentRateLimiter(client redis.Scripter, l YahooRateLimit) (*DeploymentRateLimiter, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("%w: the deployment scope needs a Redis client", ErrInvalidYahooRateLimit)
	}
	return &DeploymentRateLimiter{
		client:   client,
		key:      YahooRateLimitKey,
		interval: max(1, int64(math.Round(microsecondsPerSecond/l.PerSecond))),
		burst:    l.Burst,
		fallback: newTokenBucket(l),
		now:      time.Now,
	}, nil
}

// Reserve implements RateLimiter. It fails only when ctx ends during the
// Redis call; any Redis failure falls back to the per-process bucket.
func (l *DeploymentRateLimiter) Reserve(ctx context.Context) (Reservation, error) {
	if l.redisUsable() {
		reservation, err := l.reserveRedis(ctx)
		if err == nil {
			return reservation, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Reservation{}, ctxErr
		}
		l.redisFailed(err)
	}
	metrics.IncYahooRateLimitFallback()
	reservation := l.fallback.Reserve()
	if !reservation.OK() {
		return Reservation{}, errors.New("rate limiter burst is zero")
	}
	return Reservation{Delay: reservation.Delay(), Limiter: metrics.RateLimiterFallback, cancel: reservation.Cancel}, nil
}

// reserveRedis takes a slot from the Redis bucket.
func (l *DeploymentRateLimiter) reserveRedis(ctx context.Context) (Reservation, error) {
	callCtx, cancel := context.WithTimeout(ctx, redisRateLimitTimeout)
	defer cancel()
	values, err := reserveScript.Run(callCtx, l.client, []string{l.key}, l.interval, l.burst).Int64Slice()
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve Yahoo rate limit slot in Redis: %w", err)
	}
	if len(values) != 2 || values[0] < 0 {
		return Reservation{}, fmt.Errorf("reserve Yahoo rate limit slot in Redis: unexpected reply %v", values)
	}
	delay, slot := values[0], values[1]
	return Reservation{
		Delay:   time.Duration(delay) * time.Microsecond,
		Limiter: metrics.RateLimiterDeployment,
		cancel:  func() { l.release(slot) },
	}, nil
}

// release gives back the Redis slot at slot, best effort: a failure only
// leaves the slot unused.
func (l *DeploymentRateLimiter) release(slot int64) {
	ctx, cancel := context.WithTimeout(context.Background(), redisRateLimitTimeout)
	defer cancel()
	if err := releaseScript.Run(ctx, l.client, []string{l.key}, l.interval, slot).Err(); err != nil {
		log.Debug().Err(err).Str("key", l.key).Msg("Could not give a Yahoo rate limit slot back to Redis")
	}
}

// redisUsable reports whether the last Redis failure is old enough to try
// Redis again.
func (l *DeploymentRateLimiter) redisUsable() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.now().Before(l.redisDownTill)
}

// redisFailed switches to the fallback for redisRateLimitRetryAfter and
// warns, at most once per redisRateLimitWarnEvery; it reports whether it
// warned.
func (l *DeploymentRateLimiter) redisFailed(err error) bool {
	l.mu.Lock()
	now := l.now()
	l.redisDownTill = now.Add(redisRateLimitRetryAfter)
	warn := l.lastWarning.IsZero() || now.Sub(l.lastWarning) >= redisRateLimitWarnEvery
	if warn {
		l.lastWarning = now
	}
	l.mu.Unlock()
	if warn {
		log.Warn().Err(err).Str("key", l.key).Dur("retry_redis_after", redisRateLimitRetryAfter).
			Msg("Deployment-wide Yahoo rate limiter unavailable, limiting this process on its own at the same rate")
	}
	return warn
}

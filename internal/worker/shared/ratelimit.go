package shared

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"go.temporal.io/sdk/activity"
	"golang.org/x/time/rate"
)

const (
	// maxRateLimitHeartbeatInterval is the longest gap between the heartbeats
	// of an activity waiting for a Yahoo request token or a throttle retry. A
	// busy deployment can queue a download for longer than the activity heartbeat timeout
	// (--activity-heartbeat-timeout), and the wait is progress, not a stall.
	maxRateLimitHeartbeatInterval = 10 * time.Second
	// heartbeatsPerTimeout is how many heartbeats a waiting activity sends
	// per heartbeat timeout, so a shorter timeout gets a shorter interval.
	heartbeatsPerTimeout = 3
	// minRateLimitHeartbeatInterval keeps the interval positive (a ticker
	// panics on zero) and bounds the heartbeat rate under a tiny timeout.
	minRateLimitHeartbeatInterval = 100 * time.Millisecond
)

// YahooRateLimit is the token bucket every Yahoo request shares: those of the
// whole deployment with DeploymentRateLimiter (the default scope), those of
// one process with LocalRateLimiter.
type YahooRateLimit struct {
	// PerSecond is the sustained number of requests per second.
	PerSecond float64
	// Burst is how many requests may start back to back after an idle period.
	Burst int
	// MaxWait is the longest a request may queue for its slot: a request
	// whose slot is further away gives it back and fails with
	// ErrYahooRateLimitBusy instead of waiting.
	MaxWait time.Duration
}

// DefaultYahooRateLimit returns the settings the --yahoo-rate-* flags default to.
func DefaultYahooRateLimit() YahooRateLimit {
	return YahooRateLimit{
		PerSecond: config.DefaultYahooRateLimit,
		Burst:     config.DefaultYahooRateBurst,
		MaxWait:   config.DefaultYahooRateMaxWait * time.Second,
	}
}

var (
	// ErrInvalidYahooRateLimit reports a limit that would block or
	// unthrottle every Yahoo download.
	ErrInvalidYahooRateLimit = errors.New("invalid Yahoo rate limit")
	// ErrZeroBurst reports a token bucket that can never grant a request.
	ErrZeroBurst = errors.New("rate limiter burst is zero")
	// ErrYahooRateLimitBusy reports a request whose slot is more than
	// YahooRateLimit.MaxWait away: so many requests already queue that this
	// one would wait too long. It is retryable: the activity fails at once,
	// and Temporal's retry backoff keeps it out of the queue meanwhile, so the
	// queue never outgrows what the activities can wait for.
	ErrYahooRateLimitBusy = errors.New("too many Yahoo requests queued")
)

// Validate rejects a rate that is not a positive finite number, a burst
// below one and a maximum wait that is not positive.
func (l YahooRateLimit) Validate() error {
	if l.PerSecond <= 0 || math.IsNaN(l.PerSecond) || math.IsInf(l.PerSecond, 1) {
		return fmt.Errorf("%w: --%s must be a positive number of requests per second, got %v",
			ErrInvalidYahooRateLimit, config.FlagYahooRateLimit, l.PerSecond)
	}
	if l.Burst < 1 {
		return fmt.Errorf("%w: --%s must be at least 1, got %d",
			ErrInvalidYahooRateLimit, config.FlagYahooRateBurst, l.Burst)
	}
	if l.MaxWait <= 0 {
		return fmt.Errorf("%w: --%s must be a positive number of seconds, got %s",
			ErrInvalidYahooRateLimit, config.FlagYahooRateMaxWait, l.MaxWait)
	}
	return nil
}

// RateLimiter grants Yahoo request slots. Implementations are safe for
// concurrent use.
type RateLimiter interface {
	// Reserve takes the next request slot and says how long the caller must
	// wait before using it, or fails with ErrYahooRateLimitBusy, taking
	// nothing, when that wait would exceed the limiter's maximum. A caller
	// that gives up waiting cancels the reservation, so the slot may go to
	// another request.
	Reserve(ctx context.Context) (Reservation, error)
}

// Reservation is one request slot taken from a RateLimiter.
type Reservation struct {
	// Delay is how long to wait before sending the request.
	Delay time.Duration
	// Limiter names the limiter that granted the slot (a metrics.RateLimiter*
	// label).
	Limiter string
	cancel  func()
}

// Cancel gives the slot back, as far as the limiter can (a slot whose time
// has come stays used). It may be called on a zero Reservation.
func (r Reservation) Cancel() {
	if r.cancel != nil {
		r.cancel()
	}
}

// LocalRateLimiter is a RateLimiter over one process's token bucket.
type LocalRateLimiter struct {
	limiter *rate.Limiter
	maxWait time.Duration
}

// NewLocalRateLimiter wraps limiter, refusing a slot more than maxWait away
// (a maxWait of zero or less refuses none).
func NewLocalRateLimiter(limiter *rate.Limiter, maxWait time.Duration) *LocalRateLimiter {
	return &LocalRateLimiter{limiter: limiter, maxWait: maxWait}
}

// Reserve implements RateLimiter.
func (l *LocalRateLimiter) Reserve(context.Context) (Reservation, error) {
	return reserveLocal(l.limiter, l.maxWait, metrics.RateLimiterProcess)
}

// reserveLocal takes a slot from limiter for the limiter labeled label,
// giving it back when it is more than maxWait (if positive) away.
func reserveLocal(limiter *rate.Limiter, maxWait time.Duration, label string) (Reservation, error) {
	reservation := limiter.Reserve()
	if !reservation.OK() {
		return Reservation{}, ErrZeroBurst
	}
	delay := reservation.Delay()
	if maxWait > 0 && delay > maxWait {
		reservation.Cancel()
		return Reservation{}, busyError(label, delay, maxWait)
	}
	return Reservation{Delay: delay, Limiter: label, cancel: reservation.Cancel}, nil
}

// busyError counts and reports a slot delay away, over maxWait, refused by
// the limiter labeled label.
func busyError(label string, delay, maxWait time.Duration) error {
	metrics.IncYahooRateLimitBusy(label)
	return fmt.Errorf("%w: next slot in %s, over the %s maximum (--%s)",
		ErrYahooRateLimitBusy, delay.Round(time.Millisecond), maxWait, config.FlagYahooRateMaxWait)
}

// NewYahooRateLimiter builds the per-process limiter for l
// (--yahoo-rate-limit-scope=process). Build one per process and pass it to
// every Yahoo downloader of that process.
func NewYahooRateLimiter(l YahooRateLimit) (*LocalRateLimiter, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return NewLocalRateLimiter(newTokenBucket(l), l.MaxWait), nil
}

// newTokenBucket is the in-process token bucket of a valid l.
func newTokenBucket(l YahooRateLimit) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(l.PerSecond), l.Burst)
}

// RateLimited returns a Downloader that takes a token from limiter before
// each call to download. Waiting honors ctx: a canceled or expired context
// returns its error without downloading and gives the token back.
func RateLimited(limiter RateLimiter, download Downloader) Downloader {
	return func(ctx context.Context, url string) ([]byte, error) {
		if err := waitForToken(ctx, limiter, maxRateLimitHeartbeatInterval); err != nil {
			return nil, fmt.Errorf("wait for Yahoo rate limit: %w", err)
		}
		return download(ctx, url)
	}
}

// waitForToken blocks until limiter grants a request or ctx ends, then
// records the wait (metrics.ObserveYahooRateLimitWait). It heartbeats while
// it waits, as sleepHeartbeating does.
func waitForToken(ctx context.Context, limiter RateLimiter, maxHeartbeatEvery time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := time.Now()
	reservation, err := limiter.Reserve(ctx)
	if err != nil {
		return err
	}
	if err := sleepHeartbeating(ctx, reservation.Delay, maxHeartbeatEvery); err != nil {
		reservation.Cancel()
		return err
	}
	metrics.ObserveYahooRateLimitWait(reservation.Limiter, time.Since(start))
	return nil
}

// sleepHeartbeating waits for d or until ctx ends, returning ctx's error in
// the latter case. Inside a Temporal activity it heartbeats while it waits
// (see heartbeatInterval), repeating the activity's latest details, which
// also lets the activity see a cancellation delivered through heartbeats.
func sleepHeartbeating(ctx context.Context, d, maxHeartbeatEvery time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	var heartbeat <-chan time.Time
	if activity.IsActivity(ctx) {
		ticker := time.NewTicker(heartbeatInterval(activity.GetInfo(ctx).HeartbeatTimeout, maxHeartbeatEvery))
		defer ticker.Stop()
		heartbeat = ticker.C
	}
	for {
		select {
		case <-timer.C:
			return nil
		case <-heartbeat:
			RepeatHeartbeat(ctx)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// heartbeatInterval returns how often to heartbeat under timeout, the
// activity's heartbeat timeout (zero when it has none): a fraction of the
// timeout, at most maxEvery and at least minRateLimitHeartbeatInterval
// (unless maxEvery is shorter).
func heartbeatInterval(timeout, maxEvery time.Duration) time.Duration {
	if timeout <= 0 {
		return maxEvery
	}
	return min(maxEvery, max(minRateLimitHeartbeatInterval, timeout/heartbeatsPerTimeout))
}

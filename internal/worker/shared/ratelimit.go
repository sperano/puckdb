package shared

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"go.temporal.io/sdk/activity"
	"golang.org/x/time/rate"
)

const (
	// maxRateLimitHeartbeatInterval is the longest gap between the heartbeats
	// of an activity waiting for a Yahoo request token. A busy process can
	// queue a download for longer than the activity heartbeat timeout
	// (--activity-heartbeat-timeout), and the wait is progress, not a stall.
	maxRateLimitHeartbeatInterval = 10 * time.Second
	// heartbeatsPerTimeout is how many heartbeats a waiting activity sends
	// per heartbeat timeout, so a shorter timeout gets a shorter interval.
	heartbeatsPerTimeout = 3
)

// YahooRateLimit is the token bucket every Yahoo request of one process
// shares. The rate a whole deployment sends to Yahoo is PerSecond times its
// number of processes (worker replicas).
type YahooRateLimit struct {
	// PerSecond is the sustained number of requests per second.
	PerSecond float64
	// Burst is how many requests may start back to back after an idle period.
	Burst int
}

// DefaultYahooRateLimit returns the settings the --yahoo-rate-* flags default to.
func DefaultYahooRateLimit() YahooRateLimit {
	return YahooRateLimit{PerSecond: config.DefaultYahooRateLimit, Burst: config.DefaultYahooRateBurst}
}

// ErrInvalidYahooRateLimit reports a limit that would block or unthrottle
// every Yahoo download.
var ErrInvalidYahooRateLimit = errors.New("invalid Yahoo rate limit")

// Validate rejects a rate that is not a positive finite number and a burst
// below one.
func (l YahooRateLimit) Validate() error {
	if l.PerSecond <= 0 || math.IsNaN(l.PerSecond) || math.IsInf(l.PerSecond, 1) {
		return fmt.Errorf("%w: --%s must be a positive number of requests per second, got %v",
			ErrInvalidYahooRateLimit, config.FlagYahooRateLimit, l.PerSecond)
	}
	if l.Burst < 1 {
		return fmt.Errorf("%w: --%s must be at least 1, got %d",
			ErrInvalidYahooRateLimit, config.FlagYahooRateBurst, l.Burst)
	}
	return nil
}

// NewYahooRateLimiter builds the limiter for l. Build one per process and
// pass it to every Yahoo downloader of that process.
func NewYahooRateLimiter(l YahooRateLimit) (*rate.Limiter, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return rate.NewLimiter(rate.Limit(l.PerSecond), l.Burst), nil
}

// RateLimited returns a Downloader that takes a token from limiter before
// each call to download. Waiting honors ctx: a canceled or expired context
// returns its error without downloading and gives the token back.
func RateLimited(limiter *rate.Limiter, download Downloader) Downloader {
	return func(ctx context.Context, url string) ([]byte, error) {
		if err := waitForToken(ctx, limiter, maxRateLimitHeartbeatInterval); err != nil {
			return nil, fmt.Errorf("wait for Yahoo rate limit: %w", err)
		}
		return download(ctx, url)
	}
}

// waitForToken blocks until limiter grants a request or ctx ends. Inside a
// Temporal activity it heartbeats while it waits (see heartbeatInterval),
// repeating the activity's latest details, which also lets the activity see
// a cancellation delivered through heartbeats.
func waitForToken(ctx context.Context, limiter *rate.Limiter, maxHeartbeatEvery time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reservation := limiter.Reserve()
	if !reservation.OK() {
		return errors.New("rate limiter burst is zero")
	}
	delay := reservation.Delay()
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
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
			reservation.Cancel()
			return ctx.Err()
		}
	}
}

// heartbeatInterval returns how often to heartbeat under timeout, the
// activity's heartbeat timeout (zero when it has none): a fraction of the
// timeout, at most maxEvery.
func heartbeatInterval(timeout, maxEvery time.Duration) time.Duration {
	if timeout <= 0 {
		return maxEvery
	}
	return min(maxEvery, timeout/heartbeatsPerTimeout)
}

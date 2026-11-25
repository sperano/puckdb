package shared

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/metrics"
)

// Throttle retry defaults (DefaultThrottleRetry): five retries waiting about
// 2, 4, 8, 16 and 30 s, so a download gives up after at most a minute of
// backoff (plus its rate limiter waits) and its activity retries instead.
const (
	defaultThrottleMaxAttempts    = 6
	defaultThrottleInitialBackoff = 2 * time.Second
	defaultThrottleMaxBackoff     = 30 * time.Second
	// throttleBackoffGrowth is the factor between two consecutive backoffs.
	throttleBackoffGrowth = 2
)

// ThrottleRetry bounds the retries of one throttled download (HTTP 999 or
// 429, httpx.HTTPError.IsThrottled).
type ThrottleRetry struct {
	// MaxAttempts is how many times a download is tried in all, the first
	// included; values below 2 never retry.
	MaxAttempts int
	// InitialBackoff is the backoff before the first retry; each next one
	// doubles, up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// heartbeatEvery overrides maxRateLimitHeartbeatInterval (tests only).
	heartbeatEvery time.Duration
}

// DefaultThrottleRetry is the retry policy of the worker's Yahoo downloads.
func DefaultThrottleRetry() ThrottleRetry {
	return ThrottleRetry{
		MaxAttempts:    defaultThrottleMaxAttempts,
		InitialBackoff: defaultThrottleInitialBackoff,
		MaxBackoff:     defaultThrottleMaxBackoff,
	}
}

// RetryThrottled returns a Downloader that retries download when it answers
// a throttle (httpx.HTTPError.IsThrottled), after a jittered exponential
// backoff, up to policy.MaxAttempts attempts; then it returns the last
// error, so the activity fails and Temporal retries it. Other errors and
// successes are returned at once. Wrap a rate-limited download
// (RateLimited) so every retry takes a token too. The backoff heartbeats
// inside an activity and ends early, with ctx's error, when ctx ends.
func RetryThrottled(download Downloader, policy ThrottleRetry) Downloader {
	return func(ctx context.Context, url string) ([]byte, error) {
		for attempt := 1; ; attempt++ {
			body, err := download(ctx, url)
			status, throttled := throttleStatus(err)
			if !throttled {
				return body, err
			}
			retry := attempt < policy.MaxAttempts
			backoff := policy.backoff(attempt)
			logThrottled(url, status, attempt, retry, backoff)
			if !retry {
				return body, err
			}
			metrics.IncYahooThrottleRetry(status)
			if waitErr := sleepHeartbeating(ctx, backoff, policy.maxHeartbeatEvery()); waitErr != nil {
				return nil, fmt.Errorf("wait to retry throttled download (%w): %w", err, waitErr)
			}
		}
	}
}

// throttleStatus returns the status code of err when it is a throttle.
func throttleStatus(err error) (int, bool) {
	httpErr, ok := errors.AsType[*httpx.HTTPError](err)
	if !ok || !httpErr.IsThrottled() {
		return 0, false
	}
	return httpErr.StatusCode, true
}

// backoff is the wait before retry number attempt (1 = first retry):
// InitialBackoff doubled attempt-1 times, capped at MaxBackoff, with equal
// jitter (half fixed, half random) so throttled downloads spread out.
func (p ThrottleRetry) backoff(attempt int) time.Duration {
	d := p.InitialBackoff
	for i := 1; i < attempt && d < p.MaxBackoff; i++ {
		d *= throttleBackoffGrowth
	}
	d = min(d, p.MaxBackoff)
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + rand.N(half+1)
}

// maxHeartbeatEvery is the longest gap between heartbeats during a backoff.
func (p ThrottleRetry) maxHeartbeatEvery() time.Duration {
	if p.heartbeatEvery > 0 {
		return p.heartbeatEvery
	}
	return maxRateLimitHeartbeatInterval
}

// logThrottled warns about one throttled attempt.
func logThrottled(url string, status, attempt int, retry bool, backoff time.Duration) {
	event := log.Warn().Str("url", url).Int("status_code", status).Int("attempt", attempt)
	if !retry {
		event.Msg("Yahoo throttled a download, giving up after the last attempt")
		return
	}
	event.Dur("backoff", backoff).Msg("Yahoo throttled a download, retrying")
}

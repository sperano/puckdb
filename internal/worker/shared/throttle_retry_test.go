package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

const (
	testThrottleURL     = "https://fantasysports.yahooapis.com/fantasy/v2/team/1"
	testThrottleBackoff = time.Millisecond
	testThrottleMax     = 4 * time.Millisecond
	testThrottleTries   = 4
)

// testThrottlePolicy retries quickly so the tests run in milliseconds.
func testThrottlePolicy() ThrottleRetry {
	return ThrottleRetry{MaxAttempts: testThrottleTries, InitialBackoff: testThrottleBackoff, MaxBackoff: testThrottleMax}
}

func throttled(status int) error {
	return fmt.Errorf("wrapped: %w", &httpx.HTTPError{StatusCode: status, Status: http.StatusText(status), URL: testThrottleURL})
}

// scriptedDownloader answers errs in order, then a body, counting its calls.
func scriptedDownloader(calls *atomic.Int32, errs ...error) Downloader {
	return func(context.Context, string) ([]byte, error) {
		n := int(calls.Add(1))
		if n <= len(errs) {
			return nil, errs[n-1]
		}
		return []byte("ok"), nil
	}
}

func TestRetryThrottled_RetriesThrottlesUntilSuccess(t *testing.T) {
	t.Parallel()
	for name, status := range map[string]int{"Yahoo 999": httpx.StatusYahooThrottled, "429": http.StatusTooManyRequests} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			download := RetryThrottled(scriptedDownloader(&calls, throttled(status), throttled(status)), testThrottlePolicy())

			body, err := download(context.Background(), testThrottleURL)

			require.NoError(t, err)
			assert.Equal(t, []byte("ok"), body)
			assert.Equal(t, int32(3), calls.Load(), "two throttles, then the success")
		})
	}
}

func TestRetryThrottled_GivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	errs := make([]error, testThrottleTries+1)
	for i := range errs {
		errs[i] = throttled(httpx.StatusYahooThrottled)
	}
	download := RetryThrottled(scriptedDownloader(&calls, errs...), testThrottlePolicy())

	_, err := download(context.Background(), testThrottleURL)

	httpErr, ok := errors.AsType[*httpx.HTTPError](err)
	require.True(t, ok, "the last throttle is returned so the activity fails and retries")
	assert.Equal(t, httpx.StatusYahooThrottled, httpErr.StatusCode)
	assert.Equal(t, int32(testThrottleTries), calls.Load())
}

func TestRetryThrottled_DoesNotRetryOtherErrors(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"400":       &httpx.HTTPError{StatusCode: http.StatusBadRequest},
		"404":       &httpx.HTTPError{StatusCode: http.StatusNotFound},
		"503":       &httpx.HTTPError{StatusCode: http.StatusServiceUnavailable},
		"transport": errors.New("connection reset"),
		"canceled":  context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			_, err := RetryThrottled(scriptedDownloader(&calls, failure), testThrottlePolicy())(context.Background(), testThrottleURL)

			require.ErrorIs(t, err, failure)
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestRetryThrottled_CancelDuringBackoffStopsRetrying(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	policy := ThrottleRetry{MaxAttempts: testThrottleTries, InitialBackoff: time.Hour, MaxBackoff: time.Hour}
	download := RetryThrottled(scriptedDownloader(&calls, throttled(httpx.StatusYahooThrottled)), policy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(cancelAfter, cancel)

	begin := time.Now()
	_, err := download(ctx, testThrottleURL)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(begin), promptReturn)
	assert.Equal(t, int32(1), calls.Load(), "no retry after cancellation")
}

func TestRetryThrottled_RetriesTakeRateLimiterTokens(t *testing.T) {
	t.Parallel()
	limiter, err := NewYahooRateLimiter(YahooRateLimit{PerSecond: float64(time.Second / testRequestInterval), Burst: 1})
	require.NoError(t, err)
	var calls atomic.Int32
	download := RetryThrottled(
		RateLimited(limiter, scriptedDownloader(&calls, throttled(http.StatusTooManyRequests), throttled(http.StatusTooManyRequests))),
		testThrottlePolicy())

	begin := time.Now()
	_, err = download(context.Background(), testThrottleURL)

	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
	assert.GreaterOrEqual(t, time.Since(begin), 2*testRequestInterval-timerSlack,
		"each retry waits for its own token, however short the backoff")
}

func TestRetryThrottled_HeartbeatsDuringBackoff(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	var heartbeats atomic.Int32
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { heartbeats.Add(1) })
	var calls atomic.Int32
	policy := ThrottleRetry{MaxAttempts: 2, InitialBackoff: testHeartbeatWait, MaxBackoff: testHeartbeatWait}
	policy.heartbeatEvery = testHeartbeatInterval
	env.RegisterActivityWithOptions(func(ctx context.Context) error {
		_, err := RetryThrottled(scriptedDownloader(&calls, throttled(httpx.StatusYahooThrottled)), policy)(ctx, testThrottleURL)
		return err
	}, activity.RegisterOptions{Name: "download"})

	_, err := env.ExecuteActivity("download")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, heartbeats.Load(), int32(minHeartbeats))
}

func TestThrottleRetry_BackoffIsBoundedExponentialWithJitter(t *testing.T) {
	t.Parallel()
	policy := DefaultThrottleRetry()
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, full := range want {
		attempt := i + 1
		for range 20 {
			got := policy.backoff(attempt)
			assert.GreaterOrEqual(t, got, full/2, "attempt %d", attempt)
			assert.LessOrEqual(t, got, full, "attempt %d", attempt)
		}
	}
}

func TestDefaultThrottleRetry_BoundsTotalBackoff(t *testing.T) {
	t.Parallel()
	policy := DefaultThrottleRetry()
	var total time.Duration
	for attempt := 1; attempt < policy.MaxAttempts; attempt++ {
		total += policy.backoff(attempt)
	}
	assert.LessOrEqual(t, total, time.Minute)
}

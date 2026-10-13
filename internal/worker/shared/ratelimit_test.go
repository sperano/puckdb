package shared

import (
	"context"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"golang.org/x/time/rate"
)

const (
	// testRequestInterval is the spacing the spacing test configures (20 req/s).
	testRequestInterval = 50 * time.Millisecond
	// testConcurrentDownloads is how many downloads start at once.
	testConcurrentDownloads = 8
	// timerSlack absorbs float rounding in the limiter's token arithmetic.
	timerSlack = time.Millisecond
	// schedulingSlack bounds how late a loaded test machine may run the last download.
	schedulingSlack = 2 * time.Second
	// promptReturn bounds how long a canceled wait may take to return.
	promptReturn = 2 * time.Second
	// cancelAfter is when the cancellation tests end a pending wait.
	cancelAfter = 50 * time.Millisecond
	// testHeartbeatInterval and testHeartbeatWait drive the activity heartbeat test.
	testHeartbeatInterval = 10 * time.Millisecond
	testHeartbeatWait     = 200 * time.Millisecond
	// minHeartbeats is what reaches the listener: the SDK sends the first
	// heartbeat at once and batches the later ones beyond testHeartbeatWait.
	minHeartbeats = 1
)

// unlimited returns a limiter that never makes a test wait.
func unlimited() *rate.Limiter {
	return rate.NewLimiter(rate.Inf, 1)
}

// drained returns a limiter whose next token is interval away.
func drained(t *testing.T, interval time.Duration) *rate.Limiter {
	t.Helper()
	limiter := rate.NewLimiter(rate.Every(interval), 1)
	require.True(t, limiter.Allow(), "a new limiter starts with a full bucket")
	return limiter
}

// countingDownloader records how often it runs and returns an empty body.
func countingDownloader(calls *atomic.Int32) Downloader {
	return func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return nil, nil
	}
}

func TestRateLimited_SpacesConcurrentDownloadsAtRate(t *testing.T) {
	t.Parallel()
	start := time.Now()
	limiter, err := NewYahooRateLimiter(YahooRateLimit{PerSecond: float64(time.Second / testRequestInterval), Burst: 1})
	require.NoError(t, err)

	var mu sync.Mutex
	var started []time.Time
	download := RateLimited(limiter, func(context.Context, string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		started = append(started, time.Now())
		return nil, nil
	})

	var wg sync.WaitGroup
	for range testConcurrentDownloads {
		wg.Go(func() {
			_, err := download(context.Background(), "https://fantasysports.yahooapis.com/")
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	require.Len(t, started, testConcurrentDownloads)
	slices.SortFunc(started, func(a, b time.Time) int { return a.Compare(b) })
	// With a burst of 1, the k-th download cannot start before k intervals
	// have passed, however the goroutines are scheduled.
	for k, at := range started {
		earliest := start.Add(time.Duration(k)*testRequestInterval - timerSlack)
		assert.False(t, at.Before(earliest), "download %d started %s after start, want at least %s",
			k, at.Sub(start), time.Duration(k)*testRequestInterval)
	}
	last := time.Duration(testConcurrentDownloads-1) * testRequestInterval
	assert.Less(t, started[len(started)-1].Sub(start), last+schedulingSlack,
		"downloads must flow at the configured rate, not slower")
}

func TestRateLimited_CanceledWaitReturnsPromptlyWithoutDownloading(t *testing.T) {
	t.Parallel()
	limiter := drained(t, time.Hour)
	var calls atomic.Int32
	download := RateLimited(limiter, countingDownloader(&calls))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(cancelAfter, cancel)
	begin := time.Now()
	_, err := download(ctx, "https://fantasysports.yahooapis.com/")

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(begin), promptReturn)
	assert.Zero(t, calls.Load(), "a canceled wait must not download")
	assert.InDelta(t, 0, limiter.Tokens(), 0.01, "a canceled wait gives its token back")
}

func TestRateLimited_ExpiredDeadlineDoesNotDownload(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	download := RateLimited(drained(t, time.Hour), countingDownloader(&calls))

	ctx, cancel := context.WithTimeout(context.Background(), cancelAfter)
	defer cancel()
	_, err := download(ctx, "https://fantasysports.yahooapis.com/")

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, calls.Load())
}

func TestRateLimited_AlreadyCanceledContextTakesNoToken(t *testing.T) {
	t.Parallel()
	limiter := rate.NewLimiter(rate.Every(time.Hour), 1)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RateLimited(limiter, countingDownloader(&calls))(ctx, "https://fantasysports.yahooapis.com/")

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls.Load())
	assert.True(t, limiter.Allow(), "the bucket must still hold its token")
}

func TestWaitForToken_HeartbeatsWhileWaitingInActivity(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	var heartbeats atomic.Int32
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { heartbeats.Add(1) })
	limiter := drained(t, testHeartbeatWait)
	env.RegisterActivityWithOptions(func(ctx context.Context) error {
		return waitForToken(ctx, limiter, testHeartbeatInterval)
	}, activity.RegisterOptions{Name: "wait"})

	_, err := env.ExecuteActivity("wait")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, heartbeats.Load(), int32(minHeartbeats))
}

func TestYahooRateLimit_Validate(t *testing.T) {
	t.Parallel()
	require.NoError(t, DefaultYahooRateLimit().Validate())
	for name, limit := range map[string]YahooRateLimit{
		"zero rate":     {PerSecond: 0, Burst: 1},
		"negative rate": {PerSecond: -1, Burst: 1},
		"NaN rate":      {PerSecond: math.NaN(), Burst: 1},
		"infinite rate": {PerSecond: math.Inf(1), Burst: 1},
		"zero burst":    {PerSecond: 1, Burst: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewYahooRateLimiter(limit)
			require.ErrorIs(t, err, ErrInvalidYahooRateLimit)
		})
	}
}

func TestHeartbeatInterval(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		timeout, want time.Duration
	}{
		"no heartbeat timeout":      {timeout: 0, want: maxRateLimitHeartbeatInterval},
		"default heartbeat timeout": {timeout: time.Minute, want: maxRateLimitHeartbeatInterval},
		"short heartbeat timeout":   {timeout: 6 * time.Second, want: 2 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, heartbeatInterval(tc.timeout, maxRateLimitHeartbeatInterval))
		})
	}
}

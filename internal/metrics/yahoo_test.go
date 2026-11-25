package metrics

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yahooThrottleTestStatus is a status no production code reports, so the
// parallel tests' counts cannot collide with it.
const yahooThrottleTestStatus = 598

func TestYahooMetricsRegistered(t *testing.T) {
	t.Parallel()
	ObserveYahooRateLimitWait(RateLimiterDeployment, time.Second)
	IncYahooThrottleRetry(http.StatusTooManyRequests)
	IncYahooRateLimitFallback()

	families, err := WorkerRegistry.Gather()
	require.NoError(t, err)

	want := map[string]bool{
		"puckdb_yahoo_rate_limit_wait_seconds":    false,
		"puckdb_yahoo_throttle_retries_total":     false,
		"puckdb_yahoo_rate_limit_fallbacks_total": false,
	}
	for _, f := range families {
		if _, ok := want[f.GetName()]; ok {
			want[f.GetName()] = true
		}
	}
	for name, found := range want {
		assert.Truef(t, found, "metric %s must be registered on WorkerRegistry", name)
	}
}

func TestIncYahooThrottleRetry_LabelsByStatusCode(t *testing.T) {
	t.Parallel()
	counter := yahooThrottleRetries.WithLabelValues(strconv.Itoa(yahooThrottleTestStatus))
	before := testutil.ToFloat64(counter)

	IncYahooThrottleRetry(yahooThrottleTestStatus)

	assert.Equal(t, before+1, testutil.ToFloat64(counter))
}

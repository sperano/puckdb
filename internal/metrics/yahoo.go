package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Yahoo request limiter and throttle metrics, registered on WorkerRegistry
// in metrics.go. Requests by outcome are not counted here: the HTTP client
// already records every Yahoo response with its status code in
// puckdb_http_request_duration_seconds (api "yahoo" for the OAuth API,
// "public" for the player pages; status_code 999 or 429 is a throttle).
var (
	yahooRateLimitWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_yahoo_rate_limit_wait_seconds",
		Help:    "Time a Yahoo request waited for a rate limiter token, by the limiter that granted it (deployment|process|fallback)",
		Buckets: rateLimitWaitBuckets,
	}, []string{"limiter"})

	yahooThrottleRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_yahoo_throttle_retries_total",
		Help: "Yahoo downloads retried inside their activity after a throttle response, by status code (999|429)",
	}, []string{"status_code"})

	yahooRateLimitBusy = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_yahoo_rate_limit_busy_total",
		Help: "Yahoo requests refused because their rate limiter slot was more than --yahoo-rate-max-wait away, by limiter (deployment|process|fallback)",
	}, []string{"limiter"})

	yahooRateLimitFallbacks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "puckdb_yahoo_rate_limit_fallbacks_total",
		Help: "Yahoo rate limiter tokens taken from the per-process fallback limiter because the deployment-wide (Redis) limiter was unavailable",
	})
)

// Yahoo rate limiter labels of puckdb_yahoo_rate_limit_wait_seconds.
const (
	// RateLimiterDeployment is a token from the deployment-wide Redis limiter.
	RateLimiterDeployment = "deployment"
	// RateLimiterProcess is a token from a per-process limiter, by configuration.
	RateLimiterProcess = "process"
	// RateLimiterFallback is a token from the per-process limiter a
	// deployment-wide limiter falls back to while Redis is unavailable.
	RateLimiterFallback = "fallback"
)

// ObserveYahooRateLimitWait records how long a Yahoo request waited for its
// token; limiter is one of the RateLimiter* labels.
func ObserveYahooRateLimitWait(limiter string, wait time.Duration) {
	yahooRateLimitWait.WithLabelValues(limiter).Observe(wait.Seconds())
}

// IncYahooThrottleRetry counts one retry of a Yahoo download that answered
// statusCode, a throttle status.
func IncYahooThrottleRetry(statusCode int) {
	yahooThrottleRetries.WithLabelValues(strconv.Itoa(statusCode)).Inc()
}

// IncYahooRateLimitBusy counts one request refused by the limiter labeled
// limiter (a RateLimiter* label) because its slot was too far away.
func IncYahooRateLimitBusy(limiter string) {
	yahooRateLimitBusy.WithLabelValues(limiter).Inc()
}

// IncYahooRateLimitFallback counts one token taken from the fallback limiter.
func IncYahooRateLimitFallback() {
	yahooRateLimitFallbacks.Inc()
}

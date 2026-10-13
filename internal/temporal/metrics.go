package temporal

import (
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/uber-go/tally/v4"
	tallyprom "github.com/uber-go/tally/v4/prometheus"
	"go.temporal.io/sdk/client"
	contribtally "go.temporal.io/sdk/contrib/tally"
)

// metricsReportInterval is how often the tally scope flushes buffered
// values into the Prometheus reporter. Prometheus itself is pull-based
// (scraped via registerer), so this only bounds how stale a just-recorded
// value can look between scrapes.
const metricsReportInterval = time.Second

// temporalLatencyBuckets covers every SDK timer (request latency, usually
// sub-second, through schedule-to-start latency, which can run to minutes
// when the worker is queue-bound) with histogram buckets. A histogram is
// required rather than tally/Prometheus's default summary type: a summary's
// quantiles are computed client-side and cannot be aggregated across pods or
// task queues, which defeats the point of a fleet-wide queueing signal.
var temporalLatencyBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300,
}

// NewMetricsHandler builds a Temporal SDK metrics handler that reports
// through Prometheus, registering its collectors on registerer so SDK
// metrics (activity/workflow schedule-to-start latency, worker task slot
// gauges, poller counts) are served next to whatever else registerer
// already carries. The returned closer flushes and stops the background
// reporting loop; it must be closed on shutdown.
func NewMetricsHandler(registerer prometheus.Registerer) (client.MetricsHandler, io.Closer) {
	reporter := tallyprom.NewReporter(tallyprom.Options{
		Registerer:              registerer,
		DefaultTimerType:        tallyprom.HistogramTimerType,
		DefaultHistogramBuckets: temporalLatencyBuckets,
		// The SDK keeps its own tag sets internally consistent per metric
		// name, so a registration collision here would mean an SDK change
		// upstream. Log and drop the metric rather than taking the whole
		// worker process down over an observability detail.
		OnRegisterError: func(err error) {
			log.Warn().Err(err).Msg("temporal metrics: prometheus registration failed")
		},
	})
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		SanitizeOptions: &contribtally.PrometheusSanitizeOptions,
		Separator:       tallyprom.DefaultSeparator,
		CachedReporter:  reporter,
	}, metricsReportInterval)
	return contribtally.NewMetricsHandler(contribtally.NewPrometheusNamingScope(scope)), closer
}

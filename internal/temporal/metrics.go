package temporal

import (
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

// NewMetricsHandler builds a Temporal SDK metrics handler that reports
// through Prometheus, registering its collectors on registerer so SDK
// metrics (activity/workflow schedule-to-start latency, worker task slot
// gauges, poller counts) are served next to whatever else registerer
// already carries. The returned closer flushes and stops the background
// reporting loop; it must be closed on shutdown.
func NewMetricsHandler(registerer prometheus.Registerer) (client.MetricsHandler, io.Closer) {
	reporter := tallyprom.NewReporter(tallyprom.Options{Registerer: registerer})
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		SanitizeOptions: &contribtally.PrometheusSanitizeOptions,
		Separator:       tallyprom.DefaultSeparator,
		CachedReporter:  reporter,
	}, metricsReportInterval)
	return contribtally.NewMetricsHandler(contribtally.NewPrometheusNamingScope(scope)), closer
}

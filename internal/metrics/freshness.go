package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// dataLatestDateLayout formats the date label of puckdb_data_latest_date_info.
const dataLatestDateLayout = "2006-01-02"

// Data freshness metrics, registered on CollectorRegistry in metrics.go: the
// newest data each imported dataset holds, so a dashboard can show the last
// imported day and how many days ago it was.
var (
	dataLatestTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_latest_timestamp",
		Help: "Unix timestamp of the newest data in a dataset (a date column counts as 00:00 UTC of that date)",
	}, []string{"dataset"})

	dataLatestDateInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_latest_date_info",
		Help: "Always 1; the date label is the UTC date (YYYY-MM-DD) of the newest data in a dataset",
	}, []string{"dataset", "date"})
)

// SetDataLatest records the newest data of a dataset. The date series of the
// previous value is removed, so each dataset has exactly one date series.
func SetDataLatest(dataset string, latest time.Time) {
	dataLatestTimestamp.WithLabelValues(dataset).Set(float64(latest.Unix()))
	dataLatestDateInfo.DeletePartialMatch(prometheus.Labels{"dataset": dataset})
	dataLatestDateInfo.WithLabelValues(dataset, latest.UTC().Format(dataLatestDateLayout)).Set(1)
}

// ClearDataLatest removes a dataset's series, for a dataset that holds no data.
func ClearDataLatest(dataset string) {
	dataLatestTimestamp.DeleteLabelValues(dataset)
	dataLatestDateInfo.DeletePartialMatch(prometheus.Labels{"dataset": dataset})
}

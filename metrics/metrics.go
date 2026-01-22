package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

// Histogram buckets optimized for different latency profiles
var (
	// Filesystem operations: 1ms to 30s (NFS can be slow)
	fsBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

	// HTTP operations: 10ms to 120s
	httpBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

	// Bytes: 1KB to 100MB
	bytesBuckets = []float64{1024, 10240, 102400, 1048576, 10485760, 104857600}
)

// Filesystem metrics
var (
	fsOpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_fs_operation_duration_seconds",
		Help:    "Duration of filesystem operations in seconds",
		Buckets: fsBuckets,
	}, []string{"operation", "file_type"})

	fsBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_fs_bytes",
		Help:    "Size of filesystem read/write operations in bytes",
		Buckets: bytesBuckets,
	}, []string{"operation", "file_type"})
)

// HTTP metrics
var (
	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: httpBuckets,
	}, []string{"api", "status_code"})

	httpResponseBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_response_bytes",
		Help:    "Size of HTTP response bodies in bytes",
		Buckets: bytesBuckets,
	}, []string{"api"})
)

// Download workflow metrics
var (
	downloadTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_download_total",
		Help: "Total number of download operations by result",
	}, []string{"file_type", "result"})
)

// ObserveFSOp records a filesystem operation duration and optionally bytes
func ObserveFSOp(operation, fileType string, duration time.Duration, bytes int) {
	fsOpDuration.WithLabelValues(operation, fileType).Observe(duration.Seconds())
	if bytes > 0 {
		fsBytes.WithLabelValues(operation, fileType).Observe(float64(bytes))
	}
}

// ObserveHTTP records an HTTP request duration and response size
func ObserveHTTP(api string, statusCode int, duration time.Duration, bytes int) {
	httpRequestDuration.WithLabelValues(api, fmt.Sprintf("%d", statusCode)).Observe(duration.Seconds())
	if bytes > 0 {
		httpResponseBytes.WithLabelValues(api).Observe(float64(bytes))
	}
}

// IncDownload increments the download counter
// result should be "hit", "miss", or "error"
func IncDownload(fileType, result string) {
	downloadTotal.WithLabelValues(fileType, result).Inc()
}

// StartServer starts the Prometheus metrics HTTP server
func StartServer(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Info().Str("addr", addr).Msg("Starting metrics server")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error().Err(err).Msg("Metrics server error")
	}
}

package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/core"
)

const healthCheckTimeout = 5 * time.Second

// Download result constants for metrics labels.
const (
	ResultHit           = "hit"            // Data found in cache
	ResultMiss          = "miss"           // Cache miss, fetched from source
	ResultError         = "error"          // Error during fetch or cache read
	ResultRedisHit      = "redis_hit"      // Data found in Redis cache
	ResultFSHit         = "fs_hit"         // Data found in filesystem cache
	ResultAPIFetch      = "api_fetch"      // Successfully fetched from API
	ResultStaleFallback = "stale_fallback" // Used stale data after API failure
	ResultMissing       = "missing"        // Resource confirmed not to exist (404)
	ResultSkip          = "skip"           // Skipped processing (e.g., no games played)
)

// Separate registries for different components
var (
	// WorkerRegistry contains metrics for the worker process
	WorkerRegistry = prometheus.NewRegistry()

	// APIRegistry contains metrics for the API process
	APIRegistry = prometheus.NewRegistry()

	// CollectorRegistry contains metrics for the metrics collector process
	CollectorRegistry = prometheus.NewRegistry()
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

// Worker metrics (filesystem, HTTP, downloads, activities)
var (
	fsOpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_fs_operation_duration_seconds",
		Help:    "Duration of filesystem operations in seconds",
		Buckets: fsBuckets,
	}, []string{"operation", "file_type"})

	fsBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_fs_bytes",
		Help:    "Size of filesystem read/write operations in bytes",
		Buckets: bytesBuckets,
	}, []string{"operation", "file_type"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: httpBuckets,
	}, []string{"api", "method", "status_code"})

	httpResponseBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_response_bytes",
		Help:    "Size of HTTP response bodies in bytes",
		Buckets: bytesBuckets,
	}, []string{"api"})

	downloadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_download_total",
		Help: "Total number of download operations by result",
	}, []string{"file_type", "result"})

	activityDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_activity_duration_seconds",
		Help:    "Duration of Temporal activities in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"activity"})

	// Simulation-specific worker metrics. PLAN.md > "Phase 3.3
	// Prometheus Metrics" — three metrics covering the LLM cost
	// surface (call latency, failure rate by classifier) and the
	// per-pool day-loop pacing.
	//
	// LLM call duration buckets cover the realistic range: fast
	// Haiku calls land sub-second, slower Anthropic Sonnet calls
	// can run multi-second on a tool-heavy turn, and pathological
	// retries reach 60s+.
	simLLMBuckets = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120}

	// Day-loop duration buckets cover one calendar day's worth of
	// activities (Waivers + FA pool + N agent turns + stats +
	// standings). Realistic: 5–60s for small pools, 60s+ for
	// large pools or slow LLMs.
	simDayBuckets = []float64{1, 5, 10, 30, 60, 120, 300, 600}

	simLLMCallDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_sim_llm_call_duration_seconds",
		Help:    "Duration of one LLM Complete call in the simulation worker, by provider/model/agent",
		Buckets: simLLMBuckets,
	}, []string{"provider", "model", "agent_name"})

	simLLMFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_sim_llm_failures_total",
		Help: "LLM failures classified by reason (timeout|api_error|tool_use_failure|parse_error|validation)",
	}, []string{"provider", "model", "reason"})

	// No pool_id label: pool identifiers are unbounded, so labeling
	// per pool would blow up Prometheus series cardinality without
	// bound. Per-pool day durations live in the DB / trace tables
	// instead; this metric measures the fleet-wide day-loop pacing
	// distribution.
	simDayDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "puckdb_sim_day_duration_seconds",
		Help:    "Wall-clock duration of one simulated day's activity batch (waivers + FA pool + agents + stats + standings), aggregated across all pools",
		Buckets: simDayBuckets,
	})
)

// Simulation LLM-failure reason labels. Constants rather than free
// strings so the activities and the classifier stay in lockstep —
// a typo would land in Prometheus as a new (and silent) reason.
const (
	SimFailureTimeout        = "timeout"
	SimFailureAPIError       = "api_error"
	SimFailureToolUseFailure = "tool_use_failure"
	SimFailureParseError     = "parse_error"
	SimFailureValidation     = "validation"
)

// API metrics (HTTP middleware)
var (
	apiHTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: httpBuckets,
	}, []string{"api", "method", "status_code"})
)

// Collector metrics (cache, Redis, database)
var (
	cacheFilesExpected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_files_expected",
		Help: "Expected number of cache files",
	}, []string{"season", "file_type"})

	cacheFilesFound = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_files_found",
		Help: "Actual number of cache files found",
	}, []string{"season", "file_type"})

	cacheCompletenessPercent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_completeness_percent",
		Help: "Cache completeness percentage (found/expected * 100)",
	}, []string{"season", "file_type"})

	cacheLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_stats_last_updated_timestamp",
		Help: "Unix timestamp of last cache stats update",
	})

	cacheComputeDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_stats_compute_duration_seconds",
		Help: "Time taken to compute cache statistics",
	})

	cacheDiskSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_disk_size_bytes",
		Help: "Total disk space used by cache directory in bytes",
	})

	redisOAuthTokenValid = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_redis_oauth_token_valid",
		Help: "Whether a valid OAuth token exists for a user (1=valid, 0=missing/expired)",
	}, []string{"user"})

	redisLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_redis_last_updated_timestamp",
		Help: "Unix timestamp of last Redis metrics update",
	})

	dbTableRowCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_db_table_row_count",
		Help: "Number of rows in database tables",
	}, []string{"table"})

	dbTableSizeBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_db_table_size_bytes",
		Help: "Total on-disk size in bytes per table (heap + indexes + TOAST)",
	}, []string{"table"})

	dbSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_db_size_bytes",
		Help: "Total size of the database in bytes",
	})

	dbLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_db_last_updated_timestamp",
		Help: "Unix timestamp of last database metrics update",
	})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_build_info",
		Help: "Build version of the running binary",
	}, []string{"version"})

	dataPathFilesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_path_files_total",
		Help: "Total number of files by type in data path",
	}, []string{"file_type"})

	dataPathBytesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_path_bytes_total",
		Help: "Total bytes by file type in data path",
	}, []string{"file_type"})
)

func init() {
	// Register worker metrics
	WorkerRegistry.MustRegister(
		fsOpDuration,
		fsBytes,
		httpRequestDuration,
		httpResponseBytes,
		downloadTotal,
		activityDuration,
		simLLMCallDuration,
		simLLMFailures,
		simDayDuration,
	)

	// Register API metrics
	APIRegistry.MustRegister(
		apiHTTPRequestDuration,
	)

	// Register collector metrics
	CollectorRegistry.MustRegister(
		cacheFilesExpected,
		cacheFilesFound,
		cacheCompletenessPercent,
		cacheLastUpdated,
		cacheComputeDuration,
		cacheDiskSizeBytes,
		redisOAuthTokenValid,
		redisLastUpdated,
		dbTableRowCount,
		dbTableSizeBytes,
		dbSizeBytes,
		dbLastUpdated,
		buildInfo,
		dataPathFilesTotal,
		dataPathBytesTotal,
	)
}

// ObserveFSOp records a filesystem operation duration and optionally bytes
func ObserveFSOp(operation string, fileType core.FileType, duration time.Duration, bytes int) {
	label := fileType.String()
	fsOpDuration.WithLabelValues(operation, label).Observe(duration.Seconds())
	if bytes > 0 {
		fsBytes.WithLabelValues(operation, label).Observe(float64(bytes))
	}
}

// ObserveHTTP records an HTTP request duration and response size
func ObserveHTTP(api, method string, statusCode int, duration time.Duration, bytes int) {
	httpRequestDuration.WithLabelValues(api, method, fmt.Sprintf("%d", statusCode)).Observe(duration.Seconds())
	if bytes > 0 {
		httpResponseBytes.WithLabelValues(api).Observe(float64(bytes))
	}
}

// IncDownload increments the download counter
// result should be "hit", "miss", or "error"
func IncDownload(fileType core.FileType, result string) {
	downloadTotal.WithLabelValues(fileType.String(), result).Inc()
}

// ObserveActivityDuration records a Temporal activity's total duration
func ObserveActivityDuration(activity string, duration time.Duration) {
	activityDuration.WithLabelValues(activity).Observe(duration.Seconds())
}

// TrackActivityDuration returns a function to be deferred for timing activities.
// Usage: defer TrackActivityDuration("ActivityName")()
func TrackActivityDuration(activityName string) func() {
	start := time.Now()
	return func() {
		ObserveActivityDuration(activityName, time.Since(start))
	}
}

// HTTPMetricsMiddleware returns middleware that records HTTP request metrics for the API.
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(ww, r)

		duration := time.Since(start)
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		apiHTTPRequestDuration.WithLabelValues(path, r.Method, fmt.Sprintf("%d", ww.statusCode)).Observe(duration.Seconds())
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// HandlerFor returns a promhttp.Handler for the specified registry
func HandlerFor(registry *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// shutdownTimeout bounds how long Shutdown waits for in-flight requests to drain
// before the server closes the listener and returns.
const shutdownTimeout = 10 * time.Second

// StartServer starts the Prometheus metrics HTTP server for the collector.
// It blocks until ctx is canceled or the server fails to listen, then performs
// a graceful shutdown bounded by shutdownTimeout.
func StartServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", HandlerFor(CollectorRegistry))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	if err := serveWithShutdown(ctx, srv); err != nil {
		log.Error().Err(err).Msg("Metrics server error")
	}
}

// StartWorkerServer starts the Prometheus metrics HTTP server for the worker.
// dataPath is the filesystem path to check in the health endpoint (e.g. JuiceFS mount).
// Blocks until ctx is canceled or the server fails to listen, then performs a
// graceful shutdown bounded by shutdownTimeout. Returns the listen/serve error
// so the caller (which runs Temporal in a peer goroutine) can terminate the
// whole process on failure.
func StartWorkerServer(ctx context.Context, addr, dataPath string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", HandlerFor(WorkerRegistry))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		done := make(chan error, 1)
		go func() {
			_, err := os.Stat(dataPath)
			done <- err
		}()

		select {
		case err := <-done:
			if err != nil {
				http.Error(w, "data path unhealthy: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		case <-ctx.Done():
			http.Error(w, "data path timeout", http.StatusServiceUnavailable)
		}
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	return serveWithShutdown(ctx, srv)
}

// serveWithShutdown runs srv.ListenAndServe in a goroutine and triggers a
// graceful shutdown when ctx is canceled. Returns nil on clean shutdown,
// otherwise the underlying ListenAndServe error.
func serveWithShutdown(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return <-errCh
	}
}

// SetCacheMetrics updates cache statistics gauges for a season and file type
func SetCacheMetrics(season, fileType string, expected, found int) {
	cacheFilesExpected.WithLabelValues(season, fileType).Set(float64(expected))
	cacheFilesFound.WithLabelValues(season, fileType).Set(float64(found))
	if expected > 0 {
		cacheCompletenessPercent.WithLabelValues(season, fileType).Set(float64(found) / float64(expected) * 100)
	} else {
		cacheCompletenessPercent.WithLabelValues(season, fileType).Set(0)
	}
}

// SetCacheMetricsTimestamp records when cache metrics were last computed
func SetCacheMetricsTimestamp() {
	cacheLastUpdated.Set(float64(time.Now().Unix()))
}

// SetCacheComputeDuration records how long the stats computation took
func SetCacheComputeDuration(d time.Duration) {
	cacheComputeDuration.Set(d.Seconds())
}

// SetCacheDiskSizeBytes records the total disk space used by the cache
func SetCacheDiskSizeBytes(bytes int64) {
	cacheDiskSizeBytes.Set(float64(bytes))
}

// SetRedisOAuthTokenValid records whether a user has a valid OAuth token
func SetRedisOAuthTokenValid(user string, valid bool) {
	val := 0.0
	if valid {
		val = 1.0
	}
	redisOAuthTokenValid.WithLabelValues(user).Set(val)
}

// SetRedisMetricsTimestamp records when Redis metrics were last computed
func SetRedisMetricsTimestamp() {
	redisLastUpdated.Set(float64(time.Now().Unix()))
}

// SetDBTableRowCount records the row count for a database table
func SetDBTableRowCount(table string, count int64) {
	dbTableRowCount.WithLabelValues(table).Set(float64(count))
}

// SetDBTableSizeBytes records the on-disk size in bytes for a database table.
// The value should come from pg_total_relation_size, which includes the heap,
// indexes, and TOAST.
func SetDBTableSizeBytes(table string, bytes int64) {
	dbTableSizeBytes.WithLabelValues(table).Set(float64(bytes))
}

// SetDBSizeBytes records the total database size in bytes
func SetDBSizeBytes(bytes int64) {
	dbSizeBytes.Set(float64(bytes))
}

// SetDBMetricsTimestamp records when database metrics were last computed
func SetDBMetricsTimestamp() {
	dbLastUpdated.Set(float64(time.Now().Unix()))
}

// SetBuildInfo records the build version as a label with gauge value 1
func SetBuildInfo(version string) {
	buildInfo.WithLabelValues(version).Set(1)
}

// SetDataPathFileStats records file count and size for a file type
func SetDataPathFileStats(fileType string, count int64, bytes int64) {
	dataPathFilesTotal.WithLabelValues(fileType).Set(float64(count))
	dataPathBytesTotal.WithLabelValues(fileType).Set(float64(bytes))
}

// ObserveSimLLMCallDuration records the duration of one LLM Complete
// call in the simulation worker. Called by the instrumented client
// wrapper around every Complete invocation; provider/model/agent_name
// labels let dashboards split spend and latency per agent.
func ObserveSimLLMCallDuration(provider, model, agentName string, duration time.Duration) {
	simLLMCallDuration.WithLabelValues(provider, model, agentName).Observe(duration.Seconds())
}

// IncSimLLMFailure increments the simulation's LLM-failure counter
// with a typed reason label. Reasons are the SimFailure* constants;
// the classifier in the simulation package maps Go errors to these
// strings.
func IncSimLLMFailure(provider, model, reason string) {
	simLLMFailures.WithLabelValues(provider, model, reason).Inc()
}

// ObserveSimDayDuration records the wall-clock duration of one
// simulated day's activity batch. Called from a small telemetry
// activity invoked by SimPoolWorkflow at the end of each day —
// the workflow goroutine itself can't observe Prometheus directly
// (non-deterministic side effect breaks replay).
//
// Deliberately unlabeled: see the simDayDuration declaration for why
// pool_id is not a label (unbounded cardinality).
func ObserveSimDayDuration(duration time.Duration) {
	simDayDuration.Observe(duration.Seconds())
}

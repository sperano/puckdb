package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sperano/puckdb/internal/core"
)

// Worker metrics (filesystem, HTTP, downloads, activities), registered on
// WorkerRegistry in metrics.go.
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
		Buckets: activityBuckets,
	}, []string{"activity"})

	dbQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_db_query_duration_seconds",
		Help:    "Duration of sqlc database queries in seconds",
		Buckets: dbQueryBuckets,
	}, []string{"query"})

	valkeyCacheRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "puckdb_valkey_cache_requests_total",
		Help: "Total Valkey-backed cache reads by cache and result",
	}, []string{"cache", "result"})

	// Simulation-specific worker metrics. PLAN.md > "Phase 3.3
	// Prometheus Metrics" — three metrics covering the LLM cost
	// surface (call latency, failure rate by classifier) and the
	// per-pool day-loop pacing.
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

// ObserveDBQueryDuration records the total time spent executing and consuming
// one sqlc query call, including an entire homogeneous batch when applicable.
func ObserveDBQueryDuration(query string, duration time.Duration) {
	dbQueryDuration.WithLabelValues(query).Observe(duration.Seconds())
}

// IncValkeyCacheRequest records one logical cache read. Cache names must be a
// bounded set: the resource file types (core.FileType.String()) and "gob", the
// name GobCache.Get emits directly for key-based reads; never pass resource keys.
func IncValkeyCacheRequest(cache, result string) {
	valkeyCacheRequests.WithLabelValues(cache, result).Inc()
}

// TrackActivityDuration returns a function to be deferred for timing activities.
// Usage: defer TrackActivityDuration("ActivityName")()
func TrackActivityDuration(activityName string) func() {
	start := time.Now()
	return func() {
		ObserveActivityDuration(activityName, time.Since(start))
	}
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

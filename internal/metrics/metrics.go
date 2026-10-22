// Package metrics defines the Prometheus metrics PuckDB exposes and the three
// registries that own them. PuckDB runs three separate processes, each serving
// its own registry:
//
//   - WorkerRegistry — served by the worker process (StartWorkerServer):
//     filesystem, HTTP-client, download, Temporal activity, database-query,
//     Valkey-cache, and simulation metrics.
//   - APIRegistry — served by the API process via HTTPMetricsMiddleware: the
//     API server's own request duration.
//   - CollectorRegistry — served by the standalone collector process
//     (StartServer): cache, Redis, and database-state gauges plus build info.
//
// A metric is registered on exactly one registry; its owning registry is
// stated next to its declaration. The metric declarations are split by
// registry across the sibling files worker.go, api.go, and collector.go. This
// file holds the registries, the shared histogram buckets, the Result* label
// constants, and the HTTP-server plumbing that serves them.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
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

	// Temporal activities range from milliseconds to long-running imports and
	// downloads. The upper buckets keep multi-minute activities out of +Inf.
	activityBuckets = []float64{
		0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
		1, 2.5, 5, 10, 30, 60, 120, 300, 600, 900, 1800,
	}

	// SQL statements are normally sub-second, but imports can run for minutes.
	dbQueryBuckets = []float64{
		0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
		1, 2.5, 5, 10, 30, 60, 120, 300, 600,
	}

	// LLM call duration buckets cover the realistic range: fast Haiku calls
	// land sub-second, slower Anthropic Sonnet calls can run multi-second on
	// a tool-heavy turn, and pathological retries reach 60s+.
	simLLMBuckets = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120}

	// Day-loop duration buckets cover one calendar day's worth of activities
	// (Waivers + FA pool + N agent turns + stats + standings). Realistic:
	// 5–60s for small pools, 60s+ for large pools or slow LLMs.
	simDayBuckets = []float64{1, 5, 10, 30, 60, 120, 300, 600}
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
		dbQueryDuration,
		valkeyCacheRequests,
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

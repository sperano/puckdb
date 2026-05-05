package metrics

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sperano/puckdb/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserveFSOp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		fileType  string
		duration  time.Duration
		bytes     int
	}{
		{"read with bytes", "read", "boxscore", 100 * time.Millisecond, 1024},
		{"write with bytes", "write", "schedule", 50 * time.Millisecond, 2048},
		{"read without bytes", "read", "standings", 10 * time.Millisecond, 0},
		{"negative bytes ignored", "stat", "roster", 5 * time.Millisecond, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				ObserveFSOp(tt.operation, tt.fileType, tt.duration, tt.bytes)
			})
		})
	}
}

func TestObserveHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		api        string
		method     string
		statusCode int
		duration   time.Duration
		bytes      int
	}{
		{"successful GET", "nhl", "GET", 200, 100 * time.Millisecond, 4096},
		{"POST with error", "yahoo", "POST", 500, 2 * time.Second, 256},
		{"GET no bytes", "public", "GET", 204, 50 * time.Millisecond, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				ObserveHTTP(tt.api, tt.method, tt.statusCode, tt.duration, tt.bytes)
			})
		})
	}
}

func TestIncDownload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fileType core.FileType
		result   string
	}{
		{core.Boxscore, ResultHit},
		{core.DailySchedule, ResultMiss},
		{core.DailyStandings, ResultError},
	}

	for _, tt := range tests {
		t.Run(tt.fileType.String()+"_"+tt.result, func(t *testing.T) {
			assert.NotPanics(t, func() {
				IncDownload(tt.fileType, tt.result)
			})
		})
	}
}

func TestObserveActivityDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		activity string
		duration time.Duration
	}{
		{"DownloadBoxscore", 5 * time.Second},
		{"ProcessSchedule", 100 * time.Millisecond},
		{"SaveToDatabase", 1 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.activity, func(t *testing.T) {
			assert.NotPanics(t, func() {
				ObserveActivityDuration(tt.activity, tt.duration)
			})
		})
	}
}

func TestHTTPMetricsMiddleware(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	middleware := HTTPMetricsMiddleware(handler)

	tests := []struct {
		name   string
		path   string
		method string
	}{
		{"root path", "/", "GET"},
		{"api path", "/api/v1/games", "GET"},
		{"trailing slash normalized", "/metrics/", "GET"},
		{"post request", "/graphql", "POST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()

			middleware.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "ok", rr.Body.String())
		})
	}
}

func TestHTTPMetricsMiddleware_StatusCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"201 Created", http.StatusCreated},
		{"400 Bad Request", http.StatusBadRequest},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			middleware := HTTPMetricsMiddleware(handler)
			req := httptest.NewRequest("GET", "/test", nil)
			rr := httptest.NewRecorder()

			middleware.ServeHTTP(rr, req)

			assert.Equal(t, tt.statusCode, rr.Code)
		})
	}
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, rw.statusCode)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandlerFor(t *testing.T) {
	t.Parallel()

	t.Run("worker registry", func(t *testing.T) {
		handler := HandlerFor(WorkerRegistry)
		require.NotNil(t, handler)

		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("api registry", func(t *testing.T) {
		handler := HandlerFor(APIRegistry)
		require.NotNil(t, handler)

		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("collector registry", func(t *testing.T) {
		handler := HandlerFor(CollectorRegistry)
		require.NotNil(t, handler)

		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestSetCacheMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		season   string
		fileType string
		expected int
		found    int
	}{
		{"full cache", "2023", "boxscore", 1000, 1000},
		{"partial cache", "2022", "schedule", 82, 50},
		{"empty cache", "2021", "standings", 100, 0},
		{"zero expected", "2020", "roster", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				SetCacheMetrics(tt.season, tt.fileType, tt.expected, tt.found)
			})
		})
	}
}

func TestSetCacheMetricsTimestamp(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		SetCacheMetricsTimestamp()
	})
}

func TestSetCacheComputeDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
	}{
		{"fast", 100 * time.Millisecond},
		{"medium", 5 * time.Second},
		{"slow", 1 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				SetCacheComputeDuration(tt.duration)
			})
		})
	}
}

func TestSetCacheDiskSizeBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int64
	}{
		{"small", 1024},
		{"medium", 1024 * 1024 * 100},
		{"large", 1024 * 1024 * 1024 * 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				SetCacheDiskSizeBytes(tt.bytes)
			})
		})
	}
}

func TestSetRedisOAuthTokenValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		user  string
		valid bool
	}{
		{"valid token", "user1", true},
		{"invalid token", "user2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				SetRedisOAuthTokenValid(tt.user, tt.valid)
			})
		})
	}
}

func TestSetRedisMetricsTimestamp(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		SetRedisMetricsTimestamp()
	})
}

func TestSetDBTableRowCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		table string
		count int64
	}{
		{"players", 5000},
		{"games", 1312},
		{"teams", 32},
	}

	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			assert.NotPanics(t, func() {
				SetDBTableRowCount(tt.table, tt.count)
			})
		})
	}
}

func TestSetDBMetricsTimestamp(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		SetDBMetricsTimestamp()
	})
}

// The tests below verify gauge values via prometheus/testutil rather than
// just NotPanics. Different convention from the older tests above —
// stronger guarantee, same in-process cost. Cannot run with t.Parallel()
// since they share global gauge state with each other and with any test
// that writes to the same metric.

func TestTrackActivityDuration(t *testing.T) {
	stop := TrackActivityDuration("UnitTestActivity")
	time.Sleep(1 * time.Millisecond)
	require.NotNil(t, stop)
	assert.NotPanics(t, stop)
	// Calling the returned closure twice must not panic — this is the
	// shape that production code accidentally produces if a deferred
	// stop is also called explicitly.
	assert.NotPanics(t, stop)
}

func TestSetDBSizeBytes(t *testing.T) {
	const want = 1234567890
	SetDBSizeBytes(want)
	assert.Equal(t, float64(want), testutil.ToFloat64(dbSizeBytes))
}

func TestSetDBTableSizeBytes(t *testing.T) {
	const table = "shifts"
	const want = 2_566_955_008
	SetDBTableSizeBytes(table, want)
	assert.Equal(t, float64(want), testutil.ToFloat64(dbTableSizeBytes.WithLabelValues(table)))
}

func TestSetBuildInfo(t *testing.T) {
	// Use a unique version string so this test can't collide with any
	// other test or process-level call to SetBuildInfo.
	const version = "v0.0.0-test-abc123"
	SetBuildInfo(version)
	assert.Equal(t, float64(1), testutil.ToFloat64(buildInfo.WithLabelValues(version)))
}

func TestSetDataPathFileStats(t *testing.T) {
	const fileType = "test-file-type-stats"
	const wantCount = 42
	const wantBytes = 1024 * 1024
	SetDataPathFileStats(fileType, wantCount, wantBytes)
	assert.Equal(t, float64(wantCount), testutil.ToFloat64(dataPathFilesTotal.WithLabelValues(fileType)))
	assert.Equal(t, float64(wantBytes), testutil.ToFloat64(dataPathBytesTotal.WithLabelValues(fileType)))
}

// --- Server lifecycle (StartServer, StartWorkerServer, serveWithShutdown) ---

// reservePort grabs an OS-assigned port on 127.0.0.1, closes the listener,
// and returns the bound address. The test caller then races the metrics
// server to claim that port. There's a tiny TOCTOU window where another
// process could grab the port first, but the loopback-only constraint
// and the immediate handoff make this acceptable for test purposes.
func reservePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// waitForServer polls until the server responds with 200 on /health or
// the deadline expires. Servers spin up asynchronously, so callers can't
// just hit the endpoint immediately.
func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", addr)
}

func TestStartServer_GracefulShutdown(t *testing.T) {
	addr := reservePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		StartServer(ctx, addr)
		close(done)
	}()

	waitForServer(t, addr)

	// /metrics should respond with 200 — the only purpose of this server
	// is to expose the CollectorRegistry.
	resp, err := http.Get("http://" + addr + "/metrics")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("StartServer did not return after context cancellation")
	}
}

func TestStartWorkerServer_HealthOK(t *testing.T) {
	addr := reservePort(t)
	dataPath := t.TempDir() // exists, so /health should report ok
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- StartWorkerServer(ctx, addr, dataPath) }()

	waitForServer(t, addr)

	t.Run("metrics endpoint", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/metrics")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("pprof endpoint", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/debug/pprof/cmdline")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "graceful shutdown should return nil")
	case <-time.After(15 * time.Second):
		t.Fatal("StartWorkerServer did not return after context cancellation")
	}
}

func TestStartWorkerServer_HealthFailsForMissingDataPath(t *testing.T) {
	addr := reservePort(t)
	// Path doesn't exist — /health should report 503.
	missingPath := filepath.Join(t.TempDir(), "does-not-exist")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)

	go func() { done <- StartWorkerServer(ctx, addr, missingPath) }()

	// Can't use waitForServer because /health would fail — poll /metrics
	// to detect readiness instead.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("StartWorkerServer did not return after context cancellation")
	}
}

func TestServeWithShutdown_ListenError(t *testing.T) {
	// Pre-bind a port so the metrics server's Listen call fails with
	// "address already in use" (or platform equivalent). The function
	// must return that error immediately rather than blocking.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	srv := &http.Server{Addr: l.Addr().String(), Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serveWithShutdown(ctx, srv) }()

	select {
	case err := <-errCh:
		require.Error(t, err, "Listen on already-bound port should fail")
	case <-time.After(2 * time.Second):
		t.Fatal("serveWithShutdown should have returned listen error promptly")
	}
}

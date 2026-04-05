package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

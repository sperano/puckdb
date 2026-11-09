package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bufferLogger returns a JSON logger writing to a fresh buffer, so a test
// reads the fields an entry logged without touching the global logger.
func bufferLogger() (*zerolog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	return &logger, &buf
}

// logLines decodes each JSON line buf holds.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal(raw, &line))
		lines = append(lines, line)
	}
	return lines
}

func TestChiLogger(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	loggedHandler := ChiLogger(handler)
	require.NotNil(t, loggedHandler)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("User-Agent", "TestClient/1.0")
	rr := httptest.NewRecorder()

	loggedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "ok", rr.Body.String())
}

func TestLogFormatter_NewLogEntry(t *testing.T) {
	t.Parallel()

	logger, _ := bufferLogger()
	formatter := logFormatter{logger: logger}

	req := httptest.NewRequest("POST", "/graphql", nil)
	req.Header.Set("User-Agent", "TestClient/1.0")
	req.RemoteAddr = "192.0.2.10:54321"

	entry := formatter.NewLogEntry(req)
	require.NotNil(t, entry)

	logEntry, ok := entry.(logEntry)
	require.True(t, ok)

	assert.Same(t, logger, logEntry.logger)
	assert.Equal(t, "TestClient/1.0", logEntry.userAgent)
	assert.Equal(t, "POST", logEntry.method)
	assert.Equal(t, "192.0.2.10:54321", logEntry.addr)
	assert.Equal(t, "/graphql", logEntry.path)
}

func TestLogEntry_Write_LogsRequestFields(t *testing.T) {
	t.Parallel()

	logger, buf := bufferLogger()
	entry := logEntry{
		logger:    logger,
		userAgent: "TestClient/1.0",
		method:    "GET",
		addr:      "192.0.2.10:54321",
		path:      "/api/games",
	}

	entry.Write(http.StatusNotFound, 1024, nil, 100*time.Millisecond, nil)

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "info", lines[0]["level"])
	assert.Equal(t, "GET /api/games", lines[0]["message"])
	assert.Equal(t, "TestClient/1.0", lines[0]["ua"])
	assert.Equal(t, "192.0.2.10:54321", lines[0]["addr"])
	assert.EqualValues(t, http.StatusNotFound, lines[0]["status"])
	assert.EqualValues(t, 1024, lines[0]["bytes"])
	assert.Contains(t, lines[0], "dur")
}

func TestLogEntry_Write_SkipsPingPaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/ping", "/ping/deep"} {
		logger, buf := bufferLogger()
		entry := logEntry{logger: logger, method: "GET", path: path}

		entry.Write(http.StatusOK, 0, nil, 10*time.Millisecond, nil)

		assert.Empty(t, buf.String(), path)
	}
}

func TestLogEntry_Panic_LogsStructuredError(t *testing.T) {
	t.Parallel()

	logger, buf := bufferLogger()
	entry := logEntry{
		logger:    logger,
		userAgent: "TestClient/1.0",
		method:    "POST",
		addr:      "127.0.0.1:8080",
		path:      "/api/submit",
	}
	panicValue := struct {
		Code    int
		Message string
	}{500, "internal error"}

	entry.Panic(panicValue, []byte("goroutine 1 [running]:\nmain.handler()"))

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "error", lines[0]["level"])
	assert.Equal(t, "POST", lines[0]["method"])
	assert.Equal(t, "/api/submit", lines[0]["path"])
	assert.Equal(t, "{Code:500 Message:internal error}", lines[0]["panic"])
	assert.Equal(t, "goroutine 1 [running]:\nmain.handler()", lines[0]["stack"])
}

// TestRecovererLogsPanicThroughRequestLogger runs the middleware order the
// API router uses: the recovered panic reaches the structured logger and
// the client gets a 500.
func TestRecovererLogsPanicThroughRequestLogger(t *testing.T) {
	t.Parallel()

	logger, buf := bufferLogger()
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	handler := middleware.RequestLogger(logFormatter{logger: logger})(middleware.Recoverer(panicking))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/draft/api/board", nil))

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	lines := logLines(t, buf)
	require.Len(t, lines, 2, "the panic, then the request")
	assert.Equal(t, "boom", lines[0]["panic"])
	assert.Equal(t, "/draft/api/board", lines[0]["path"])
	assert.EqualValues(t, http.StatusInternalServerError, lines[1]["status"])
}

func TestServerErrorWriter_LogsOneWarningPerLine(t *testing.T) {
	t.Parallel()

	logger, buf := bufferLogger()
	w := serverErrorWriter{logger: logger}

	n, err := w.Write([]byte("http: TLS handshake error from 192.0.2.1:5000: EOF\n"))

	require.NoError(t, err)
	assert.Equal(t, len("http: TLS handshake error from 192.0.2.1:5000: EOF\n"), n)
	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "warn", lines[0]["level"])
	assert.Equal(t, "http: TLS handshake error from 192.0.2.1:5000: EOF", lines[0]["message"])
}

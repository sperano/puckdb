package httpx

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	formatter := logFormatter{}

	req := httptest.NewRequest("POST", "/graphql", nil)
	req.Header.Set("User-Agent", "TestClient/1.0")
	req.RemoteAddr = "192.0.2.10:54321"

	entry := formatter.NewLogEntry(req)
	require.NotNil(t, entry)

	logEntry, ok := entry.(logEntry)
	require.True(t, ok)

	assert.Equal(t, "TestClient/1.0", logEntry.userAgent)
	assert.Equal(t, "POST", logEntry.method)
	assert.Equal(t, "192.0.2.10:54321", logEntry.addr)
	assert.Equal(t, "/graphql", logEntry.path)
}

func TestLogEntry_Write_NormalPath(t *testing.T) {
	t.Parallel()

	entry := logEntry{
		userAgent: "TestClient/1.0",
		method:    "GET",
		addr:      "192.0.2.10:54321",
		path:      "/api/games",
	}

	// Write should not panic
	assert.NotPanics(t, func() {
		entry.Write(200, 1024, nil, 100*time.Millisecond, nil)
	})
}

func TestLogEntry_Write_PingPath(t *testing.T) {
	t.Parallel()

	entry := logEntry{
		userAgent: "HealthChecker/1.0",
		method:    "GET",
		addr:      "192.0.2.1:12345",
		path:      "/ping",
	}

	// Write should return early for ping paths (no logging)
	assert.NotPanics(t, func() {
		entry.Write(200, 0, nil, 10*time.Millisecond, nil)
	})
}

func TestLogEntry_Write_PingSubpath(t *testing.T) {
	t.Parallel()

	entry := logEntry{
		userAgent: "HealthChecker/1.0",
		method:    "GET",
		addr:      "192.0.2.1:12345",
		path:      "/ping/deep",
	}

	// Write should return early for paths starting with /ping
	assert.NotPanics(t, func() {
		entry.Write(200, 0, nil, 10*time.Millisecond, nil)
	})
}

func TestLogEntry_Write_VariousStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		bytes  int
	}{
		{"200 OK", 200, 1024},
		{"201 Created", 201, 256},
		{"204 No Content", 204, 0},
		{"400 Bad Request", 400, 128},
		{"404 Not Found", 404, 64},
		{"500 Internal Server Error", 500, 512},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := logEntry{
				userAgent: "TestClient/1.0",
				method:    "GET",
				addr:      "127.0.0.1:8080",
				path:      "/test",
			}

			assert.NotPanics(t, func() {
				entry.Write(tt.status, tt.bytes, nil, 50*time.Millisecond, nil)
			})
		})
	}
}

// TestLogEntry_Panic and TestLogEntry_Panic_WithError both swap os.Stdout to
// capture Panic's output. They must NOT run in parallel — concurrent stdout
// swaps race for the global, causing one test's pipe to capture nothing.
func TestLogEntry_Panic(t *testing.T) {
	entry := logEntry{
		userAgent: "TestClient/1.0",
		method:    "GET",
		addr:      "127.0.0.1:8080",
		path:      "/api/crash",
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	panicValue := "something went wrong"
	stack := []byte("goroutine 1 [running]:\nmain.handler()")

	entry.Panic(panicValue, stack)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "panic")
	assert.Contains(t, output, panicValue)
	assert.Contains(t, output, "goroutine")
}

func TestLogEntry_Panic_WithError(t *testing.T) {
	entry := logEntry{
		userAgent: "TestClient/1.0",
		method:    "POST",
		addr:      "127.0.0.1:8080",
		path:      "/api/submit",
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	panicValue := struct {
		Code    int
		Message string
	}{500, "internal error"}
	stack := []byte("stack trace here")

	entry.Panic(panicValue, stack)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "panic")
	assert.Contains(t, output, "internal error")
}

func TestChiLogger_Integration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"GET request", "GET", "/api/v1/games", http.StatusOK},
		{"POST request", "POST", "/graphql", http.StatusOK},
		{"Not found", "GET", "/nonexistent", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			})

			loggedHandler := ChiLogger(handler)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()

			loggedHandler.ServeHTTP(rr, req)

			assert.Equal(t, tt.status, rr.Code)
		})
	}
}

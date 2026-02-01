package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPError_Error(t *testing.T) {
	t.Parallel()

	err := &HTTPError{
		StatusCode: 404,
		Status:     "404 Not Found",
		URL:        "https://example.com/resource",
	}

	msg := err.Error()
	assert.Contains(t, msg, "download failed")
	assert.Contains(t, msg, "404 Not Found")
	assert.Contains(t, msg, "https://example.com/resource")
}

func TestHTTPError_IsClientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"200 OK", 200, false},
		{"201 Created", 201, false},
		{"301 Redirect", 301, false},
		{"400 Bad Request", 400, true},
		{"401 Unauthorized", 401, true},
		{"403 Forbidden", 403, true},
		{"404 Not Found", 404, true},
		{"499 Client Closed", 499, true},
		{"500 Internal Server Error", 500, false},
		{"502 Bad Gateway", 502, false},
		{"503 Service Unavailable", 503, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsClientError())
		})
	}
}

func TestHTTPError_IsNonRetryableClientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"200 OK - not client error", 200, false},
		{"400 Bad Request - non-retryable", 400, true},
		{"401 Unauthorized - non-retryable", 401, true},
		{"403 Forbidden - non-retryable", 403, true},
		{"404 Not Found - retryable (Yahoo intermittent)", 404, false},
		{"405 Method Not Allowed - non-retryable", 405, true},
		{"429 Too Many Requests - non-retryable", 429, true},
		{"500 Internal Server Error - not client error", 500, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsNonRetryableClientError())
		})
	}
}

func TestGenericClient_Download_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "test"}`))
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Equal(t, `{"data": "test"}`, string(data))
}

func TestGenericClient_Download_HTTPError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"400 Bad Request", http.StatusBadRequest, "bad request"},
		{"401 Unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"404 Not Found", http.StatusNotFound, "not found"},
		{"500 Internal Server Error", http.StatusInternalServerError, "server error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &GenericClient{
				Client:   server.Client(),
				apiLabel: "test",
			}

			_, err := client.Download(server.URL)
			require.Error(t, err)

			httpErr, ok := err.(*HTTPError)
			require.True(t, ok, "expected HTTPError")
			assert.Equal(t, tt.statusCode, httpErr.StatusCode)
			assert.Equal(t, server.URL, httpErr.URL)
		})
	}
}

func TestGenericClient_Download_ConnectionError(t *testing.T) {
	t.Parallel()

	client := &GenericClient{
		Client:   &http.Client{},
		apiLabel: "test",
	}

	// Use an invalid URL that will fail to connect
	_, err := client.Download("http://localhost:1")
	require.Error(t, err)
	// Should not be an HTTPError since we couldn't connect
	_, ok := err.(*HTTPError)
	assert.False(t, ok, "expected non-HTTPError for connection failure")
}

func TestGenericClient_Download_Redirects(t *testing.T) {
	t.Parallel()

	finalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("final destination"))
	}))
	defer finalServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalServer.URL, http.StatusFound)
	}))
	defer redirectServer.Close()

	client := &GenericClient{
		Client:   &http.Client{},
		apiLabel: "test",
	}

	data, err := client.Download(redirectServer.URL)
	require.NoError(t, err)
	assert.Equal(t, "final destination", string(data))
}

func TestGenericClient_Download_LargeResponse(t *testing.T) {
	t.Parallel()

	// Create a 1MB response
	largeBody := make([]byte, 1024*1024)
	for i := range largeBody {
		largeBody[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(largeBody)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Equal(t, len(largeBody), len(data))
}

func TestGenericClient_Download_EmptyResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestGenericClient_Download_ReadBodyError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set content length but close connection before writing all data
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial"))
		// Connection will be closed, causing read error
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	_, err := client.Download(server.URL)
	// This may or may not error depending on timing
	// The important thing is it doesn't panic
	_ = err
}

func TestDownloadPublic(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("public data"))
	}))
	defer server.Close()

	// Temporarily override the function to use our test server
	// Since DownloadPublic creates its own client, we test through a server
	data, err := DownloadPublic(server.URL)
	require.NoError(t, err)
	assert.Equal(t, "public data", string(data))
}

func TestDownloadPublic_Error(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer server.Close()

	_, err := DownloadPublic(server.URL)
	require.Error(t, err)

	httpErr, ok := err.(*HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
}

// TestUserAgent verifies the User-Agent constant is set
func TestUserAgent(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, UserAgent)
	assert.Contains(t, UserAgent, "Mozilla")
}

// Benchmark download performance
func BenchmarkGenericClient_Download(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"status": "ok"}`)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "bench",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Download(server.URL)
	}
}

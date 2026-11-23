package mcp

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	mcpserversdk "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headerRecorder captures the headers of the most recent request a wrapped
// handler received, so a test can assert on what dialMCP actually sent over
// the wire rather than on its option plumbing alone.
type headerRecorder struct {
	mu      sync.Mutex
	headers http.Header
}

func (r *headerRecorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.headers = req.Header.Clone()
		r.mu.Unlock()
		next.ServeHTTP(w, req)
	})
}

func (r *headerRecorder) get(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.headers == nil {
		return ""
	}
	return r.headers.Get(key)
}

// newRecordingMCPServer starts a real streamable-HTTP MCP server (so
// dialMCP's Initialize handshake succeeds) behind a recorder that captures
// every request's headers.
func newRecordingMCPServer(t *testing.T) (*httptest.Server, *headerRecorder) {
	t.Helper()
	srv := mcpserversdk.NewMCPServer("test", "0.0.1")
	streamable := mcpserversdk.NewStreamableHTTPServer(srv)
	recorder := &headerRecorder{}
	httpSrv := httptest.NewServer(recorder.wrap(streamable))
	t.Cleanup(httpSrv.Close)
	return httpSrv, recorder
}

func TestDialMCP_SendsConfiguredHeaders(t *testing.T) {
	httpSrv, recorder := newRecordingMCPServer(t)

	conn, err := dialMCP(t.Context(), httpSrv.URL, map[string]string{
		"Authorization": "Bearer secret-key",
		"X-Client-Name": "kelos",
	})
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, "Bearer secret-key", recorder.get("Authorization"))
	assert.Equal(t, "kelos", recorder.get("X-Client-Name"))
}

func TestDialMCP_NoHeadersStaysUnchanged(t *testing.T) {
	httpSrv, recorder := newRecordingMCPServer(t)

	conn, err := dialMCP(t.Context(), httpSrv.URL, nil)
	require.NoError(t, err)
	defer conn.Close()

	assert.Empty(t, recorder.get("Authorization"))
}

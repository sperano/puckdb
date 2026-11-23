package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
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

// newRecordingSSEServer starts a real SSE MCP server behind a recorder that
// captures every request's headers. The handler is wired up after the
// httptest.Server starts, because the SSE server's endpoint event embeds the
// server's own base URL.
func newRecordingSSEServer(t *testing.T) (*httptest.Server, *headerRecorder) {
	t.Helper()
	recorder := &headerRecorder{}
	var sseHandler http.Handler
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.wrap(sseHandler).ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)

	srv := mcpserversdk.NewMCPServer("test", "0.0.1", mcpserversdk.WithToolCapabilities(false))
	srv.AddTool(mcpgo.NewTool("ping"), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("pong"), nil
	})
	sseHandler = mcpserversdk.NewSSEServer(srv, mcpserversdk.WithBaseURL(httpSrv.URL))
	return httpSrv, recorder
}

// TestDialMCP_SSE_SendsConfiguredHeaders exercises dialMCP's SSE path
// directly (a "/sse" URL skips the streamable-HTTP attempt), since the
// streamable-HTTP tests above never reach client.WithHeaders on
// NewSSEMCPClient.
func TestDialMCP_SSE_SendsConfiguredHeaders(t *testing.T) {
	httpSrv, recorder := newRecordingSSEServer(t)

	conn, err := dialMCP(t.Context(), httpSrv.URL+"/sse", map[string]string{
		"Authorization": "Bearer secret-key",
	})
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, "Bearer secret-key", recorder.get("Authorization"))
}

// TestDialMCP_SSE_StreamSurvivesDialContextCancellation pins the fix for the
// SSE transport's persistent background stream dying when the context given
// to dialMCP ends: mcpClient.ensureConnected dials with whatever context the
// triggering call happened to carry, then keeps the connection for later
// calls made with unrelated contexts, so the stream must outlive that first
// context rather than being canceled alongside it.
func TestDialMCP_SSE_StreamSurvivesDialContextCancellation(t *testing.T) {
	httpSrv, _ := newRecordingSSEServer(t)

	dialCtx, cancelDial := context.WithCancel(context.Background())
	conn, err := dialMCP(dialCtx, httpSrv.URL+"/sse", nil)
	require.NoError(t, err)
	defer conn.Close()

	// Simulate the triggering request ending right after the dial returns.
	cancelDial()

	_, err = conn.ListTools(context.Background(), mcpgo.ListToolsRequest{})
	assert.NoError(t, err, "SSE stream should stay open after the dial context is canceled")
}

// TestDialMCP_SSE_StartFailureIsWrapped covers the failure path of the SSE
// fallback's Start call: a non-200 response to the SSE GET must surface as a
// wrapped "start SSE transport" error, not a panic or a silent success.
func TestDialMCP_SSE_StartFailureIsWrapped(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(httpSrv.Close)

	conn, err := dialMCP(t.Context(), httpSrv.URL+"/sse", nil)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorContains(t, err, "start SSE transport for")
	assert.ErrorContains(t, err, "500")
}

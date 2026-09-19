package cmd

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runHTTPServerTestTimeout bounds how long a test waits for runHTTPServer to
// return after context cancellation or a listen failure.
const runHTTPServerTestTimeout = 5 * time.Second

// serverStartDeadline and serverPollInterval bound waitForListening's
// readiness polling of the asynchronously started test server.
const (
	serverStartDeadline = 2 * time.Second
	serverPollInterval  = 10 * time.Millisecond
)

// reservePort grabs an OS-assigned port on 127.0.0.1, closes the listener,
// and returns the bound address for the test server to reclaim. There is a
// small TOCTOU window where another process could take the port first; the
// loopback-only constraint and the immediate handoff make this acceptable
// for test purposes.
func reservePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// waitForListening polls addr until a TCP connection succeeds or the
// deadline expires. runHTTPServer starts ListenAndServe asynchronously, so
// callers can't dial or cancel immediately.
func waitForListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(serverStartDeadline)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(serverPollInterval)
	}
	t.Fatalf("server at %s did not start listening", addr)
}

func TestRunHTTPServer_ReturnsNilAfterContextCancel(t *testing.T) {
	addr := reservePort(t)
	srv := &http.Server{Addr: addr, Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	// Bound the server goroutine's lifetime even when waitForListening
	// fails the test before the explicit cancel below is reached.
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})

	errCh := make(chan error, 1)
	go func() { errCh <- runHTTPServer(ctx, srv, false, "", "") }()

	waitForListening(t, addr)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(runHTTPServerTestTimeout):
		t.Fatal("runHTTPServer did not return after context cancellation")
	}
}

func TestRunHTTPServer_ReturnsListenError(t *testing.T) {
	// Pre-bind a port so srv.ListenAndServe fails with "address already in use".
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	srv := &http.Server{Addr: l.Addr().String(), Handler: http.NewServeMux()}
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() { errCh <- runHTTPServer(ctx, srv, false, "", "") }()

	select {
	case err := <-errCh:
		require.Error(t, err, "ListenAndServe on an already-bound port should fail")
	case <-time.After(runHTTPServerTestTimeout):
		t.Fatal("runHTTPServer should have returned the listen error promptly")
	}
}

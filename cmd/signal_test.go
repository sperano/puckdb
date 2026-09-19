package cmd

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signalTestTimeout bounds how long a test waits for context cancellation
// (from a delivered signal or a canceled parent) before failing. Generous
// relative to normal signal-delivery latency to avoid flakes under load,
// short enough that a genuine hang still fails the test suite promptly.
const signalTestTimeout = 5 * time.Second

// TestSignalContext_CancelsOnSIGTERM sends SIGTERM to the test process's own
// PID and asserts the returned context is canceled. This exercises the real
// signal.NotifyContext plumbing rather than just the goroutine wiring.
func TestSignalContext_CancelsOnSIGTERM(t *testing.T) {
	ctx, stop := SignalContext(context.Background())
	defer stop()

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))

	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(signalTestTimeout):
		t.Fatal("context was not canceled after SIGTERM")
	}
}

// TestSignalContext_CancelsWithParent verifies that canceling the parent
// context (independent of any signal) also cancels the returned context.
func TestSignalContext_CancelsWithParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, stop := SignalContext(parent)
	defer stop()

	cancelParent()

	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(signalTestTimeout):
		t.Fatal("context was not canceled after parent cancellation")
	}
}

// TestSignalContext_StopIsIdempotent verifies the returned CancelFunc can be
// called more than once without panicking, matching context.CancelFunc's
// documented contract.
func TestSignalContext_StopIsIdempotent(t *testing.T) {
	ctx, stop := SignalContext(context.Background())

	assert.NotPanics(t, func() {
		stop()
		stop()
	})
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchSyncCancelTestTimeout bounds how long a test waits for
// watchSyncCancel's goroutine to observe a context transition.
const watchSyncCancelTestTimeout = 2 * time.Second

// TestWatchSyncCancel_RootCancellationOrdering verifies that on root-context
// cancellation, cancelWorkflows runs to completion before release is called
// — the ordering that lets in-flight workflow-cancel RPCs finish before
// polling is released.
func TestWatchSyncCancel_RootCancellationOrdering(t *testing.T) {
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	// Mirror runSync's construction: the polling context is detached from
	// the root via WithoutCancel so it survives root cancellation until
	// release is called explicitly.
	pollCtx, releasePoll := context.WithCancel(context.WithoutCancel(rootCtx))
	defer releasePoll()

	var order []string
	done := make(chan struct{})

	cancelWorkflows := func() {
		// The polling context must still be live while the workflow-cancel
		// RPCs run — the property the WithoutCancel detachment exists for.
		assert.NoError(t, pollCtx.Err())
		order = append(order, "cancelWorkflows")
	}
	release := func() {
		order = append(order, "release")
		releasePoll()
	}

	go func() {
		watchSyncCancel(pollCtx, rootCtx, cancelWorkflows, release)
		close(done)
	}()

	cancelRoot()

	select {
	case <-done:
	case <-time.After(watchSyncCancelTestTimeout):
		t.Fatal("watchSyncCancel did not return after root cancellation")
	}

	require.Equal(t, []string{"cancelWorkflows", "release"}, order)
}

// TestWatchSyncCancel_PollDoneFirst verifies that when pollCtx ends before
// rootCtx (the normal-completion path), watchSyncCancel returns without
// calling either callback.
func TestWatchSyncCancel_PollDoneFirst(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	called := false
	noop := func() { called = true }

	done := make(chan struct{})
	go func() {
		watchSyncCancel(pollCtx, rootCtx, noop, noop)
		close(done)
	}()

	cancelPoll()

	select {
	case <-done:
	case <-time.After(watchSyncCancelTestTimeout):
		t.Fatal("watchSyncCancel did not return after pollCtx cancellation")
	}

	assert.False(t, called, "neither callback should run when pollCtx ends first")
}

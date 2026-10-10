package draftboard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const watchTestTimeout = time.Second

func TestWatchControllerStartIsIdempotentAndStopWaits(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	runner := &blockingWatchRunner{started: make(chan struct{}, 1)}
	controller := NewWatchController(staticIdentityResolver{identity: identity},
		func() WatchRunner { return runner }, draftwatch.WatchOptions{FinalTimeout: watchTestTimeout})

	first, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	second, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	assert.True(t, first.Running)
	assert.Equal(t, first.StartedAt, second.StartedAt)

	select {
	case <-runner.started:
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not start")
	}
	assert.Equal(t, int32(1), runner.calls.Load())

	stopped, err := controller.Stop(t.Context(), identity.LeagueKey)
	require.NoError(t, err)
	assert.False(t, stopped.Running)
	require.NotNil(t, stopped.StoppedAt)
}

func TestWatchControllerRefreshRoutesToRunningWatchOnly(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	controller := NewWatchController(staticIdentityResolver{identity: identity},
		func() WatchRunner { return answeringWatchRunner{} }, draftwatch.WatchOptions{FinalTimeout: watchTestTimeout})

	_, routed, err := controller.Refresh(t.Context(), identity.LeagueKey)
	require.NoError(t, err)
	assert.False(t, routed, "no watch is running in this process")

	_, err = controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	outcome, routed, err := controller.Refresh(t.Context(), identity.LeagueKey)
	require.NoError(t, err)
	assert.True(t, routed)
	assert.Equal(t, answeredSyncVersion, outcome.Session.SyncVersion)

	_, err = controller.Stop(t.Context(), identity.LeagueKey)
	require.NoError(t, err)
	_, routed, err = controller.Refresh(t.Context(), identity.LeagueKey)
	require.NoError(t, err)
	assert.False(t, routed, "a stopped watch no longer owns refreshes")
}

func TestWatchControllerRefreshIsNotRoutedWhenWatchStopsFirst(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	runner := &blockingWatchRunner{started: make(chan struct{}, 1)}
	controller := NewWatchController(staticIdentityResolver{identity: identity},
		func() WatchRunner { return runner }, draftwatch.WatchOptions{FinalTimeout: watchTestTimeout})
	_, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	<-runner.started

	type refreshResult struct {
		routed bool
		err    error
	}
	result := make(chan refreshResult, 1)
	go func() {
		_, routed, err := controller.Refresh(t.Context(), identity.LeagueKey)
		result <- refreshResult{routed: routed, err: err}
	}()
	_, err = controller.Stop(t.Context(), identity.LeagueKey)
	require.NoError(t, err)

	select {
	case got := <-result:
		require.NoError(t, got.err)
		assert.False(t, got.routed, "the caller must fall back to a one-shot poll")
	case <-time.After(watchTestTimeout):
		t.Fatal("refresh kept waiting for a stopped watch")
	}
}

const answeredSyncVersion uint64 = 7

// answeringWatchRunner answers every refresh request until cancellation.
type answeringWatchRunner struct{}

func (answeringWatchRunner) Watch(ctx context.Context, _ draftwatch.Identity, options draftwatch.WatchOptions) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case request := <-options.Refresh:
			request.Reply <- draftwatch.Outcome{Session: draftwatch.Session{SyncVersion: answeredSyncVersion}}
		}
	}
}

type staticIdentityResolver struct{ identity draftwatch.Identity }

func (r staticIdentityResolver) ResolveIdentity(context.Context, string, int) (draftwatch.Identity, error) {
	return r.identity, nil
}

type blockingWatchRunner struct {
	calls   atomic.Int32
	started chan struct{}
}

func (r *blockingWatchRunner) Watch(ctx context.Context, _ draftwatch.Identity, _ draftwatch.WatchOptions) error {
	r.calls.Add(1)
	r.started <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

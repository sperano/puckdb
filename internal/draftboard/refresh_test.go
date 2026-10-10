package draftboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envDraftBoardTestPGURL = "PUCKDB_TEST_PG_URL"
	draftBoardTestDBMarker = "test"
	// refreshTestIdle keeps watches waiting so only refresh requests poll.
	refreshTestIdle = time.Hour
)

func TestRefreshRoutesThroughInProcessWatch(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	refresher := &fakeRefresher{err: errors.New("one-shot poll must not run while this process watches")}
	controller := NewWatchController(staticIdentityResolver{identity: identity},
		func() WatchRunner { return answeringWatchRunner{} }, draftwatch.WatchOptions{FinalTimeout: watchTestTimeout})
	service := NewService(&fakeDataSource{identity: identity}, nil, refresher, Options{Watch: controller})
	_, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	t.Cleanup(func() { _ = controller.Close(context.Background()) })

	session, _, err := service.Refresh(t.Context(), identity.LeagueKey, identity.Season)

	require.NoError(t, err)
	assert.Equal(t, answeredSyncVersion, session.SyncVersion)
	assert.Zero(t, refresher.calls.Load())
}

func TestRefreshReportsUnavailableWhileAnotherSyncHoldsLock(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	refresher := &fakeRefresher{err: fmt.Errorf("%w for %s", draftwatch.ErrSyncInProgress, identity.LeagueKey)}
	service := NewService(&fakeDataSource{identity: identity}, nil, refresher, Options{})

	_, _, err := service.Refresh(t.Context(), identity.LeagueKey, identity.Season)

	require.ErrorIs(t, err, ErrRefreshUnavailable)
	assert.Contains(t, err.Error(), identity.LeagueKey)
	assert.Contains(t, err.Error(), "another API process")
}

func TestRefreshPassesOtherFailuresThrough(t *testing.T) {
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	upstream := errors.New("yahoo unavailable")
	service := NewService(&fakeDataSource{identity: identity}, nil, &fakeRefresher{err: upstream}, Options{})

	_, _, err := service.Refresh(t.Context(), identity.LeagueKey, identity.Season)

	require.ErrorIs(t, err, upstream)
	assert.NotErrorIs(t, err, ErrRefreshUnavailable)
}

// TestRefreshWhileWatchHoldsPostgresLock covers both lock holders with the
// real session advisory lock: this process's watch answers the refresh, and a
// watch on another pool (a CLI or another API process) makes it unavailable.
func TestRefreshWhileWatchHoldsPostgresLock(t *testing.T) {
	pool := openDraftBoardTestDB(t)
	identity := uniqueRefreshIdentity(t, pool)
	source := newSignalingSource()
	runner := draftwatch.Runner{Pool: pool, Repository: draftwatch.NewRepository(pool), Source: source}
	controller := NewWatchController(staticIdentityResolver{identity: identity},
		func() WatchRunner { return runner }, idleRefreshWatchOptions())
	service := NewService(&fakeDataSource{identity: identity}, nil, runner, Options{Watch: controller})
	// Runs before pool.Close, which would wait forever for a watch's connection.
	t.Cleanup(func() { _ = controller.Close(context.Background()) })

	_, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	source.awaitPoll(t)
	session, _, err := service.Refresh(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err, "the in-process watch must answer instead of contending for its own lock")
	assert.Equal(t, uint64(2), session.SyncVersion)
	assert.Equal(t, int32(2), source.polls.Load())
	_, err = controller.Stop(t.Context(), identity.LeagueKey)
	require.NoError(t, err)

	stopExternal := startExternalWatch(t, identity)
	_, _, err = service.Refresh(t.Context(), identity.LeagueKey, identity.Season)
	require.ErrorIs(t, err, ErrRefreshUnavailable)
	status := startLosingWatch(t, controller, identity)
	assert.Contains(t, status.LastError, draftwatch.ErrSyncInProgress.Error())
	_, _, err = service.Refresh(t.Context(), identity.LeagueKey, identity.Season)
	require.ErrorIs(t, err, ErrRefreshUnavailable, "a watch that lost the lock must not swallow refreshes")
	stopExternal()

	_, _, err = service.Refresh(t.Context(), identity.LeagueKey, identity.Season)
	assert.NoError(t, err, "refresh works again once the external watch stops")
}

// startExternalWatch runs a watch on its own pool, as another process would,
// and returns a function that stops it.
func startExternalWatch(t *testing.T, identity draftwatch.Identity) func() {
	t.Helper()
	external := openDraftBoardTestDB(t)
	source := newSignalingSource()
	runner := draftwatch.Runner{Pool: external, Repository: draftwatch.NewRepository(external), Source: source}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Watch(ctx, identity, idleRefreshWatchOptions()) }()
	source.awaitPoll(t)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(watchTestTimeout):
				t.Error("external watch did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// startLosingWatch starts an in-process watch while another holder owns the
// lock and waits for it to give up.
func startLosingWatch(t *testing.T, controller *WatchController, identity draftwatch.Identity) WatchStatus {
	t.Helper()
	_, err := controller.Start(t.Context(), identity.LeagueKey, identity.Season)
	require.NoError(t, err)
	deadline := time.Now().Add(watchTestTimeout)
	for time.Now().Before(deadline) {
		if status, _ := controller.Status(identity.LeagueKey); !status.Running {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("in-process watch kept running without the lock")
	return WatchStatus{}
}

func idleRefreshWatchOptions() draftwatch.WatchOptions {
	return draftwatch.WatchOptions{Interval: refreshTestIdle, MaxBackoff: refreshTestIdle, FinalTimeout: watchTestTimeout}
}

var draftBoardMigrateOnce struct {
	sync.Once
	err error
}

func openDraftBoardTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envDraftBoardTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL draft board tests", envDraftBoardTestPGURL)
	}
	require.Contains(t, dbURL, draftBoardTestDBMarker,
		"%s must name a dedicated test database", envDraftBoardTestPGURL)
	draftBoardMigrateOnce.Do(func() { draftBoardMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, draftBoardMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// uniqueRefreshIdentity returns a league key no other test uses and removes
// its session rows afterwards.
func uniqueRefreshIdentity(t *testing.T, pool *pgxpool.Pool) draftwatch.Identity {
	t.Helper()
	const testGameKey = 998
	leagueID := int(time.Now().UnixNano()%999_999_999) + 1
	identity := draftwatch.Identity{LeagueKey: fmt.Sprintf("%d.l.%d", testGameKey, leagueID),
		Season: 2026, LeagueID: leagueID, GameKey: testGameKey}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM draft_sessions WHERE league_key=$1`, identity.LeagueKey)
	})
	return identity
}

type fakeRefresher struct {
	err   error
	calls atomic.Int32
}

func (f *fakeRefresher) SyncOnce(context.Context, draftwatch.Identity) (draftwatch.Session, draftsession.Report, error) {
	f.calls.Add(1)
	return draftwatch.Session{}, draftsession.Report{}, f.err
}

// signalingSource reports every Yahoo poll so tests can wait for one.
type signalingSource struct {
	polls  atomic.Int32
	polled chan struct{}
}

func newSignalingSource() *signalingSource {
	const pollSignalBuffer = 16
	return &signalingSource{polled: make(chan struct{}, pollSignalBuffer)}
}

func (s *signalingSource) Poll(context.Context, draftwatch.Identity) (draftwatch.PollResult, error) {
	s.polls.Add(1)
	select {
	case s.polled <- struct{}{}:
	default:
	}
	return draftwatch.PollResult{PolledAt: time.Now().UTC()}, nil
}

func (s *signalingSource) awaitPoll(t *testing.T) {
	t.Helper()
	select {
	case <-s.polled:
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not poll")
	}
}

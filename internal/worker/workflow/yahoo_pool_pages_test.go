package workflow

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	poolPageSize = resource.LeaguePlayersPageSize
	// poolTestPlayers is a pool of 63 full pages and a short one.
	poolTestPlayers = 63*poolPageSize + poolTestLastPage
	// poolTestBarrierWait bounds how long the fake holds a page while waiting
	// for the others it expects in flight at the same time.
	poolTestBarrierWait = 5 * time.Second
)

// fakePool serves a league pool of players through FetchYahooLeaguePlayersPage
// and records how the pages were requested. With hold > 0, the first hold
// pages after the first one each wait (up to poolTestBarrierWait) until hold
// pages are in flight together, so a walk that keeps fewer in flight shows a
// lower peak instead of passing by luck. An unavailable pool answers its
// first page as Yahoo does before the pool opens.
type fakePool struct {
	players     int
	hold        int
	failAt      map[int]bool
	unavailable bool

	mu         sync.Mutex
	requested  []int
	inFlight   int
	peak       int
	held       int
	release    chan struct{}
	firstAlone bool
	firstDone  bool
}

func newFakePool(players, hold int) *fakePool {
	return &fakePool{players: players, hold: hold, failAt: map[int]bool{}, release: make(chan struct{}), firstAlone: true}
}

func (p *fakePool) fetch(_ context.Context, input yahoo.FetchYahooLeaguePlayersPageInput) (yahoo.FetchYahooLeaguePlayersPageResult, error) {
	wait := p.enter(input.Start)
	if wait {
		select {
		case <-p.release:
		case <-time.After(poolTestBarrierWait):
		}
	}
	defer p.leave(input.Start)
	if p.unavailable && input.Start == 0 {
		return yahoo.FetchYahooLeaguePlayersPageResult{Unavailable: true}, nil
	}
	if p.failAt[input.Start] {
		return yahoo.FetchYahooLeaguePlayersPageResult{}, temporal.NewNonRetryableApplicationError("page failed", "test", nil)
	}
	players := min(max(p.players-input.Start, 0), poolPageSize)
	return yahoo.FetchYahooLeaguePlayersPageResult{Players: players, LeagueKey: poolTestLeagueKey, GameKey: poolTestGameKey}, nil
}

// enter records a request and says whether it must wait for the barrier.
func (p *fakePool) enter(start int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requested = append(p.requested, start)
	p.inFlight++
	p.peak = max(p.peak, p.inFlight)
	if start != 0 && !p.firstDone {
		p.firstAlone = false
	}
	if start == 0 || p.held >= p.hold {
		return false
	}
	p.held++
	if p.held == p.hold {
		close(p.release)
		return false
	}
	return true
}

func (p *fakePool) leave(start int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--
	if start == 0 {
		p.firstDone = true
	}
}

// fakePoolStats is what a fakePool observed, copied under its lock.
type fakePoolStats struct {
	requested  []int
	peak       int
	firstAlone bool
}

func (p *fakePool) stats() fakePoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fakePoolStats{requested: slices.Clone(p.requested), peak: p.peak, firstAlone: p.firstAlone}
}

func (s fakePoolStats) sortedRequests() []int {
	return slices.Sorted(slices.Values(s.requested))
}

// poolRun is one FetchYahooSeasonWorkflow run against a fake pool.
type poolRun struct {
	env    *testsuite.TestWorkflowEnvironment
	pool   *fakePool
	commit *yahoo.CommitYahooLeaguePlayerPoolInput
}

// runPoolFetch runs the season fetch with a pool plan that reports previous
// players and the fake pool's pages; configure may add expectations first.
func runPoolFetch(t *testing.T, pool *fakePool, previous int, configure func(*testsuite.TestWorkflowEnvironment)) poolRun {
	t.Helper()
	env := newPoolFetchEnv(t)
	run := poolRun{env: env, pool: pool}
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.PlanYahooLeaguePlayerPool, mock.Anything, mock.Anything).
		Return(yahoo.YahooLeaguePlayerPoolPlan{Refresh: true, PreviousPlayerCount: previous}, nil).Once()
	env.OnActivity(activities.FetchYahooLeaguePlayersPage, mock.Anything, mock.Anything).Return(pool.fetch)
	env.OnActivity(activities.CommitYahooLeaguePlayerPool, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input yahoo.CommitYahooLeaguePlayerPoolInput) error {
			run.commit = &input
			return nil
		}).Maybe()
	if configure != nil {
		configure(env)
	}
	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())
	require.True(t, env.IsWorkflowCompleted())
	return run
}

// pageStarts returns the starts of pages [0, pages).
func pageStarts(pages int) []int {
	starts := make([]int, pages)
	for i := range starts {
		starts[i] = i * poolPageSize
	}
	return starts
}

// The pages the previous snapshot predicts run up to the concurrency limit,
// after the first page alone, and need no tail walk when the pool kept its
// size.
func TestFetchYahooPoolPages_KnownPagesRunConcurrently(t *testing.T) {
	pool := newFakePool(poolTestPlayers, yahooPoolPageConcurrency)
	run := runPoolFetch(t, pool, poolTestPlayers, nil)

	require.NoError(t, run.env.GetWorkflowError())
	stats := pool.stats()
	assert.Equal(t, yahooPoolPageConcurrency, stats.peak, "pages in flight at once")
	assert.True(t, stats.firstAlone, "no page starts before the first one returns")
	expected := pageStarts(yahooPoolPages(poolTestPlayers))
	assert.Equal(t, expected, stats.sortedRequests(), "every page once, nothing past the short one")
	require.NotNil(t, run.commit)
	assert.Equal(t, expected, run.commit.Starts)
	assert.Equal(t, poolTestPlayers, run.commit.Players)
	assert.Empty(t, run.commit.Discarded)
}

// A pool that grew past the previous snapshot continues one page at a time
// and stops at the first short page.
func TestFetchYahooPoolPages_TailWalkStopsAtFirstShortPage(t *testing.T) {
	const previous, grown = 2*poolPageSize + poolTestLastPage, 4*poolPageSize + poolTestLastPage
	pool := newFakePool(grown, 0)
	run := runPoolFetch(t, pool, previous, nil)

	require.NoError(t, run.env.GetWorkflowError())
	assert.Equal(t, pageStarts(5), pool.stats().sortedRequests(), "the tail ends at the short page")
	require.NotNil(t, run.commit)
	assert.Equal(t, pageStarts(5), run.commit.Starts)
	assert.Equal(t, grown, run.commit.Players)
}

// A page past the pool's last one may fail without failing the download: a
// one-at-a-time walk would never have requested it.
func TestFetchYahooPoolPages_ShrunkPoolIgnoresPagesPastTheEnd(t *testing.T) {
	const shrunk = 2*poolPageSize + poolTestLastPage
	pool := newFakePool(shrunk, 0)
	failing := 4 * poolPageSize
	pool.failAt[failing] = true
	run := runPoolFetch(t, pool, poolTestPlayers, nil)

	require.NoError(t, run.env.GetWorkflowError())
	require.NotNil(t, run.commit)
	assert.Equal(t, pageStarts(3), run.commit.Starts)
	assert.Equal(t, shrunk, run.commit.Players)
	assert.NotContains(t, run.commit.Discarded, failing, "a failed page wrote nothing to delete")
	stats := pool.stats()
	for _, start := range run.commit.Discarded {
		assert.Greater(t, start, 2*poolPageSize)
		assert.Contains(t, stats.requested, start)
	}
	assert.Less(t, len(stats.requested), yahooPoolPages(poolTestPlayers), "pages past the end stop being started")
}

// A page that fails before the pool's last one fails the download.
func TestFetchYahooPoolPages_FailedKnownPageCommitsNothing(t *testing.T) {
	pool := newFakePool(poolTestPlayers, 0)
	pool.failAt[3*poolPageSize] = true
	run := runPoolFetch(t, pool, poolTestPlayers, nil)

	require.Error(t, run.env.GetWorkflowError())
	assert.Contains(t, run.env.GetWorkflowError().Error(), "fetch Yahoo player pool 2026/1001")
	assert.Nil(t, run.commit)
}

// The first page alone decides that the pool is unavailable.
func TestFetchYahooPoolPages_UnavailableFirstPageStopsTheDownload(t *testing.T) {
	pool := newFakePool(poolTestPlayers, 0)
	pool.unavailable = true
	run := runPoolFetch(t, pool, poolTestPlayers, nil)

	require.NoError(t, run.env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, run.env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"league 1001 player pool"}, result.UnavailableResources)
	assert.Equal(t, []int{0}, pool.stats().requested, "no other page is requested")
	assert.Nil(t, run.commit)
}

// A run that recorded the one-page-at-a-time walk keeps replaying it.
func TestFetchYahooPoolPages_LegacyVersionWalksOnePageAtATime(t *testing.T) {
	pool := newFakePool(poolTestPlayers, 0)
	run := runPoolFetch(t, pool, poolTestPlayers, func(env *testsuite.TestWorkflowEnvironment) {
		env.OnGetVersion(yahooPoolParallelPagesChange, workflow.DefaultVersion, yahooPoolParallelPagesVersion).
			Return(workflow.DefaultVersion)
	})

	require.NoError(t, run.env.GetWorkflowError())
	stats := pool.stats()
	assert.Equal(t, 1, stats.peak)
	assert.Equal(t, stats.sortedRequests(), stats.requested, "pages in order")
	require.NotNil(t, run.commit)
	assert.Equal(t, pageStarts(yahooPoolPages(poolTestPlayers)), run.commit.Starts)
}

// Whatever the previous snapshot predicted, the committed pool is the one the
// one-page-at-a-time walk (no previous snapshot) commits.
func TestFetchYahooPoolPages_PoolEqualsSequentialWalk(t *testing.T) {
	sizes := []int{0, poolTestLastPage, 2 * poolPageSize, 4*poolPageSize + poolTestLastPage, poolTestPlayers}
	previous := []int{1, 2*poolPageSize + poolTestLastPage, 2 * poolPageSize, poolTestPlayers}
	for _, size := range sizes {
		sequential := runPoolFetch(t, newFakePool(size, 0), 0, nil)
		require.NoError(t, sequential.env.GetWorkflowError())
		require.NotNil(t, sequential.commit)
		for _, prev := range previous {
			t.Run(fmt.Sprintf("pool %d previous %d", size, prev), func(t *testing.T) {
				run := runPoolFetch(t, newFakePool(size, 0), prev, nil)
				require.NoError(t, run.env.GetWorkflowError())
				require.NotNil(t, run.commit)
				got, want := *run.commit, *sequential.commit
				got.Discarded = nil
				got.DownloadID, got.FetchedAt = want.DownloadID, want.FetchedAt
				assert.Equal(t, want, got)
			})
		}
	}
}

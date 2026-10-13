package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
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
	poolLeagueB = 1002
	poolLeagueC = 1003
	// poolBarrierTimeout bounds how long a first page waits for the other
	// leagues' first pages; a sequential fetch never gets there.
	poolBarrierTimeout = 5 * time.Second
	poolPageFailure    = "PoolPageFailure"
)

// poolLeagues is the three-league season the concurrency tests fetch.
var poolLeagues = []int{preseasonTestLeagueID, poolLeagueB, poolLeagueC}

// poolLeagueRun records the pool activities of a multi-league fetch in the
// order they started, and how many leagues were between plan and commit.
type poolLeagueRun struct {
	mu          sync.Mutex
	events      []string
	inFlight    int
	maxInFlight int
	commits     map[int]yahoo.CommitYahooLeaguePlayerPoolInput
	// firstPage, when set, runs inside each league's first page download.
	firstPage func(leagueID int) error
	// failLeague and failStart name the one page that fails.
	failLeague, failStart int
}

func newPoolLeagueRun() *poolLeagueRun {
	return &poolLeagueRun{commits: map[int]yahoo.CommitYahooLeaguePlayerPoolInput{}, failLeague: -1}
}

func (r *poolLeagueRun) record(event string, delta int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.inFlight += delta
	r.maxInFlight = max(r.maxInFlight, r.inFlight)
}

func (r *poolLeagueRun) plan(_ context.Context, input yahoo.PlanYahooLeaguePlayerPoolInput) (yahoo.YahooLeaguePlayerPoolPlan, error) {
	r.record(fmt.Sprintf("plan %d", input.LeagueID), 1)
	return yahoo.YahooLeaguePlayerPoolPlan{
		Refresh: true, PreviousPlayerCount: resource.LeaguePlayersPageSize, Reason: "pool is stale",
	}, nil
}

// page serves a full first page and a short second one.
func (r *poolLeagueRun) page(_ context.Context, input yahoo.FetchYahooLeaguePlayersPageInput) (yahoo.FetchYahooLeaguePlayersPageResult, error) {
	r.record(poolPageEvent(input.LeagueID, input.Start), 0)
	if input.Start == 0 && r.firstPage != nil {
		if err := r.firstPage(input.LeagueID); err != nil {
			return yahoo.FetchYahooLeaguePlayersPageResult{}, temporal.NewNonRetryableApplicationError(err.Error(), poolPageFailure, err)
		}
	}
	if input.LeagueID == r.failLeague && input.Start == r.failStart {
		return yahoo.FetchYahooLeaguePlayersPageResult{}, temporal.NewNonRetryableApplicationError("page refused", poolPageFailure, nil)
	}
	players := resource.LeaguePlayersPageSize
	if input.Start > 0 {
		players = poolTestLastPage
	}
	return yahoo.FetchYahooLeaguePlayersPageResult{Players: players, LeagueKey: poolTestLeagueKey, GameKey: poolTestGameKey}, nil
}

func (r *poolLeagueRun) commit(_ context.Context, input yahoo.CommitYahooLeaguePlayerPoolInput) error {
	r.record(fmt.Sprintf("commit %d", input.LeagueID), -1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits[input.LeagueID] = input
	return nil
}

func poolPageEvent(leagueID, start int) string {
	return fmt.Sprintf("page %d@%d", leagueID, start)
}

// newPoolBarrier returns a first-page hook that blocks until parties leagues
// are downloading their first page at the same time.
func newPoolBarrier(parties int) func(int) error {
	var arrived sync.WaitGroup
	arrived.Add(parties)
	all := make(chan struct{})
	go func() { arrived.Wait(); close(all) }()
	return func(leagueID int) error {
		arrived.Done()
		select {
		case <-all:
			return nil
		case <-time.After(poolBarrierTimeout):
			return fmt.Errorf("league %d's first page never overlapped %d leagues", leagueID, parties)
		}
	}
}

func newPoolLeaguesEnv(t *testing.T, run *poolLeagueRun) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	mockProgressSaves(env)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchLeague, mock.Anything, mock.Anything, mock.Anything).
		Return(yahoo.FetchLeagueResult{}, nil)
	mockAnyLeagueData(env)
	env.OnActivity(activities.PlanYahooLeaguePlayerPool, mock.Anything, mock.Anything).Return(run.plan)
	env.OnActivity(activities.FetchYahooLeaguePlayersPage, mock.Anything, mock.Anything).Return(run.page)
	env.OnActivity(activities.CommitYahooLeaguePlayerPool, mock.Anything, mock.Anything).Return(run.commit)
	return env
}

func poolLeaguesInput() YahooSeasonWorkflowInput {
	leagues := make([]config.League, len(poolLeagues))
	for i, id := range poolLeagues {
		leagues[i] = config.League{LeagueID: id}
	}
	return YahooSeasonWorkflowInput{StartYear: preseasonTestYear, Season: config.Season{Leagues: leagues}}
}

func assertPoolCommitted(t *testing.T, run *poolLeagueRun, leagueID int) {
	t.Helper()
	commit, ok := run.commits[leagueID]
	require.True(t, ok, "league %d committed no manifest", leagueID)
	pageSize := resource.LeaguePlayersPageSize
	assert.Equal(t, []int{0, pageSize}, commit.Starts)
	assert.Equal(t, pageSize+poolTestLastPage, commit.Players)
}

func TestFetchYahooSeasonWorkflow_PlayerPoolLeaguesInterleave(t *testing.T) {
	run := newPoolLeagueRun()
	run.firstPage = newPoolBarrier(len(poolLeagues))
	env := newPoolLeaguesEnv(t, run)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, poolLeaguesInput())

	require.NoError(t, env.GetWorkflowError())
	for _, leagueID := range poolLeagues {
		assertPoolCommitted(t, run, leagueID)
	}
	lastFirstPage := 0
	for _, leagueID := range poolLeagues {
		lastFirstPage = max(lastFirstPage, slices.Index(run.events, poolPageEvent(leagueID, 0)))
	}
	for _, leagueID := range poolLeagues {
		assert.Greater(t, slices.Index(run.events, poolPageEvent(leagueID, resource.LeaguePlayersPageSize)), lastFirstPage,
			"every league's first page starts before any league's second page: %v", run.events)
	}
}

func TestFetchYahooSeasonWorkflow_PlayerPoolLeagueFailureKeepsOtherManifests(t *testing.T) {
	run := newPoolLeagueRun()
	run.failLeague, run.failStart = poolLeagueB, resource.LeaguePlayersPageSize
	env := newPoolLeaguesEnv(t, run)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, poolLeaguesInput())

	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("fetch Yahoo player pool %d/%d", preseasonTestYear, poolLeagueB))
	for _, leagueID := range []int{preseasonTestLeagueID, poolLeagueC} {
		assert.NotContains(t, err.Error(), fmt.Sprintf("fetch Yahoo player pool %d/%d", preseasonTestYear, leagueID))
		assertPoolCommitted(t, run, leagueID)
	}
	assert.NotContains(t, run.commits, poolLeagueB, "the failed league must not commit a partial snapshot")
}

func TestFetchYahooSeasonWorkflow_PlayerPoolLeagueConcurrencyIsBounded(t *testing.T) {
	const bound = 2
	setViperInt(t, config.FlagYahooPoolLeagueConcurrency, bound)
	run := newPoolLeagueRun()
	barrier := newPoolBarrier(bound)
	run.firstPage = func(leagueID int) error {
		if leagueID == poolLeagueC {
			return nil // starts only once one of the first two has committed
		}
		return barrier(leagueID)
	}
	env := newPoolLeaguesEnv(t, run)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, poolLeaguesInput())

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, bound, run.maxInFlight, "leagues between plan and commit: %v", run.events)
	assertPoolCommitted(t, run, poolLeagueC)
}

// A history recorded before the change replays the one-league-at-a-time loop.
func TestFetchYahooSeasonWorkflow_PlayerPoolPreVersionHistoryIsSequential(t *testing.T) {
	run := newPoolLeagueRun()
	env := newPoolLeaguesEnv(t, run)
	env.OnGetVersion(yahooPoolLeaguesChangeID, workflow.DefaultVersion, yahooPoolLeaguesConcurrentVersion).
		Return(workflow.DefaultVersion)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, poolLeaguesInput())

	require.NoError(t, env.GetWorkflowError())
	var want []string
	for _, leagueID := range poolLeagues {
		want = append(want, fmt.Sprintf("plan %d", leagueID), poolPageEvent(leagueID, 0),
			poolPageEvent(leagueID, resource.LeaguePlayersPageSize), fmt.Sprintf("commit %d", leagueID))
	}
	assert.Equal(t, want, run.events)
}

func TestCollectYahooPoolOutcomes_JoinsFailuresInConfigurationOrder(t *testing.T) {
	t.Parallel()
	leagues := poolLeaguesInput().Season.Leagues
	errB, errC := errors.New("b refused"), errors.New("c refused")
	outcomes := []yahooPoolOutcome{{unavailable: true}, {err: errB}, {err: errC}}

	unavailable, err := collectYahooPoolOutcomes(preseasonTestYear, leagues, outcomes)

	assert.Equal(t, []string{fmt.Sprintf("league %d player pool", preseasonTestLeagueID)}, unavailable)
	require.ErrorIs(t, err, errB)
	require.ErrorIs(t, err, errC)
	assert.Equal(t, fmt.Sprintf("fetch Yahoo player pool %d/%d: b refused\nfetch Yahoo player pool %d/%d: c refused",
		preseasonTestYear, poolLeagueB, preseasonTestYear, poolLeagueC), err.Error())
}

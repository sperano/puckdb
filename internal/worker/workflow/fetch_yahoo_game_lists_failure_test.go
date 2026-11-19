package workflow

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// gameListOtherDueSeason is a second season whose list is due.
const gameListOtherDueSeason = 2025

// Non-retryable, so each failing activity runs once.
var (
	errGameListPage   = temporal.NewNonRetryableApplicationError("page failed", "test", nil)
	errGameListCommit = temporal.NewNonRetryableApplicationError("commit failed", "test", nil)
)

// twoDueSeasonFakes: two due seasons, each downloaded in two pages (one
// full, one short).
func twoDueSeasonFakes() *gameListFakes {
	twoPages := []int{resource.GamePlayersPageSize, gameListShortPagePlayers}
	return &gameListFakes{
		seasons: []int{gameListOtherDueSeason, gameListCurrentSeason},
		plans: map[int]yahoo.YahooGamePlayerListPlan{
			gameListOtherDueSeason: {Refresh: true, GameKey: gameListCurrentGameKey - 1},
			gameListCurrentSeason:  {Refresh: true, GameKey: gameListCurrentGameKey},
		},
		pages: map[int][]int{gameListOtherDueSeason: twoPages, gameListCurrentSeason: twoPages},
	}
}

func TestFetchYahooPlayersWorkflow_PageFailureDiscardsSeasonDownload(t *testing.T) {
	f := twoDueSeasonFakes()
	f.pageErr = map[int]map[int]error{gameListCurrentSeason: {resource.GamePlayersPageSize: errGameListPage}}

	env, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "fetch Yahoo game player list 2026")
	require.ErrorContains(t, err, errGameListPage.Error())
	require.Len(t, f.commits, 1, "the other season is still committed")
	assert.Equal(t, gameListOtherDueSeason, f.commits[0].Season)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.starts[gameListCurrentSeason],
		"no page is requested after the failed one")
	require.Len(t, f.discards, 1)
	assert.Equal(t, yahoo.DiscardYahooGamePlayerListInput{
		Season: gameListCurrentSeason, DownloadID: f.discards[0].DownloadID,
		Starts: []int{0, resource.GamePlayersPageSize},
	}, f.discards[0], "the failed page is discarded with the stored one")
	assert.Positive(t, f.discards[0].DownloadID)
	assert.Zero(t, queryProgress(t, env).Groups[GroupFetchYahooPlayers].CompletedAt)
}

func TestFetchYahooPlayersWorkflow_CommitFailureDiscardsSeasonDownload(t *testing.T) {
	f := twoDueSeasonFakes()
	f.commitErr = map[int]error{gameListCurrentSeason: errGameListCommit}

	env, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "fetch Yahoo game player list 2026")
	require.ErrorContains(t, err, errGameListCommit.Error())
	require.Len(t, f.commits, 1, "the other season is still committed")
	assert.Equal(t, gameListOtherDueSeason, f.commits[0].Season)
	require.Len(t, f.discards, 1)
	assert.Equal(t, gameListCurrentSeason, f.discards[0].Season)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.discards[0].Starts)
	group := queryProgress(t, env).Groups[GroupFetchYahooPlayers]
	assert.Zero(t, group.CompletedAt, "a failed run leaves the group open")
	assert.Equal(t, 1+2+1, group.Bars[1].Total)
	assert.Equal(t, 1+2, group.Bars[1].Current, "the failed commit does not advance the bar")
}

func TestFetchYahooPlayersWorkflow_SeasonConcurrencyLimit(t *testing.T) {
	setViperInt(t, config.FlagYahooGamePlayerListConcurrency, 1)
	seasons := []int{2023, 2024, 2025, 2026}
	f := &gameListFakes{seasons: seasons, plans: map[int]yahoo.YahooGamePlayerListPlan{}, pages: map[int][]int{}}
	for _, season := range seasons {
		f.plans[season] = yahoo.YahooGamePlayerListPlan{Refresh: true, GameKey: gameListCurrentGameKey}
		f.pages[season] = []int{gameListShortPagePlayers}
	}

	_, err := runGameListWorkflow(t, f, nil)

	require.NoError(t, err)
	require.Len(t, f.commits, len(seasons))
	assert.Equal(t, 1, f.maxInFlight, "one season at a time")
	for i, commit := range f.commits {
		assert.Equal(t, seasons[i], commit.Season, "seasons run in order")
	}
}

// gameListOutcomesWorkflow returns fetchYahooGamePlayerListSeasons'
// outcomes, so a test can check what each season reports; concurrency 0
// runs every season at once.
func gameListOutcomesWorkflow(ctx workflow.Context, seasons []int, concurrency int) ([]gameListOutcome, error) {
	tracker, err := shared.InitTracker(ctx, newFetchYahooPlayersProgressReport())
	if err != nil {
		return nil, err
	}
	for range seasons {
		tracker.AddBar(GroupFetchYahooPlayers, shared.ProgressBar{Total: gameListPlanUnits})
	}
	if concurrency == 0 {
		concurrency = len(seasons)
	}
	outcomes, _ := fetchYahooGamePlayerListSeasons(ctx, tracker, seasons, concurrency)
	return outcomes, nil
}

func TestFetchYahooGamePlayerListSeasons_FailedSeasonsReportFailed(t *testing.T) {
	const (
		planFailSeason   = 2021
		pageFailSeason   = 2022
		commitFailSeason = 2023
	)
	f := threeSeasonFakes()
	f.seasons = []int{planFailSeason, pageFailSeason, commitFailSeason, gameListKeptSeason, gameListUnavailableSeason, gameListCurrentSeason}
	due := yahoo.YahooGamePlayerListPlan{Refresh: true, GameKey: gameListCurrentGameKey}
	f.plans[pageFailSeason], f.plans[commitFailSeason] = due, due
	f.pages[commitFailSeason] = []int{gameListShortPagePlayers}
	f.planErr = map[int]error{planFailSeason: errGameListPlan}
	f.pageErr = map[int]map[int]error{pageFailSeason: {0: errGameListPage}}
	f.commitErr = map[int]error{commitFailSeason: errGameListCommit}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(gameListOutcomesWorkflow)
	f.register(env)

	env.ExecuteWorkflow(gameListOutcomesWorkflow, f.seasons, 0)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var outcomes []gameListOutcome
	require.NoError(t, env.GetWorkflowResult(&outcomes))
	assert.Equal(t, []gameListOutcome{gameListFailed, gameListFailed, gameListFailed,
		gameListKept, gameListUnavailable, gameListDownloaded}, outcomes)
}

// gameListCancelAfter is when the cancellation test cancels the run: after
// the first page, while the second (blocked for gameListBlockedPage) is in
// flight.
const (
	gameListCancelAfter = time.Minute
	gameListBlockedPage = time.Hour
)

// blockSecondPages makes every season's second page take
// gameListBlockedPage, so a cancellation at gameListCancelAfter finds it in
// flight.
func blockSecondPages(env *testsuite.TestWorkflowEnvironment) {
	var act *yahoo.FetchActivities
	secondPage := mock.MatchedBy(func(in yahoo.FetchYahooGamePlayersPageInput) bool {
		return in.Start == resource.GamePlayersPageSize
	})
	env.OnActivity(act.FetchYahooGamePlayersPage, mock.Anything, secondPage).After(gameListBlockedPage).
		Return(yahoo.FetchYahooGamePlayersPageResult{Players: gameListShortPagePlayers}, nil)
}

func TestFetchYahooPlayersWorkflow_CancelledDownloadIsDiscarded(t *testing.T) {
	f := &gameListFakes{
		seasons: []int{gameListCurrentSeason},
		plans:   map[int]yahoo.YahooGamePlayerListPlan{gameListCurrentSeason: {Refresh: true, GameKey: gameListCurrentGameKey}},
		pages:   map[int][]int{gameListCurrentSeason: {resource.GamePlayersPageSize, gameListShortPagePlayers}},
	}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	blockSecondPages(env)
	f.register(env)
	env.RegisterDelayedCallback(env.CancelWorkflow, gameListCancelAfter)

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, nil)

	require.True(t, env.IsWorkflowCompleted())
	var canceled *temporal.CanceledError
	require.ErrorAs(t, env.GetWorkflowError(), &canceled)
	assert.Empty(t, f.commits)
	require.Len(t, f.discards, 1, "the discard runs after the cancellation")
	assert.Equal(t, gameListCurrentSeason, f.discards[0].Season)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.discards[0].Starts,
		"the in-flight page is discarded with the stored one")
}

func TestFetchYahooGamePlayerListSeasons_CancelledRunReportsUnstartedSeasonsNotRun(t *testing.T) {
	f := twoDueSeasonFakes()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(gameListOutcomesWorkflow)
	blockSecondPages(env)
	f.register(env)
	env.RegisterDelayedCallback(env.CancelWorkflow, gameListCancelAfter)

	env.ExecuteWorkflow(gameListOutcomesWorkflow, f.seasons, 1)

	require.True(t, env.IsWorkflowCompleted())
	var outcomes []gameListOutcome
	require.NoError(t, env.GetWorkflowResult(&outcomes))
	assert.Equal(t, []gameListOutcome{gameListFailed, gameListNotRun}, outcomes,
		"the cancelled season failed, the one waiting for a slot never ran")
	assert.NotContains(t, f.starts, gameListCurrentSeason, "the unstarted season requested no page")
}

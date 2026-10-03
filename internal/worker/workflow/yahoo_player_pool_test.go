package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	poolTestLeagueKey   = "465.l.1001"
	poolTestGameKey     = 465
	poolTestLastPage    = 3
	poolTestUpToDateWhy = "pool downloaded 1h0m0s ago"
)

// expectPoolUpToDate makes the league's pool plan say no download is due.
func expectPoolUpToDate(env *testsuite.TestWorkflowEnvironment, leagueID int) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.PlanYahooLeaguePlayerPool, mock.Anything,
		yahoo.PlanYahooLeaguePlayerPoolInput{Season: preseasonTestYear, LeagueID: leagueID}).
		Return(yahoo.YahooLeaguePlayerPoolPlan{Reason: poolTestUpToDateWhy}, nil).Once()
}

func singleLeagueYahooInput() YahooSeasonWorkflowInput {
	return YahooSeasonWorkflowInput{
		StartYear: preseasonTestYear,
		Season:    config.Season{Leagues: []config.League{{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1}}}},
	}
}

func newPoolFetchEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	// Progress is saved after every page; a failing save would retry on a
	// timer and move the clock that poolPageAt matches download IDs against.
	mockProgressSaves(env)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchLeague, mock.Anything, preseasonTestYear, preseasonTestLeagueID).
		Return(yahoo.FetchLeagueResult{}, nil)
	env.OnActivity(activities.FetchTeams, mock.Anything, mock.Anything).Return(nil)
	mockAnyLeagueData(env)
	return env
}

func expectPoolRefresh(env *testsuite.TestWorkflowEnvironment) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.PlanYahooLeaguePlayerPool, mock.Anything,
		yahoo.PlanYahooLeaguePlayerPoolInput{Season: preseasonTestYear, LeagueID: preseasonTestLeagueID}).
		Return(yahoo.YahooLeaguePlayerPoolPlan{Refresh: true, Reason: "pool never downloaded"}, nil).Once()
}

// poolPageAt matches the page at start of the download started at the test
// workflow clock's current time.
func poolPageAt(env *testsuite.TestWorkflowEnvironment, start int) any {
	return mock.MatchedBy(func(input yahoo.FetchYahooLeaguePlayersPageInput) bool {
		return input.Season == preseasonTestYear && input.LeagueID == preseasonTestLeagueID &&
			input.Start == start && input.DownloadID == env.Now().UnixMilli()
	})
}

func expectPoolPage(env *testsuite.TestWorkflowEnvironment, start, players int) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchYahooLeaguePlayersPage, mock.Anything, poolPageAt(env, start)).
		Return(yahoo.FetchYahooLeaguePlayersPageResult{
			Players: players, LeagueKey: poolTestLeagueKey, GameKey: poolTestGameKey,
		}, nil).Once()
}

func TestFetchYahooSeasonWorkflow_PlayerPoolPagesUntilShortPage(t *testing.T) {
	env := newPoolFetchEnv(t)
	expectPoolRefresh(env)
	pageSize := resource.LeaguePlayersPageSize
	expectPoolPage(env, 0, pageSize)
	expectPoolPage(env, pageSize, pageSize)
	expectPoolPage(env, 2*pageSize, poolTestLastPage)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.CommitYahooLeaguePlayerPool, mock.Anything,
		mock.MatchedBy(func(input yahoo.CommitYahooLeaguePlayerPoolInput) bool {
			return input.LeagueKey == poolTestLeagueKey && input.GameKey == poolTestGameKey &&
				assert.ObjectsAreEqual([]int{0, pageSize, 2 * pageSize}, input.Starts) &&
				input.Players == 2*pageSize+poolTestLastPage && !input.FetchedAt.IsZero() &&
				input.DownloadID == input.FetchedAt.UnixMilli()
		})).Return(nil).Once()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// A full last page is followed by an empty one; the empty page still ends
// the download and is part of the snapshot.
func TestFetchYahooSeasonWorkflow_PlayerPoolStopsOnEmptyPage(t *testing.T) {
	env := newPoolFetchEnv(t)
	expectPoolRefresh(env)
	pageSize := resource.LeaguePlayersPageSize
	expectPoolPage(env, 0, pageSize)
	expectPoolPage(env, pageSize, 0)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.CommitYahooLeaguePlayerPool, mock.Anything,
		mock.MatchedBy(func(input yahoo.CommitYahooLeaguePlayerPoolInput) bool {
			return assert.ObjectsAreEqual([]int{0, pageSize}, input.Starts) && input.Players == pageSize
		})).Return(nil).Once()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestFetchYahooSeasonWorkflow_PlayerPoolPageCap(t *testing.T) {
	env := newPoolFetchEnv(t)
	expectPoolRefresh(env)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchYahooLeaguePlayersPage, mock.Anything, mock.Anything).
		Return(yahoo.FetchYahooLeaguePlayersPageResult{Players: resource.LeaguePlayersPageSize}, nil).
		Times(yahoo.MaxLeaguePlayerPoolPages)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "pool has more than 200 pages")
	env.AssertNotCalled(t, "CommitYahooLeaguePlayerPool", mock.Anything, mock.Anything)
}

// A failed page must not commit a partial snapshot.
func TestFetchYahooSeasonWorkflow_PlayerPoolPageFailureCommitsNothing(t *testing.T) {
	env := newPoolFetchEnv(t)
	expectPoolRefresh(env)
	expectPoolPage(env, 0, resource.LeaguePlayersPageSize)
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchYahooLeaguePlayersPage, mock.Anything, poolPageAt(env, resource.LeaguePlayersPageSize)).
		Return(yahoo.FetchYahooLeaguePlayersPageResult{}, assert.AnError)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "fetch Yahoo player pool 2026/1001")
	env.AssertNotCalled(t, "CommitYahooLeaguePlayerPool", mock.Anything, mock.Anything)
}

func TestFetchYahooSeasonWorkflow_PlayerPoolUpToDateSkipsDownload(t *testing.T) {
	env := newPoolFetchEnv(t)
	expectPoolUpToDate(env, preseasonTestLeagueID)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "FetchYahooLeaguePlayersPage", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CommitYahooLeaguePlayerPool", mock.Anything, mock.Anything)
}

func TestImportYahooSeasonWorkflow_PlayerPoolImportFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	var activities *yahoo.ImportActivities
	env.OnActivity(activities.ImportYahooLeague, mock.Anything, mock.Anything).Return(yahoo.ImportYahooLeagueResult{}, nil)
	env.OnActivity(activities.ImportYahooTeams, mock.Anything, mock.Anything).Return(yahoo.ImportYahooTeamsResult{}, nil)
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil)
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeaguePlayersResult{}, assert.AnError)

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, singleLeagueYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "import Yahoo player pool 2026/1001")
}

package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const preseasonSecondLeagueID = 1002

func twoLeagueYahooInput() YahooSeasonWorkflowInput {
	return YahooSeasonWorkflowInput{
		StartYear: preseasonTestYear,
		Season: config.Season{Leagues: []config.League{
			{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2}},
			{LeagueID: preseasonSecondLeagueID, TeamIDs: []int{3}},
		}},
	}
}

func expectedTwoLeagueTeams(input yahoo.FetchTeamsInput) bool {
	return input.StartSeason == preseasonTestYear && assert.ObjectsAreEqual(
		[]yahoo.TeamInfo{
			{LeagueID: preseasonTestLeagueID, TeamID: 1},
			{LeagueID: preseasonTestLeagueID, TeamID: 2},
			{LeagueID: preseasonSecondLeagueID, TeamID: 3},
		},
		input.Teams,
	)
}

func TestFetchYahooSeasonWorkflow_TwoConfiguredLeagues(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)

	for _, leagueID := range []int{preseasonTestLeagueID, preseasonSecondLeagueID} {
		expectLeagueFetch(env, preseasonTestYear, leagueID, unknownEndWeek)
	}
	// One activity per team download.
	expectTeamFetches(env, preseasonTestLeagueID, 1, 2)
	expectTeamFetches(env, preseasonSecondLeagueID, 3)
	expectLeagueData(env, leagueDataMock{leagueID: preseasonTestLeagueID, lastWeek: 2})
	// Week 1 rejected: preseason, no matchups yet.
	expectLeagueData(env, leagueDataMock{leagueID: preseasonSecondLeagueID, lastWeek: 0, noDrafts: true})
	expectPoolUpToDate(env, preseasonTestLeagueID)
	expectPoolUpToDate(env, preseasonSecondLeagueID)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, twoLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"league 1002 draft results", "league 1002 matchups"}, result.UnavailableResources)
	env.AssertExpectations(t)
}

func TestImportYahooSeasonWorkflow_TwoConfiguredLeagues(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	var activities *yahoo.ImportActivities

	for _, leagueID := range []int{preseasonTestLeagueID, preseasonSecondLeagueID} {
		env.OnActivity(activities.ImportYahooLeague, mock.Anything,
			yahoo.ImportYahooLeagueInput{Season: preseasonTestYear, LeagueID: leagueID}).
			Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	}
	env.OnActivity(activities.ImportYahooTeams, mock.Anything, mock.MatchedBy(func(input yahoo.ImportYahooTeamsInput) bool {
		return expectedTwoLeagueTeams(yahoo.FetchTeamsInput{StartSeason: input.Season, Teams: input.Teams})
	})).Return(yahoo.ImportYahooTeamsResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything,
		yahoo.ImportYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonTestLeagueID}).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything,
		yahoo.ImportYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.ImportYahooLeagueDataResult{UnavailableResources: []string{"transactions"}}, nil).Once()
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything,
		yahoo.ImportYahooLeaguePlayersInput{Season: preseasonTestYear, LeagueID: preseasonTestLeagueID}).
		Return(yahoo.ImportYahooLeaguePlayersResult{Players: 1}, nil).Once()
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything,
		yahoo.ImportYahooLeaguePlayersInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.ImportYahooLeaguePlayersResult{Unavailable: true}, nil).Once()

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, twoLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"league 1002 transactions", "league 1002 player pool"}, result.UnavailableResources)
	env.AssertExpectations(t)
}

// singleLeagueFixedUnits is the single test league's units outside matchups
// and pool pages: metadata, its one team, transactions, draft results, the
// pool plan, and the pool commit.
const singleLeagueFixedUnits = 6

// TestFetchYahooSeasonWorkflow_LeagueTotalAdjusts runs one league whose
// matchup weeks and pool pages differ from the estimates and checks the bar
// never overruns its Total and ends exactly complete at the calls made.
func TestFetchYahooSeasonWorkflow_LeagueTotalAdjusts(t *testing.T) {
	pageSize := resource.LeaguePlayersPageSize
	tests := []struct {
		name            string
		endWeek         int // reported by the league settings
		lastWeek        int // last week Yahoo serves
		previousPlayers int // last snapshot's size
		pagePlayers     []int
		wantCalls       int // matchup calls + pool pages actually made
	}{
		{"matchups stop before end_week", 25, 2, pageSize - 1, []int{5}, 3 + 1},
		{"matchups run past end_week", 2, 4, pageSize - 1, []int{5}, 5 + 1},
		{"matchups reach the loop bound", unknownEndWeek, yahoo.MaxMatchupWeeks, pageSize - 1, []int{5},
			yahoo.MaxMatchupWeeks + 1},
		{"pool shorter than estimate", 3, 3, 3*pageSize + 5, []int{pageSize, 5}, 4 + 2},
		{"pool longer than estimate", 3, 3, 5, []int{pageSize, pageSize, 5}, 4 + 3},
		{"pool without previous snapshot", 3, 3, 0, []int{pageSize, 0}, 4 + 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(FetchYahooSeasonWorkflow)
			saves := recordBarSaves(env)
			expectLeagueFetch(env, preseasonTestYear, preseasonTestLeagueID, tt.endWeek)
			expectTeamFetches(env, preseasonTestLeagueID, 1)
			expectLeagueData(env, leagueDataMock{leagueID: preseasonTestLeagueID, lastWeek: tt.lastWeek})
			expectPoolPlan(env, preseasonTestLeagueID, tt.previousPlayers)
			expectPoolDownload(env, tt.pagePlayers)

			env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
			want := singleLeagueFixedUnits + tt.wantCalls
			history := barHistory(*saves, 0)
			for _, point := range history {
				assert.LessOrEqual(t, point.Current, point.Total, "bar overran its Total: %v", history)
			}
			assert.Equal(t, barPoint{want, want}, history[len(history)-1])
			assert.Equal(t, want, countAdvances(history), "one advance per download")
			var result YahooSeasonSyncResult
			require.NoError(t, env.GetWorkflowResult(&result))
			assert.Equal(t, []int{want}, result.LeagueTotals)
		})
	}
}

// countAdvances counts the states whose Current moved up by one.
func countAdvances(history []barPoint) int {
	advances := 0
	for i := 1; i < len(history); i++ {
		if history[i].Current == history[i-1].Current+1 {
			advances++
		}
	}
	return advances
}

// A failed download leaves the league's bar incomplete: the estimate is not
// shrunk to the calls made so far.
func TestFetchYahooSeasonWorkflow_FailedMatchupWeekLeavesBarIncomplete(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	saves := recordBarSaves(env)
	expectLeagueFetch(env, preseasonTestYear, preseasonTestLeagueID, unknownEndWeek)
	expectTeamFetches(env, preseasonTestLeagueID, 1)
	var activities *yahoo.FetchActivities
	input := yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonTestLeagueID}
	env.OnActivity(activities.FetchYahooTransactions, mock.Anything, input).
		Return(yahoo.FetchYahooLeagueResourceResult{}, nil).Once()
	env.OnActivity(activities.FetchYahooDraftResults, mock.Anything, input).
		Return(yahoo.FetchYahooLeagueResourceResult{}, nil).Once()
	env.OnActivity(activities.FetchYahooMatchupWeek, mock.Anything, mock.Anything).
		Return(yahoo.FetchYahooMatchupWeekResult{}, temporal.NewNonRetryableApplicationError("boom", "test", nil)).Once()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, singleLeagueYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "fetch yahoo league data 1001")
	history := barHistory(*saves, 0)
	last := history[len(history)-1]
	assert.Equal(t, 4, last.Current, "metadata, team, transactions, draft results")
	assert.Equal(t, yahooLeagueUnits(singleLeagueYahooInput().Season.Leagues[0], seasonSyncFetch), last.Total)
}

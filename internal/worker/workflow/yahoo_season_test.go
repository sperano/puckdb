package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	var activities *yahoo.FetchActivities

	for _, leagueID := range []int{preseasonTestLeagueID, preseasonSecondLeagueID} {
		env.OnActivity(activities.FetchLeague, mock.Anything, preseasonTestYear, leagueID).
			Return(nil).Once()
	}
	env.OnActivity(activities.FetchTeams, mock.Anything, mock.MatchedBy(expectedTwoLeagueTeams)).
		Return(nil).Once()
	env.OnActivity(activities.FetchYahooLeagueData, mock.Anything,
		yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonTestLeagueID}).
		Return(yahoo.FetchYahooLeagueDataResult{}, nil).Once()
	env.OnActivity(activities.FetchYahooLeagueData, mock.Anything,
		yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.FetchYahooLeagueDataResult{UnavailableResources: []string{"draft results"}}, nil).Once()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, twoLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"league 1002 draft results"}, result.UnavailableResources)
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

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, twoLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"league 1002 transactions"}, result.UnavailableResources)
	env.AssertExpectations(t)
}

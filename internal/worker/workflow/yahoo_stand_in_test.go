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

const (
	standInSourceSeason   = preseasonTestYear - 1
	standInSourceLeagueID = 1003
	standInExpectedNote   = "league 1001 (using TEMPORARY stand-in settings from 2025 league 1003"
)

// standInYahooInput configures league 1001 with stand-in settings and league
// 1002 normally.
func standInYahooInput() YahooSeasonWorkflowInput {
	return YahooSeasonWorkflowInput{
		StartYear: preseasonTestYear,
		Season: config.Season{Leagues: []config.League{
			{
				LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2},
				TemporaryMetadataFrom: &config.LeagueMetadataSource{
					Season: standInSourceSeason, LeagueID: standInSourceLeagueID,
				},
			},
			{LeagueID: preseasonSecondLeagueID, TeamIDs: []int{3}},
		}},
	}
}

var onlySecondLeagueTeams = []yahoo.TeamInfo{{LeagueID: preseasonSecondLeagueID, TeamID: 3}}

func TestFetchYahooSeasonWorkflow_StandInLeagueSkipsYahooAPI(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	var activities *yahoo.FetchActivities

	env.OnActivity(activities.FetchLeague, mock.Anything, standInSourceSeason, standInSourceLeagueID).
		Return(nil).Once()
	env.OnActivity(activities.FetchLeague, mock.Anything, preseasonTestYear, preseasonSecondLeagueID).
		Return(nil).Once()
	env.OnActivity(activities.FetchTeams, mock.Anything,
		yahoo.FetchTeamsInput{StartSeason: preseasonTestYear, Teams: onlySecondLeagueTeams}).
		Return(nil).Once()
	env.OnActivity(activities.FetchYahooLeagueData, mock.Anything,
		yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.FetchYahooLeagueDataResult{}, nil).Once()
	// Only the API league plans a player pool download.
	expectPoolUpToDate(env, preseasonSecondLeagueID)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, standInYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{standInExpectedNote}, result.UnavailableResources)
	env.AssertExpectations(t)
}

func TestFetchYahooSeasonWorkflow_StandInSourceFetchFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	var activities *yahoo.FetchActivities

	env.OnActivity(activities.FetchLeague, mock.Anything, standInSourceSeason, standInSourceLeagueID).
		Return(assert.AnError)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, standInYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "fetch stand-in source league 2025/1003 for league 1001")
}

func TestImportYahooSeasonWorkflow_StandInLeagueImportsSourceSettings(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	var activities *yahoo.ImportActivities

	env.OnActivity(activities.ImportYahooStandInLeague, mock.Anything, yahoo.ImportYahooStandInLeagueInput{
		Season: preseasonTestYear, LeagueID: preseasonTestLeagueID,
		Source: config.LeagueMetadataSource{Season: standInSourceSeason, LeagueID: standInSourceLeagueID},
	}).Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeague, mock.Anything,
		yahoo.ImportYahooLeagueInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooTeams, mock.Anything,
		yahoo.ImportYahooTeamsInput{Season: preseasonTestYear, Teams: onlySecondLeagueTeams}).
		Return(yahoo.ImportYahooTeamsResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything,
		yahoo.ImportYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil).Once()
	// Only the API league imports a player pool.
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything,
		yahoo.ImportYahooLeaguePlayersInput{Season: preseasonTestYear, LeagueID: preseasonSecondLeagueID}).
		Return(yahoo.ImportYahooLeaguePlayersResult{Players: 1}, nil).Once()

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, standInYahooInput())

	require.NoError(t, env.GetWorkflowError())
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{standInExpectedNote}, result.UnavailableResources)
	env.AssertExpectations(t)
}

func TestImportYahooSeasonWorkflow_StandInImportFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	var activities *yahoo.ImportActivities

	env.OnActivity(activities.ImportYahooStandInLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, assert.AnError)

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, standInYahooInput())

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "import stand-in league 1001 from 2025/1003")
}

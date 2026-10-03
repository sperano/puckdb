package workflow

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	standInTestLeagueID = 2001
	// twoLeagueStepTotal: 2 league metadata + 1 teams batch + 2 league data + 2 pools.
	twoLeagueStepTotal = 7
	// singleLeagueStepTotal: 1 league metadata + 1 teams batch + 1 league data + 1 pool.
	singleLeagueStepTotal = 4
	// standInOnlyStepTotal: one metadata step, nothing else.
	standInOnlyStepTotal = 1
	// mixedLeagueStepTotal: 3 league metadata (API with teams, API without
	// teams, stand-in) + 1 teams batch + 2 league data + 2 pools (API leagues only).
	mixedLeagueStepTotal = 8

	mixedNoTeamsLeagueID  = 3001
	mixedStandInLeagueID  = 3002
	mixedStandInSourceID  = 3003
	mixedStandInSourceSsn = preseasonTestYear - 1
)

func TestYahooSeasonStepTotal(t *testing.T) {
	t.Parallel()
	standIn := config.League{
		LeagueID:              standInTestLeagueID,
		TemporaryMetadataFrom: &config.LeagueMetadataSource{Season: 2025, LeagueID: 1},
	}
	tests := []struct {
		name   string
		season config.Season
		want   int
	}{
		{"no leagues", config.Season{}, 0},
		{"one league with teams", singleLeagueYahooInput().Season, singleLeagueStepTotal},
		{"two leagues", twoLeagueYahooInput().Season, twoLeagueStepTotal},
		{"league without teams skips teams batch",
			config.Season{Leagues: []config.League{{LeagueID: preseasonTestLeagueID}}}, singleLeagueStepTotal - 1},
		{"stand-in only", config.Season{Leagues: []config.League{standIn}}, standInOnlyStepTotal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, yahooSeasonStepTotal(tt.season))
		})
	}
}

// recordProgressSaves captures the Current of the child's single bar on every
// saved report, in order.
func recordProgressSaves(env *testsuite.TestWorkflowEnvironment) *[]int {
	currents := &[]int{}
	env.OnActivity(((*shared.ProgressActivities)(nil)).Save,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _, _ string, data []byte) error {
			var report shared.ProgressReport
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
				return err
			}
			*currents = append(*currents, report.Groups[0].Bars[0].Current)
			return nil
		}).Maybe()
	return currents
}

func assertCompletedYahooChildReport(t *testing.T, report shared.ProgressReport, total int, header string) {
	t.Helper()
	require.Len(t, report.Groups, 1)
	group := report.Groups[0]
	assert.Equal(t, header, group.Header)
	assert.Equal(t, total, report.Total)
	require.Len(t, group.Bars, 1)
	assert.Equal(t, total, group.Bars[0].Total)
	assert.Equal(t, total, group.Bars[0].Current)
	assert.NotZero(t, group.StartedAt)
	assert.NotZero(t, group.CompletedAt)
	assert.Contains(t, group.CompletedMsg, "Done in")
}

func TestFetchYahooSeasonWorkflow_ProgressAdvancesAndCompletes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	currents := recordProgressSaves(env)
	var activities *yahoo.FetchActivities
	for _, leagueID := range []int{preseasonTestLeagueID, preseasonSecondLeagueID} {
		env.OnActivity(activities.FetchLeague, mock.Anything, preseasonTestYear, leagueID).Return(nil).Once()
		env.OnActivity(activities.FetchYahooLeagueData, mock.Anything,
			yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: leagueID}).
			Return(yahoo.FetchYahooLeagueDataResult{}, nil).Once()
		expectPoolUpToDate(env, leagueID)
	}
	env.OnActivity(activities.FetchTeams, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, twoLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	assertCompletedYahooChildReport(t, queryProgress(t, env), twoLeagueStepTotal,
		"Fetching Yahoo 2026 metadata...")
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 7}, *currents,
		"one save per step, then the completion save")
}

func TestImportYahooSeasonWorkflow_ProgressAdvancesAndCompletes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	currents := recordProgressSaves(env)
	var activities *yahoo.ImportActivities
	env.OnActivity(activities.ImportYahooLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooTeams, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooTeamsResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeaguePlayersResult{}, nil).Once()

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, singleLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	assertCompletedYahooChildReport(t, queryProgress(t, env), singleLeagueStepTotal,
		"Importing Yahoo 2026 metadata...")
	assert.Equal(t, []int{0, 1, 2, 3, 4, 4}, *currents)
}

func TestFetchSeasonsWorkflow_YahooBarsLinkToChildWorkflows(t *testing.T) {
	for _, mode := range []seasonSyncMode{seasonSyncFetch, seasonSyncImport} {
		withYahooSnapshot(t, preseasonYahooSnapshot())
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetStartTime(preseasonTestNow)
		env.RegisterWorkflow(FetchSeasonsWorkflow)
		env.RegisterWorkflow(ImportSeasonsWorkflow)
		env.RegisterWorkflow(FetchYahooSeasonWorkflow)
		env.RegisterWorkflow(ImportYahooSeasonWorkflow)
		mockProgressSaves(env)
		mockNoUpcomingSeason(env)
		var activities *nhl.SeasonsActivities
		env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, mock.Anything).
			Return(nhl.FetchSeasonsManifestResult{Origin: core.OriginFileSystem}, nil)
		env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.Anything).Return(YahooSeasonSyncResult{}, nil).Maybe()
		env.OnWorkflow(ImportYahooSeasonWorkflow, mock.Anything, mock.Anything).Return(YahooSeasonSyncResult{}, nil).Maybe()

		workflowFn, wantID := any(FetchSeasonsWorkflow), WorkflowIDFetchYahooSeason(preseasonTestYear)
		if mode == seasonSyncImport {
			workflowFn, wantID = ImportSeasonsWorkflow, WorkflowIDImportYahooSeason(preseasonTestYear)
		}
		env.ExecuteWorkflow(workflowFn, preseasonInput())

		require.NoError(t, env.GetWorkflowError())
		bars := queryProgress(t, env).Groups[groupYahooMetadata].Bars
		require.Len(t, bars, 1)
		assert.Equal(t, wantID, bars[0].ProgressSourceKey)
		assert.Equal(t, "2026-27", bars[0].Label)
		assert.Equal(t, singleLeagueStepTotal, bars[0].Total)
		assert.Equal(t, bars[0].Total, bars[0].Current)
	}
}

// mixedLeagueYahooInput mixes an API league with teams, an API league without
// teams, and a stand-in league.
func mixedLeagueYahooInput() YahooSeasonWorkflowInput {
	return YahooSeasonWorkflowInput{
		StartYear: preseasonTestYear,
		Season: config.Season{Leagues: []config.League{
			{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1}},
			{LeagueID: mixedNoTeamsLeagueID},
			{
				LeagueID: mixedStandInLeagueID,
				TemporaryMetadataFrom: &config.LeagueMetadataSource{
					Season: mixedStandInSourceSsn, LeagueID: mixedStandInSourceID,
				},
			},
		}},
	}
}

func TestFetchYahooSeasonWorkflow_MixedLeaguesProgressMatchesTotal(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	currents := recordProgressSaves(env)
	var activities *yahoo.FetchActivities
	for _, leagueID := range []int{preseasonTestLeagueID, mixedNoTeamsLeagueID} {
		env.OnActivity(activities.FetchLeague, mock.Anything, preseasonTestYear, leagueID).Return(nil).Once()
		env.OnActivity(activities.FetchYahooLeagueData, mock.Anything,
			yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: leagueID}).
			Return(yahoo.FetchYahooLeagueDataResult{}, nil).Once()
		expectPoolUpToDate(env, leagueID)
	}
	env.OnActivity(activities.FetchLeague, mock.Anything, mixedStandInSourceSsn, mixedStandInSourceID).
		Return(nil).Once()
	env.OnActivity(activities.FetchTeams, mock.Anything, mock.Anything).Return(nil).Once()
	input := mixedLeagueYahooInput()

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, mixedLeagueStepTotal, yahooSeasonStepTotal(input.Season))
	assertCompletedYahooChildReport(t, queryProgress(t, env), mixedLeagueStepTotal,
		"Fetching Yahoo 2026 metadata...")
	assert.Equal(t, stepSequence(mixedLeagueStepTotal), *currents)
}

func TestImportYahooSeasonWorkflow_MixedLeaguesProgressMatchesTotal(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	currents := recordProgressSaves(env)
	var activities *yahoo.ImportActivities
	env.OnActivity(activities.ImportYahooStandInLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Twice()
	env.OnActivity(activities.ImportYahooTeams, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooTeamsResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil).Twice()
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeaguePlayersResult{}, nil).Twice()
	input := mixedLeagueYahooInput()

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, mixedLeagueStepTotal, yahooSeasonStepTotal(input.Season))
	assertCompletedYahooChildReport(t, queryProgress(t, env), mixedLeagueStepTotal,
		"Importing Yahoo 2026 metadata...")
	assert.Equal(t, stepSequence(mixedLeagueStepTotal), *currents)
}

// stepSequence is the expected saves for a total of n steps: 0..n, then n
// again for the completion save.
func stepSequence(n int) []int {
	seq := make([]int, 0, n+2)
	for i := 0; i <= n; i++ {
		seq = append(seq, i)
	}
	return append(seq, n)
}

package workflow

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	preseasonTestYear     = 2026
	preseasonTestLeagueID = 1001
	preseasonConcurrency  = 1
)

var preseasonTestNow = time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)

func preseasonInput() *model.SeasonsInput {
	start := preseasonTestYear
	concurrency := preseasonConcurrency
	return &model.SeasonsInput{StartSeason: &start, SeasonConcurrency: &concurrency}
}

func preseasonYahooSnapshot() shared.YahooSeasonsSnapshot {
	return shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{
		preseasonTestYear: {Leagues: []config.League{{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2}}}},
	}}
}

func withYahooSnapshot(t *testing.T, snapshot shared.YahooSeasonsSnapshot) {
	t.Helper()
	previous := loadYahooSeasons
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return snapshot }
	t.Cleanup(func() { loadYahooSeasons = previous })
}

func mockProgressSaves(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(((*shared.ProgressActivities)(nil)).Save,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// mockNoUpcomingSeason stubs the upcoming-season roster step as having no
// upcoming season in range.
func mockNoUpcomingSeason(env *testsuite.TestWorkflowEnvironment) {
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchUpcomingSeasonRosters, mock.Anything, mock.Anything).
		Return(worknhl.UpcomingSeasonRostersResult{}, nil).Maybe()
	env.OnActivity(activities.ImportUpcomingSeasonRosters, mock.Anything, mock.Anything).
		Return(worknhl.UpcomingSeasonRostersResult{}, nil).Maybe()
}

func queryProgress(t *testing.T, env *testsuite.TestWorkflowEnvironment) shared.ProgressReport {
	t.Helper()
	encoded, err := env.QueryWorkflow(shared.ProgressReportQueryName)
	require.NoError(t, err)
	var report shared.ProgressReport
	require.NoError(t, encoded.Get(&report))
	return report
}

func TestSelectYahooSeasons_SortedRangeAndOmittedUpperBound(t *testing.T) {
	t.Parallel()
	start := preseasonTestYear
	snapshot := shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{
		2027: {Leagues: []config.League{{LeagueID: 3}}},
		2025: {Leagues: []config.League{{LeagueID: 1}}},
		2026: {Leagues: []config.League{{LeagueID: 2}}},
	}}

	selected := selectYahooSeasons(snapshot, &model.SeasonsInput{StartSeason: &start})

	require.Len(t, selected, 2)
	assert.Equal(t, 2026, selected[0].StartYear)
	assert.Equal(t, 2027, selected[1].StartYear)
}

func TestValidateSeasonsInput(t *testing.T) {
	t.Parallel()
	validStart, validEnd := 2026, 2027
	invalidStart, invalidEnd := 2027, 2026
	zero := 0

	assert.NoError(t, ValidateSeasonsInput(nil))
	assert.NoError(t, ValidateSeasonsInput(&model.SeasonsInput{StartSeason: &validStart, EndSeason: &validEnd}))
	assert.Error(t, ValidateSeasonsInput(&model.SeasonsInput{StartSeason: &invalidStart, EndSeason: &invalidEnd}))
	assert.Error(t, ValidateSeasonsInput(&model.SeasonsInput{StartSeason: &zero}))
}

func TestFetchSeasonsWorkflow_PreseasonRunsYahooWithoutNHL(t *testing.T) {
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(preseasonTestNow)
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{Seasons: nil, Origin: core.OriginFileSystem}, nil)
	env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.MatchedBy(func(input YahooSeasonWorkflowInput) bool {
		return input.StartYear == preseasonTestYear && len(input.Season.Leagues) == 1
	})).Return(YahooSeasonSyncResult{}, nil).Once()

	env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	require.Len(t, report.Groups, 3)
	assert.Contains(t, report.Groups[groupYahooMetadata].CompletedMsg, "Fetched Yahoo metadata for 1 seasons")
	assert.Contains(t, report.Groups[groupNHLSeasons].CompletedMsg, "no started NHL seasons")
	assert.Contains(t, report.Message, "1 Yahoo season(s), 0 started NHL season(s)")
	env.AssertExpectations(t)
}

func TestImportSeasonsWorkflow_PreseasonRunsYahooWithoutNHL(t *testing.T) {
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(preseasonTestNow)
	env.RegisterWorkflow(ImportSeasonsWorkflow)
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{Seasons: nil, Origin: core.OriginFileSystem}, nil)
	env.OnWorkflow(ImportYahooSeasonWorkflow, mock.Anything, mock.Anything).
		Return(YahooSeasonSyncResult{}, nil).Once()

	env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupYahooMetadata].CompletedMsg, "Imported Yahoo metadata for 1 seasons")
	assert.Contains(t, report.Groups[groupNHLSeasons].CompletedMsg, "no started NHL seasons")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_StartedSeasonRunsBothDomains(t *testing.T) {
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC))
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	env.RegisterWorkflow(FetchNHLSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	season := testSeasonW(preseasonTestYear, "2026-09-29", "2026-10-01")
	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	var playoffActivities *worknhl.PlayoffActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{Seasons: []nhl.SeasonInfo{season}}, nil)
	env.OnActivity(playoffActivities.ListSeasonTeams, mock.Anything, preseasonTestYear).
		Return([]string{"MTL"}, nil)
	env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.Anything).
		Return(YahooSeasonSyncResult{}, nil).Once()
	env.OnWorkflow(FetchNHLSeasonWorkflow, mock.Anything, mock.Anything).
		Return(core.OriginCounts{}, nil).Once()

	env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "1 Yahoo season(s), 1 started NHL season(s)")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_MissingYahooConfigReportsNoWork(t *testing.T) {
	withYahooSnapshot(t, shared.YahooSeasonsSnapshot{})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{}, nil)
	env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "No work matched seasons 2026 onward")
	assert.Contains(t, report.Groups[groupYahooMetadata].CompletedMsg, "no configured Yahoo seasons")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_InvalidRangeFailsBeforeSelection(t *testing.T) {
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	start, end := 2027, 2026

	env.ExecuteWorkflow(FetchSeasonsWorkflow, &model.SeasonsInput{StartSeason: &start, EndSeason: &end})

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid season range 2027-2026")
}

func TestFetchSeasonsWorkflow_ReportsTemporarilyUnavailableYahooResources(t *testing.T) {
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{}, nil)
	env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.Anything).Return(YahooSeasonSyncResult{
		UnavailableResources: []string{"league 1001 draft results"},
	}, nil)

	env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupYahooMetadata].CompletedMsg,
		"Temporarily unavailable: 2026 league 1001 draft results")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_PartialYahooFailureKeepsFailedBarIncomplete(t *testing.T) {
	snapshot := preseasonYahooSnapshot()
	snapshot.Seasons[2027] = config.Season{Leagues: []config.League{{LeagueID: 1002}}}
	withYahooSnapshot(t, snapshot)
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	input := preseasonInput()
	env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.MatchedBy(func(input YahooSeasonWorkflowInput) bool {
		return input.StartYear == 2026
	})).Return(YahooSeasonSyncResult{}, nil).Once()
	env.OnWorkflow(FetchYahooSeasonWorkflow, mock.Anything, mock.MatchedBy(func(input YahooSeasonWorkflowInput) bool {
		return input.StartYear == 2027
	})).Return(YahooSeasonSyncResult{}, assert.AnError).Once()

	env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	require.Error(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "Yahoo metadata partially completed")
	assert.Equal(t, 1, report.Groups[groupYahooMetadata].Bars[0].Current)
	assert.Equal(t, 2, report.Groups[groupYahooMetadata].Bars[0].Total)
	env.AssertExpectations(t)
}

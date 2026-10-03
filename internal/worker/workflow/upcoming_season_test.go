package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	upcomingTestTeams            = 32
	upcomingTestTeamsWithRosters = 30
)

func upcomingResult() worknhl.UpcomingSeasonRostersResult {
	return worknhl.UpcomingSeasonRostersResult{
		Season: preseasonTestYear, Teams: upcomingTestTeams, TeamsWithRosters: upcomingTestTeamsWithRosters,
	}
}

func runPreseasonSync(t *testing.T, mode seasonSyncMode, upcoming worknhl.UpcomingSeasonRostersResult, upcomingErr error) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	withYahooSnapshot(t, shared.YahooSeasonsSnapshot{})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(preseasonTestNow)
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(ImportSeasonsWorkflow)
	mockProgressSaves(env)

	input := preseasonInput()
	var activities *worknhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, input).
		Return(worknhl.FetchSeasonsManifestResult{Origin: core.OriginFileSystem}, nil)
	upcomingActivity, parent := activities.FetchUpcomingSeasonRosters, FetchSeasonsWorkflow
	if mode == seasonSyncImport {
		upcomingActivity, parent = activities.ImportUpcomingSeasonRosters, ImportSeasonsWorkflow
	}
	env.OnActivity(upcomingActivity, mock.Anything, input).Return(upcoming, upcomingErr)

	env.ExecuteWorkflow(parent, input)
	return env
}

func TestFetchSeasonsWorkflow_FetchesUpcomingSeasonRosters(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncFetch, upcomingResult(), nil)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	require.Len(t, report.Groups, 3)
	assert.Contains(t, report.Groups[groupUpcomingSeason].CompletedMsg,
		"Fetched 2026-27 rosters for 30 of 32 clubs carried forward from 2025-26")
	assert.Contains(t, report.Message, "upcoming NHL season 2026 rosters")
	assert.NotContains(t, report.Message, "No work matched")
	assert.Equal(t, 1, report.Groups[groupUpcomingSeason].Bars[0].Current)
	env.AssertExpectations(t)
}

func TestImportSeasonsWorkflow_ImportsUpcomingSeasonRosters(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncImport, upcomingResult(), nil)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupUpcomingSeason].CompletedMsg, "Imported 2026-27 rosters for 30 of 32 clubs")
	assert.Contains(t, report.Message, "upcoming NHL season 2026 rosters")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_NoUpcomingSeasonIsSkipped(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncFetch, worknhl.UpcomingSeasonRostersResult{}, nil)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupUpcomingSeason].CompletedMsg,
		"Skipped upcoming NHL season: none in seasons 2026 onward has yet to start.")
	assert.Contains(t, report.Message, "No work matched seasons 2026 onward")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_UpcomingSeasonFailureFailsSync(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncFetch, worknhl.UpcomingSeasonRostersResult{}, assert.AnError)

	require.Error(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "Upcoming NHL season rosters failed for seasons 2026 onward")
	assert.Empty(t, report.Groups[groupUpcomingSeason].CompletedMsg)
}

package workflow

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

const (
	upcomingTestTeams            = 32
	upcomingTestTeamsWithRosters = 30
	preUpcomingFetchWorkflowType = "PreUpcomingFetchSeasonsWorkflow"
	preUpcomingImportWorkflow    = "PreUpcomingImportSeasonsWorkflow"
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
	assert.Equal(t, 1, report.Groups[groupUpcomingSeason].Bars[0].Current)
	env.AssertExpectations(t)
}

func TestImportSeasonsWorkflow_ImportsUpcomingSeasonRosters(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncImport, upcomingResult(), nil)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupUpcomingSeason].CompletedMsg, "Imported 2026-27 rosters for 30 of 32 clubs")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_NoUpcomingSeasonIsSkipped(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncFetch, worknhl.UpcomingSeasonRostersResult{}, nil)

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[groupUpcomingSeason].CompletedMsg,
		"Skipped upcoming NHL season: none in seasons 2026 onward has yet to start.")
	env.AssertExpectations(t)
}

func TestFetchSeasonsWorkflow_UpcomingSeasonFailureFailsSync(t *testing.T) {
	env := runPreseasonSync(t, seasonSyncFetch, worknhl.UpcomingSeasonRostersResult{}, assert.AnError)

	require.Error(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "Upcoming NHL season rosters failed for seasons 2026 onward")
	assert.Empty(t, report.Groups[groupUpcomingSeason].CompletedMsg)
}

// preUpcomingSeasonSync is the season sync parent as it was before the
// upcoming-season step: it records histories without that version marker.
func preUpcomingSeasonSync(mode seasonSyncMode) func(temporalworkflow.Context, *model.SeasonsInput) error {
	return func(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
		_ = seasonSyncVersion(ctx)
		input = normalizeSeasonsInput(input)
		cfg, err := snapshotSeasonSyncConfig(ctx, input)
		if err != nil {
			return err
		}
		tracker, err := shared.InitTracker(ctx, newSeasonSyncProgressReport(mode))
		if err != nil {
			return err
		}
		ctx = temporalworkflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
		rangeLabel := formatSeasonRange(input)
		yahooSeasons := selectYahooSeasons(cfg.Yahoo, input)
		if err := processYahooSeasonGroup(ctx, tracker, yahooSeasons, cfg.Concurrency, rangeLabel, mode); err != nil {
			return err
		}
		seasons, err := loadSeasonsManifest(ctx, temporalworkflow.GetLogger(ctx), input)
		if err != nil {
			return err
		}
		if err := processNHLSeasonGroup(ctx, tracker, seasons, cfg.Concurrency, rangeLabel, mode); err != nil {
			return err
		}
		setSeasonSyncOutcome(ctx, tracker, len(yahooSeasons), len(seasons), rangeLabel)
		return nil
	}
}

func TestSeasonSyncReplaysHistoriesWithoutUpcomingSeasonStep(t *testing.T) {
	withYahooSnapshot(t, shared.YahooSeasonsSnapshot{})
	c, stopServer := startTemporalForReplayTest(t)
	defer stopServer()
	taskQueue := uniqueReplayName("pre-upcoming-season-tq")
	season := testSeasonW(replaySeasonStartYear, replaySeasonStart, replaySeasonEnd)
	input := &model.SeasonsInput{StartSeason: seasonIntPtr(replaySeasonStartYear), EndSeason: seasonIntPtr(replaySeasonStartYear)}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(preUpcomingSeasonSync(seasonSyncFetch),
		temporalworkflow.RegisterOptions{Name: preUpcomingFetchWorkflowType})
	w.RegisterWorkflowWithOptions(preUpcomingSeasonSync(seasonSyncImport),
		temporalworkflow.RegisterOptions{Name: preUpcomingImportWorkflow})
	for _, name := range []string{"FetchNHLSeasonWorkflow", "ImportNHLSeasonWorkflow"} {
		w.RegisterWorkflowWithOptions(func(temporalworkflow.Context, nhl.SeasonInfo) (core.OriginCounts, error) {
			return core.OriginCounts{}, nil
		}, temporalworkflow.RegisterOptions{Name: name})
	}
	for _, item := range []namedActivity{
		{"Save", func(context.Context, string, string, []byte) error { return nil }},
		{"DeleteBatch", func(context.Context, []string) error { return nil }},
		{"FetchSeasonsManifest", func(context.Context, *model.SeasonsInput) (worknhl.FetchSeasonsManifestResult, error) {
			return worknhl.FetchSeasonsManifestResult{Seasons: []nhl.SeasonInfo{season}}, nil
		}},
		{"ListSeasonTeams", func(context.Context, int) ([]string, error) { return replayTeams, nil }},
	} {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
	require.NoError(t, w.Start())
	fetchHistory := runSeasonWorkflow(t, c, taskQueue, preUpcomingFetchWorkflowType, "pre-upcoming-fetch", input)
	importHistory := runSeasonWorkflow(t, c, taskQueue, preUpcomingImportWorkflow, "pre-upcoming-import", input)
	w.Stop()

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(FetchSeasonsWorkflow, temporalworkflow.RegisterOptions{Name: preUpcomingFetchWorkflowType})
	replayer.RegisterWorkflowWithOptions(ImportSeasonsWorkflow, temporalworkflow.RegisterOptions{Name: preUpcomingImportWorkflow})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, fetchHistory))
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, importHistory))
}

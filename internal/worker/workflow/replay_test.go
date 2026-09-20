package workflow

// Replay-determinism test for the real season workflows.
//
// FetchSeasonWorkflow and ImportSeasonWorkflow shape their command sequence
// from two process-local settings: the day-concurrency flag (how many
// activities the worker pool schedules up front) and the Yahoo seasons YAML
// (which leagues get fetched/imported). Both are snapshotted at workflow start,
// so a worker that restarts with different settings must still replay a
// recorded history to the same command sequence.
//
// This test records histories with one configuration, changes BOTH settings,
// and then asserts the recorded histories replay clean while a fresh execution
// picks up the new configuration.
//
// It needs a Temporal server and mutates package globals (viper, the
// loadYahooSeasons seam), so it must not run in parallel.

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const (
	// A short, long-past season: EffectiveEndDate clips the end date at
	// yesterday, so a past window keeps the day count fixed at replaySeasonDays.
	replaySeasonStartYear = 2019
	replaySeasonStart     = "2019-10-02"
	replaySeasonEnd       = "2019-10-07"
	replaySeasonDays      = 6

	recordedDayConcurrency = 2
	changedDayConcurrency  = 5

	fetchSeasonWorkflowType  = "FetchSeasonWorkflow"
	importSeasonWorkflowType = "ImportSeasonWorkflow"
)

// recordedLeagues is the Yahoo configuration in force while histories are
// recorded; changedLeagues is what the process reads afterwards.
func recordedLeagues() []config.League {
	return []config.League{{LeagueID: 1, TeamIDs: []int{1, 2}}}
}

func changedLeagues() []config.League {
	return []config.League{
		{LeagueID: 1, TeamIDs: []int{1, 2}},
		{LeagueID: 2, TeamIDs: []int{3, 4}},
	}
}

func TestSeasonWorkflowsReplayUnderChangedConfig(t *testing.T) {
	c, stopServer := startTemporalForReplayTest(t)
	defer stopServer()

	taskQueue := uniqueReplayName("season-replay-tq")
	stopWorker := startSeasonReplayWorker(t, c, taskQueue)
	defer stopWorker()

	season := testSeasonW(replaySeasonStartYear, replaySeasonStart, replaySeasonEnd)

	setSeasonConfig(t, recordedDayConcurrency, replaySeasonStartYear, recordedLeagues())
	fetchHist := runSeasonWorkflow(t, c, taskQueue, fetchSeasonWorkflowType, "replay-fetch-recorded", season)
	importHist := runSeasonWorkflow(t, c, taskQueue, importSeasonWorkflowType, "replay-import-recorded", season)
	requireRecordedFetchShape(t, fetchHist)
	requireRecordedImportShape(t, importHist)

	// Both configuration inputs change under the already-recorded histories,
	// as they would when a worker restarts with an edited YAML and flags.
	setSeasonConfig(t, changedDayConcurrency, replaySeasonStartYear, changedLeagues())

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FetchSeasonWorkflow)
	replayer.RegisterWorkflow(ImportSeasonWorkflow)
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, fetchHist),
		"recorded fetch history must replay unchanged under new local config")
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, importHist),
		"recorded import history must replay unchanged under new local config")

	requireNewRunUsesNewConfig(t, c, taskQueue, season)
}

// requireRecordedFetchShape pins the command sequence the fetch history was
// recorded with, so a later replay assertion cannot pass vacuously.
func requireRecordedFetchShape(t *testing.T, hist *historypb.History) {
	t.Helper()
	requireActivityScheduledCount(t, hist, "FetchLeague", len(recordedLeagues()))
	requireActivityScheduledCount(t, hist, "FetchYahooLeagueData", len(recordedLeagues()))
	requireActivityScheduledCount(t, hist, "FetchDay", replaySeasonDays)
	requireActivityScheduledCount(t, hist, "FetchTeamPlayoffGames", len(replayTeams))
	require.Equal(t, recordedDayConcurrency, initialActivityBatchSize(hist, "FetchDay"),
		"recorded run should have scheduled the recorded concurrency up front")
}

func requireRecordedImportShape(t *testing.T, hist *historypb.History) {
	t.Helper()
	requireActivityScheduledCount(t, hist, "ImportYahooLeague", len(recordedLeagues()))
	requireActivityScheduledCount(t, hist, "ImportYahooLeagueData", len(recordedLeagues()))
	requireActivityScheduledCount(t, hist, "ImportDay", replaySeasonDays)
	requireActivityScheduledCount(t, hist, "ImportTeamPlayoffGames", len(replayTeams))
	require.Equal(t, recordedDayConcurrency, initialActivityBatchSize(hist, "ImportDay"),
		"recorded run should have scheduled the recorded concurrency up front")
}

// requireNewRunUsesNewConfig is the other half of the acceptance criteria:
// snapshotting must not freeze the configuration for executions started after
// the change — only for the ones already recorded.
func requireNewRunUsesNewConfig(t *testing.T, c client.Client, taskQueue string, season nhl.SeasonInfo) {
	t.Helper()
	hist := runSeasonWorkflow(t, c, taskQueue, fetchSeasonWorkflowType, "replay-fetch-new", season)
	requireActivityScheduledCount(t, hist, "FetchLeague", len(changedLeagues()))
	requireActivityScheduledCount(t, hist, "FetchYahooLeagueData", len(changedLeagues()))
	require.Equal(t, changedDayConcurrency, initialActivityBatchSize(hist, "FetchDay"),
		"a new execution should schedule the new concurrency up front")
}

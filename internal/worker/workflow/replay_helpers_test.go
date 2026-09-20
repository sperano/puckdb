package workflow

// Harness for the season-workflow replay tests: a Temporal dev server, stub
// activities registered by name, and history helpers.
//
// The stubs are registered by name rather than by struct because the workflows
// dispatch by name too — both the regular activities (via method values on nil
// receivers) and the ProgressActivities local activities, which the SDK
// resolves through the registry precisely because the receiver is nil.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

const (
	// envTestTemporalHostPort names an already-running Temporal server to use
	// instead of starting an in-process dev server.
	envTestTemporalHostPort = "PUCKDB_TEST_TEMPORAL_HOSTPORT"

	// devServerStartTimeout bounds the first-run download of the Temporal CLI
	// binary (~50MB) plus server startup.
	devServerStartTimeout = 90 * time.Second

	// seasonWorkflowRunTimeout bounds one season workflow run against stub
	// activities; anything longer means a stuck worker, not slow work.
	seasonWorkflowRunTimeout = 120 * time.Second
)

// replayTeams is what the ListSeasonTeams stub returns; it sizes the playoff
// group of both season workflows.
var replayTeams = []string{"MTL", "TOR", "BOS"}

// namedActivity pairs a stub with the activity name the workflows dispatch on.
type namedActivity struct {
	name string
	fn   any
}

// seasonStubActivities lists every activity FetchSeasonWorkflow and
// ImportSeasonWorkflow schedule, including the Save/Load progress local
// activities. Each stub mirrors the real activity's signature (argument count
// and types must match, or the worker rejects the task) and returns zero
// values — these tests are about the command sequence, not the work.
func seasonStubActivities() []namedActivity {
	return []namedActivity{
		{"Save", func(_ context.Context, _ string, _ []byte) error { return nil }},
		{"Load", func(_ context.Context, _ string) ([]byte, error) { return nil, nil }},

		{"FetchLeague", func(_ context.Context, _ int, _ int) error { return nil }},
		{"FetchTeams", func(_ context.Context, _ yahoo.FetchTeamsInput) error { return nil }},
		{"FetchYahooLeagueData", func(_ context.Context, _ yahoo.FetchYahooLeagueDataInput) error { return nil }},
		{"FetchSeasonRosters", func(_ context.Context, _ worknhl.FetchSeasonRostersInput) error { return nil }},
		{"FetchClubStats", func(_ context.Context, _ worknhl.FetchClubStatsInput) error { return nil }},
		{"FetchDay", func(_ context.Context, _ worknhl.FetchDayInput) (core.OriginCounts, error) {
			return core.OriginCounts{}, nil
		}},
		{"ListSeasonTeams", func(_ context.Context, _ int) ([]string, error) { return replayTeams, nil }},
		{"FetchTeamPlayoffGames", func(_ context.Context, _ worknhl.FetchTeamPlayoffGamesInput) (worknhl.FetchTeamPlayoffGamesResult, error) {
			return worknhl.FetchTeamPlayoffGamesResult{}, nil
		}},

		{"ImportYahooLeague", func(_ context.Context, _ yahoo.ImportYahooLeagueInput) (yahoo.ImportYahooLeagueResult, error) {
			return yahoo.ImportYahooLeagueResult{}, nil
		}},
		{"ImportYahooTeams", func(_ context.Context, _ yahoo.ImportYahooTeamsInput) (yahoo.ImportYahooTeamsResult, error) {
			return yahoo.ImportYahooTeamsResult{}, nil
		}},
		{"ImportYahooLeagueData", func(_ context.Context, _ yahoo.ImportYahooLeagueDataInput) (yahoo.ImportYahooLeagueDataResult, error) {
			return yahoo.ImportYahooLeagueDataResult{}, nil
		}},
		{"ImportSeasonRosters", func(_ context.Context, _ worknhl.FetchSeasonRostersInput) error { return nil }},
		{"ImportClubStats", func(_ context.Context, _ worknhl.FetchClubStatsInput) error { return nil }},
		{"ImportDay", func(_ context.Context, _ worknhl.ImportDayInput) (core.OriginCounts, error) {
			return core.OriginCounts{}, nil
		}},
		{"ImportTeamPlayoffGames", func(_ context.Context, _ worknhl.ImportTeamPlayoffGamesInput) (worknhl.ImportTeamPlayoffGamesResult, error) {
			return worknhl.ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}, nil
		}},
	}
}

// startSeasonReplayWorker registers both season workflows and every stub
// activity on a worker and starts it. The returned func stops the worker.
func startSeasonReplayWorker(t *testing.T, c client.Client, taskQueue string) func() {
	t.Helper()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(FetchSeasonWorkflow)
	w.RegisterWorkflow(ImportSeasonWorkflow)
	for _, a := range seasonStubActivities() {
		w.RegisterActivityWithOptions(a.fn, activity.RegisterOptions{Name: a.name})
	}
	require.NoError(t, w.Start())
	return w.Stop
}

// setSeasonConfig points both configuration inputs of the season workflows at
// test values for the rest of the test (or until the next call): the day
// concurrency viper flag and the Yahoo seasons loader. Restoring on cleanup is
// what keeps these tests from leaking into the rest of the package — which is
// also why they must not run in parallel.
func setSeasonConfig(t *testing.T, dayConcurrency, startYear int, leagues []config.League) {
	t.Helper()
	prevConcurrency := viper.Get(config.FlagDayConcurrency)
	prevLoader := loadYahooSeasons

	viper.Set(config.FlagDayConcurrency, dayConcurrency)
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot {
		return shared.YahooSeasonsSnapshot{
			Seasons: config.YahooSeasonsMap{startYear: {Leagues: leagues}},
		}
	}

	t.Cleanup(func() {
		viper.Set(config.FlagDayConcurrency, prevConcurrency)
		loadYahooSeasons = prevLoader
	})
}

// startTemporalForReplayTest returns a Temporal client and a cleanup func.
// With PUCKDB_TEST_TEMPORAL_HOSTPORT set it dials that server; otherwise it
// starts an in-process dev server (downloading the Temporal CLI on first run).
// Either failure skips the test rather than failing it.
func startTemporalForReplayTest(t *testing.T) (client.Client, func()) {
	t.Helper()
	if hp := os.Getenv(envTestTemporalHostPort); hp != "" {
		c, err := client.Dial(client.Options{HostPort: hp})
		if err != nil {
			t.Skipf("%s=%q dial failed: %v", envTestTemporalHostPort, hp, err)
		}
		return c, func() { c.Close() }
	}
	ctx, cancel := context.WithTimeout(context.Background(), devServerStartTimeout)
	defer cancel()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
	if err != nil {
		t.Skipf("DevServer start failed: %v (set %s to use an external Temporal)", err, envTestTemporalHostPort)
	}
	return srv.Client(), func() { _ = srv.Stop() }
}

// runSeasonWorkflow runs one season workflow to completion and returns its
// recorded history.
func runSeasonWorkflow(t *testing.T, c client.Client, taskQueue, workflowType, idPrefix string, args ...any) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), seasonWorkflowRunTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        uniqueReplayName(idPrefix),
		TaskQueue: taskQueue,
	}, workflowType, args...)
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil), "%s must complete", workflowType)

	return fetchReplayHistory(t, c, run.GetID(), run.GetRunID())
}

// fetchReplayHistory reads a completed execution's full history into a History
// proto, which is what WorkflowReplayer.ReplayWorkflowHistory consumes.
func fetchReplayHistory(t *testing.T, c client.Client, workflowID, runID string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), seasonWorkflowRunTimeout)
	defer cancel()

	iter := c.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	hist := &historypb.History{}
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		hist.Events = append(hist.Events, event)
	}
	require.NotEmpty(t, hist.Events, "history for %s should not be empty", workflowID)
	return hist
}

func requireActivityScheduledCount(t *testing.T, hist *historypb.History, activityType string, want int) {
	t.Helper()
	got := 0
	for _, e := range hist.Events {
		if e.GetEventType() == enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED &&
			e.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName() == activityType {
			got++
		}
	}
	require.Equal(t, want, got, "%s activities scheduled", activityType)
}

// initialActivityBatchSize returns how many activities of the given type were
// scheduled before the first one of them completed — i.e. the worker pool's
// initial batch, which is the day-concurrency setting the execution ran with.
// All commands from one workflow task land in history together, before any
// activity of that task can complete, so the count is exact.
func initialActivityBatchSize(hist *historypb.History, activityType string) int {
	scheduled := make(map[int64]bool)
	batch := 0
	for _, e := range hist.Events {
		switch e.GetEventType() {
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			if e.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName() == activityType {
				scheduled[e.GetEventId()] = true
				batch++
			}
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if scheduled[e.GetActivityTaskCompletedEventAttributes().GetScheduledEventId()] {
				return batch
			}
		}
	}
	return batch
}

// uniqueReplayName keeps workflow IDs and task queues from colliding across
// runs against a long-lived external Temporal server.
func uniqueReplayName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

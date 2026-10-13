package workflow

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

const (
	seasonReplayHostEnv       = "PUCKDB_TEST_TEMPORAL_HOSTPORT"
	seasonReplayServerTimeout = 90 * time.Second
	seasonReplayRunTimeout    = 30 * time.Second
	seasonReplayFetchName     = "FetchSeasonsWorkflow"
	seasonReplayImportName    = "ImportSeasonsWorkflow"
	// seasonReplayYahooDelay keeps the Yahoo child running while the NHL
	// child starts and finishes, so the recorded history interleaves them.
	seasonReplayYahooDelay = 2 * time.Second
)

func TestSeasonSyncParentsReplayLegacyHistoryWithChangedYahooConfig(t *testing.T) {
	previousLoader := loadYahooSeasons
	defer func() { loadYahooSeasons = previousLoader }()
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return replayYahooSnapshot(preseasonTestYear) }

	temporalClient, stopServer := startSeasonReplayServer(t)
	defer stopServer()
	taskQueue := seasonReplayName("season-sync-replay")
	w := newSeasonReplayWorker(t, temporalClient, taskQueue)
	defer w.Stop()

	input := &model.SeasonsInput{StartSeason: seasonIntPointer(preseasonTestYear), EndSeason: seasonIntPointer(preseasonTestYear)}
	fetchHistory := runSeasonParentForReplay(t, temporalClient, taskQueue, seasonReplayFetchName, input)
	importHistory := runSeasonParentForReplay(t, temporalClient, taskQueue, seasonReplayImportName, input)

	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return replayYahooSnapshot(preseasonTestYear + 1) }
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(FetchSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: seasonReplayFetchName})
	replayer.RegisterWorkflowWithOptions(ImportSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: seasonReplayImportName})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, fetchHistory))
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, importHistory))
}

func replayYahooSnapshot(year int) shared.YahooSeasonsSnapshot {
	return shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{
		year: {Leagues: []config.League{{LeagueID: preseasonTestLeagueID}}},
	}}
}

func newSeasonReplayWorker(t *testing.T, temporalClient client.Client, taskQueue string) worker.Worker {
	t.Helper()
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(replayLegacyFetchSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: seasonReplayFetchName})
	w.RegisterWorkflowWithOptions(replayLegacyImportSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: seasonReplayImportName})
	w.RegisterWorkflowWithOptions(replayFetchYahooChild,
		temporalworkflow.RegisterOptions{Name: "FetchYahooSeasonWorkflow"})
	w.RegisterWorkflowWithOptions(replayImportYahooChild,
		temporalworkflow.RegisterOptions{Name: "ImportYahooSeasonWorkflow"})
	registerSeasonReplayActivities(w, nil)
	require.NoError(t, w.Start())
	return w
}

// A history recorded by the current FetchSeasonsWorkflow, with the Yahoo and
// NHL groups running at the same time over one started season, carries the
// version marker and replays without a nondeterminism error.
func TestFetchSeasonsReplaysConcurrentGroupsHistory(t *testing.T) {
	previousLoader := loadYahooSeasons
	defer func() { loadYahooSeasons = previousLoader }()
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return replayYahooSnapshot(preseasonTestYear) }

	temporalClient, stopServer := startSeasonReplayServer(t)
	defer stopServer()
	taskQueue := seasonReplayName("season-sync-concurrent-replay")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(FetchSeasonsWorkflow, temporalworkflow.RegisterOptions{Name: seasonReplayFetchName})
	w.RegisterWorkflowWithOptions(replaySlowFetchYahooChild,
		temporalworkflow.RegisterOptions{Name: "FetchYahooSeasonWorkflow"})
	w.RegisterWorkflowWithOptions(replayFetchNHLChild, temporalworkflow.RegisterOptions{Name: "FetchNHLSeasonWorkflow"})
	registerSeasonReplayActivities(w, []nhl.SeasonInfo{testSeasonW(preseasonTestYear, "2026-09-29", "2026-10-01")})
	w.RegisterActivityWithOptions(func(context.Context, int) ([]string, error) { return []string{"MTL"}, nil },
		activity.RegisterOptions{Name: "ListSeasonTeams"})
	require.NoError(t, w.Start())
	defer w.Stop()

	input := &model.SeasonsInput{StartSeason: seasonIntPointer(preseasonTestYear), EndSeason: seasonIntPointer(preseasonTestYear)}
	history := runSeasonParentForReplay(t, temporalClient, taskQueue, seasonReplayFetchName, input)
	require.True(t, slices.ContainsFunc(history.Events, func(event *historypb.HistoryEvent) bool {
		return event.GetEventType() == enumspb.EVENT_TYPE_MARKER_RECORDED
	}), "history records the %s version marker", seasonSyncConcurrentGroupsChange)
	nhlStarted := slices.IndexFunc(history.Events, func(event *historypb.HistoryEvent) bool {
		attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes()
		return attrs != nil && attrs.GetWorkflowType().GetName() == "FetchNHLSeasonWorkflow"
	})
	yahooCompleted := slices.IndexFunc(history.Events, func(event *historypb.HistoryEvent) bool {
		attrs := event.GetChildWorkflowExecutionCompletedEventAttributes()
		return attrs != nil && attrs.GetWorkflowType().GetName() == "FetchYahooSeasonWorkflow"
	})
	require.NotEqual(t, -1, nhlStarted)
	require.Less(t, nhlStarted, yahooCompleted, "the NHL child starts before the Yahoo child completes")

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(FetchSeasonsWorkflow, temporalworkflow.RegisterOptions{Name: seasonReplayFetchName})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, history))
}

func replaySlowFetchYahooChild(ctx temporalworkflow.Context, _ YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	if err := temporalworkflow.Sleep(ctx, seasonReplayYahooDelay); err != nil {
		return YahooSeasonSyncResult{}, err
	}
	return YahooSeasonSyncResult{LeagueTotals: []int{1}}, nil
}

func replayFetchNHLChild(temporalworkflow.Context, nhl.SeasonInfo) (core.OriginCounts, error) {
	return core.OriginCounts{}, nil
}

func replayLegacyFetchSeasonsWorkflow(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
	return runLegacySeasonSyncWorkflow(ctx, input, seasonSyncFetch)
}

func replayLegacyImportSeasonsWorkflow(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
	return runLegacySeasonSyncWorkflow(ctx, input, seasonSyncImport)
}

func runLegacySeasonSyncWorkflow(ctx temporalworkflow.Context, input *model.SeasonsInput, mode seasonSyncMode) error {
	input = normalizeSeasonsInput(input)
	if err := ValidateSeasonsInput(input); err != nil {
		return err
	}
	cfg, err := snapshotSeasonSyncConfig(ctx, input)
	if err != nil {
		return err
	}
	tracker, err := shared.InitTracker(ctx, newSeasonSyncProgressReport(mode))
	if err != nil {
		return err
	}
	ctx = temporalworkflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return executeLegacySeasonSync(ctx, tracker, input, cfg, mode)
}

func executeLegacySeasonSync(ctx temporalworkflow.Context, tracker *shared.ReportTracker, input *model.SeasonsInput,
	cfg seasonSyncConfig, mode seasonSyncMode) error {
	rangeLabel := formatSeasonRange(input)
	yahooSeasons := selectYahooSeasons(cfg.Yahoo, input)
	if err := processYahooSeasonGroup(ctx, tracker, yahooSeasons, cfg.Concurrency, rangeLabel, mode); err != nil {
		return err
	}
	seasons, err := loadSeasonsManifest(ctx, temporalworkflow.GetLogger(ctx), input)
	if err != nil {
		return err
	}
	if err := processNHLSeasonGroup(ctx, tracker, seasons, cfg.Concurrency, rangeLabel, mode, ImportNHLSeasonOptions{}); err != nil {
		return err
	}
	if _, err := processUpcomingSeason(ctx, tracker, input, rangeLabel, mode); err != nil {
		return err
	}
	setSeasonSyncOutcome(ctx, tracker, len(yahooSeasons), len(seasons), seasonYearUnset, rangeLabel)
	return nil
}

func replayFetchYahooChild(temporalworkflow.Context, YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	return YahooSeasonSyncResult{LeagueTotals: []int{1}}, nil
}

func replayImportYahooChild(temporalworkflow.Context, YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	return YahooSeasonSyncResult{LeagueTotals: []int{1}}, nil
}

// registerSeasonReplayActivities registers the parents' activities; the
// manifest lists seasons as the started NHL seasons.
func registerSeasonReplayActivities(w worker.Worker, seasons []nhl.SeasonInfo) {
	activities := []struct {
		name string
		fn   any
	}{
		{"Save", func(context.Context, string, string, []byte) error { return nil }},
		{"DeleteBatch", func(context.Context, []string) error { return nil }},
		{"FetchSeasonsManifest", func(context.Context, *model.SeasonsInput) (worknhl.FetchSeasonsManifestResult, error) {
			return worknhl.FetchSeasonsManifestResult{Seasons: seasons}, nil
		}},
		{"FetchUpcomingSeasonRosters", func(context.Context, *model.SeasonsInput) (worknhl.UpcomingSeasonRostersResult, error) {
			return worknhl.UpcomingSeasonRostersResult{Season: preseasonTestYear}, nil
		}},
		{"ImportUpcomingSeasonRosters", func(context.Context, *model.SeasonsInput) (worknhl.UpcomingSeasonRostersResult, error) {
			return worknhl.UpcomingSeasonRostersResult{Season: preseasonTestYear}, nil
		}},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

func startSeasonReplayServer(t *testing.T) (client.Client, func()) {
	t.Helper()
	if hostPort := os.Getenv(seasonReplayHostEnv); hostPort != "" {
		temporalClient, err := client.Dial(client.Options{HostPort: hostPort})
		if err != nil {
			t.Skipf("%s=%q dial failed: %v", seasonReplayHostEnv, hostPort, err)
		}
		return temporalClient, func() { temporalClient.Close() }
	}
	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayServerTimeout)
	defer cancel()
	server, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
	if err != nil {
		t.Skipf("Temporal dev server failed: %v", err)
	}
	return server.Client(), func() { _ = server.Stop() }
}

func runSeasonParentForReplay(t *testing.T, temporalClient client.Client, taskQueue, workflowName string,
	input *model.SeasonsInput) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: seasonReplayName(workflowName), TaskQueue: taskQueue,
	}, workflowName, input)
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))
	return readSeasonReplayHistory(t, temporalClient, run.GetID(), run.GetRunID())
}

func readSeasonReplayHistory(t *testing.T, temporalClient client.Client, workflowID, runID string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	iter := temporalClient.GetWorkflowHistory(ctx, workflowID, runID, false,
		enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	history := &historypb.History{}
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		history.Events = append(history.Events, event)
	}
	require.NotEmpty(t, history.Events)
	return history
}

func seasonIntPointer(value int) *int { return &value }

func seasonReplayName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

package workflow

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

const (
	legacyFetchSeasonsWorkflowType  = "FetchSeasonsWorkflow"
	legacyImportSeasonsWorkflowType = "ImportSeasonsWorkflow"
)

func legacyFetchSeasonsWorkflow(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
	return iterateSeasons(ctx, input, NewFetchSeasonsProgressReport(), GroupFetchSeasonsData,
		daysWithPlayoffTeamsCounter(), WorkflowIDFetchSeason,
		func(n int, elapsed string, counts core.OriginCounts) string { return "legacy fetch complete" },
		func(ctx temporalworkflow.Context, season nhl.SeasonInfo, _ bool) temporalworkflow.ChildWorkflowFuture {
			return temporalworkflow.ExecuteChildWorkflow(
				shared.WithChildOptions(ctx, WorkflowIDFetchSeason(season.ID.StartYear())), FetchSeasonWorkflow, season)
		})
}

func legacyImportSeasonsWorkflow(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
	return ImportSeasonsWorkflowLegacy(ctx, input)
}

// ImportSeasonsWorkflowLegacy is the pre-change parent command sequence kept
// in tests so recorded histories prove the new version gate can replay it.
func ImportSeasonsWorkflowLegacy(ctx temporalworkflow.Context, input *model.SeasonsInput) error {
	logger := temporalworkflow.GetLogger(ctx)
	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}
	tracker, err := shared.InitTracker(ctx, NewImportSeasonsProgressReport())
	if err != nil {
		return err
	}
	ctx = temporalworkflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil || len(seasons) == 0 {
		return err
	}
	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx: GroupImportSeasonsData, Counter: daysWithPlayoffTeamsCounter(),
		SourceKeyFunc: WorkflowIDImportSeason, GroupLabel: "Imported", CountLabel: "cache reads",
	}, func(ctx temporalworkflow.Context, i int) temporalworkflow.Future {
		season := seasons[i]
		return temporalworkflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportSeason(season.ID.StartYear())), ImportSeasonWorkflow, season)
	})
	return err
}

func TestSeasonParentWorkflowsReplayLegacyHistories(t *testing.T) {
	c, stopServer := startTemporalForReplayTest(t)
	defer stopServer()
	taskQueue := uniqueReplayName("season-parent-legacy-tq")
	season := testSeasonW(replaySeasonStartYear, replaySeasonStart, replaySeasonEnd)
	input := &model.SeasonsInput{StartSeason: seasonIntPtr(replaySeasonStartYear), EndSeason: seasonIntPtr(replaySeasonStartYear)}

	w := worker.New(c, taskQueue, worker.Options{})
	registerLegacyParentWorkflows(w, season)
	require.NoError(t, w.Start())
	fetchHistory := runSeasonWorkflow(t, c, taskQueue, legacyFetchSeasonsWorkflowType, "legacy-fetch-parent", input)
	importHistory := runSeasonWorkflow(t, c, taskQueue, legacyImportSeasonsWorkflowType, "legacy-import-parent", input)
	w.Stop()

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FetchSeasonsWorkflow)
	replayer.RegisterWorkflow(ImportSeasonsWorkflow)
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, fetchHistory))
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, importHistory))
}

func registerLegacyParentWorkflows(w worker.Worker, season nhl.SeasonInfo) {
	w.RegisterWorkflowWithOptions(legacyFetchSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: legacyFetchSeasonsWorkflowType})
	w.RegisterWorkflowWithOptions(legacyImportSeasonsWorkflow,
		temporalworkflow.RegisterOptions{Name: legacyImportSeasonsWorkflowType})
	w.RegisterWorkflowWithOptions(func(temporalworkflow.Context, nhl.SeasonInfo) (core.OriginCounts, error) {
		return core.OriginCounts{}, nil
	}, temporalworkflow.RegisterOptions{Name: "FetchSeasonWorkflow"})
	w.RegisterWorkflowWithOptions(func(temporalworkflow.Context, nhl.SeasonInfo) (core.OriginCounts, error) {
		return core.OriginCounts{}, nil
	}, temporalworkflow.RegisterOptions{Name: "ImportSeasonWorkflow"})

	activities := []namedActivity{
		{"Save", func(context.Context, string, string, []byte) error { return nil }},
		{"DeleteBatch", func(context.Context, []string) error { return nil }},
		{"FetchSeasonsManifest", func(context.Context, *model.SeasonsInput) (worknhl.FetchSeasonsManifestResult, error) {
			return worknhl.FetchSeasonsManifestResult{Seasons: []nhl.SeasonInfo{season}}, nil
		}},
		{"ListSeasonTeams", func(context.Context, int) ([]string, error) { return replayTeams, nil }},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

func seasonIntPtr(value int) *int { return &value }

package workflow

import (
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// GroupImportSeasonsData is the single progress group for ImportSeasonsWorkflow.
const GroupImportSeasonsData = 0

// NewImportSeasonsProgressReport creates the initial progress structure for import seasons.
func NewImportSeasonsProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Importing seasons...", Bars: []shared.ProgressBar{}},
		},
	}
}

// ImportSeasonsWorkflow imports season data from cached files into the database.
// Spawns ImportSeasonWorkflow children for day-level imports (boxscores, game stories, shifts, Yahoo data).
func ImportSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}

	tracker, err := shared.InitTracker(ctx, NewImportSeasonsProgressReport())
	if err != nil {
		return err
	}

	logger.Info("ImportSeasonsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		return nil
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      GroupImportSeasonsData,
		Counter:       daysWithPlayoffTeamsCounter(),
		SourceKeyFunc: WorkflowIDImportSeason,
		GroupLabel:    "Imported",
		CountLabel:    "cache reads",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportSeason(season.ID.StartYear())),
			ImportSeasonWorkflow, season)
	})
	return err
}

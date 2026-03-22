package worker

import (
	"github.com/sperano/puckdb/graph/model"
	"go.temporal.io/sdk/workflow"
)

// GroupImportSeasonsData is the single progress group for ImportSeasonsWorkflow.
const GroupImportSeasonsData = 0

// NewImportSeasonsProgressReport creates the initial progress structure for import seasons.
func NewImportSeasonsProgressReport() *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Importing seasons...", Bars: []ProgressBar{}},
		},
	}
}

// ImportSeasonsWorkflow imports season data from cached files into the database.
// Spawns ImportSeasonWorkflow children for day-level imports (boxscores, game stories, shifts, Yahoo data).
func ImportSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	tracker := NewReportTracker(NewImportSeasonsProgressReport())
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	concurrency := resolveConfigInt(logger, seasonConcurrencyParam, input.SeasonConcurrency)
	logger.Info("ImportSeasonsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		return nil
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:    GroupImportSeasonsData,
		Counter:     countDaysInSeason,
		ChildIDFunc: WorkflowIDImportSeason,
		GroupLabel:  "Imported",
		CountLabel:  "cache reads",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			withChildOptions(ctx, WorkflowIDImportSeason(season.ID.StartYear())),
			ImportSeasonWorkflow, season)
	})
	return err
}

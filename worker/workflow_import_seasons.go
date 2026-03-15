package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/graph/model"
	"go.temporal.io/sdk/workflow"
)

// Group indices for ImportSeasonsWorkflow progress.
const (
	GroupImportSeasonsData       = 0
	GroupImportSeasonsPlayerLogs = 1
)

// NewImportSeasonsProgressReport creates the initial progress structure for import seasons.
// Two groups: one for day imports, one for player log imports.
func NewImportSeasonsProgressReport() *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Importing seasons...", Bars: []ProgressBar{}},
			{Header: "Importing player logs...", Bars: []ProgressBar{}},
		},
	}
}

// ImportSeasonsWorkflow imports season data from cached files into the database.
// Phase 1: spawn ImportSeasonWorkflow children for day-level imports.
// Phase 2: spawn ImportSeasonPlayerLogsWorkflow children for player game log imports.
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

	// --- Phase 1: Import days ---
	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:    GroupImportSeasonsData,
		Counter:     countDaysInSeason,
		ChildIDFunc: WorkflowIDImportSeason,
		GroupLabel:  "Imported",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			withChildOptions(ctx, WorkflowIDImportSeason(season.ID.StartYear())),
			ImportSeasonWorkflow, season)
	})
	if err != nil {
		return err
	}

	// --- Phase 2: Import player game logs ---
	startYears := make([]int, len(seasons))
	for i, s := range seasons {
		startYears[i] = s.ID.StartYear()
	}

	var playerAct *PlayerActivities
	var playerCounts map[int]int
	if err := workflow.ExecuteActivity(ctx, playerAct.CountPlayersForAllSeasons, startYears).Get(ctx, &playerCounts); err != nil {
		return fmt.Errorf("count players for all seasons: %w", err)
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:    GroupImportSeasonsPlayerLogs,
		Counter:     playerBatchCounter(playerCounts),
		ChildIDFunc: WorkflowIDImportSeasonPlayerLogs,
		GroupLabel:  "Imported player logs for",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			withChildOptions(ctx, WorkflowIDImportSeasonPlayerLogs(season.ID.StartYear())),
			ImportSeasonPlayerLogsWorkflow, season)
	})
	return err
}

// playerBatchCounter returns a SeasonCounterFunc that computes the number of
// player game log batches for each season from pre-fetched player counts.
func playerBatchCounter(playerCounts map[int]int) SeasonCounterFunc {
	return func(season nhl.SeasonInfo) (int, error) {
		count := playerCounts[season.ID.StartYear()]
		if count == 0 {
			return 0, nil
		}
		return batchCount(count, playerGameLogBatchSize), nil
	}
}

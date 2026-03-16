package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// playerGameLogBatchSize is the number of player IDs processed per ImportPlayerGameLogsBatch activity.
const playerGameLogBatchSize = 50

// GroupImportPlayerLogs is the single progress group for ImportSeasonPlayerLogsWorkflow.
const GroupImportPlayerLogs = 0

// NewImportSeasonPlayerLogsReport creates the progress report for a single season's player log import.
func NewImportSeasonPlayerLogsReport(season nhl.SeasonInfo, playerCount int) *ProgressReport {
	return &ProgressReport{
		Total: playerCount,
		Groups: []ProgressGroup{
			{Header: fmt.Sprintf("Importing player logs for %s...", season.Label()), Bars: []ProgressBar{{Total: playerCount}}},
		},
	}
}

// ImportSeasonPlayerLogsWorkflow imports player game logs for all players in a season.
// Player IDs are loaded from Redis (populated by ExtractBoxscorePlayersWorkflow).
func ImportSeasonPlayerLogsWorkflow(ctx workflow.Context, season nhl.SeasonInfo) error {
	logger := workflow.GetLogger(ctx)

	logger.Info("ImportSeasonPlayerLogsWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Load player IDs from Redis
	var sa *SeasonsActivities
	var playerIDs []int64
	if err := workflow.ExecuteActivity(ctx, sa.CollectSeasonPlayerIDs, season).Get(ctx, &playerIDs); err != nil {
		return fmt.Errorf("collect player IDs: %w", err)
	}

	if len(playerIDs) == 0 {
		logger.Info("No players found for season", "startYear", season.ID.StartYear())
		return nil
	}

	numBatches := batchCount(len(playerIDs), playerGameLogBatchSize)
	dayConcurrency := getDayConcurrency()

	tracker := NewReportTracker(NewImportSeasonPlayerLogsReport(season, len(playerIDs)))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}
	tracker.StartGroup(ctx, GroupImportPlayerLogs)

	logger.Info("Importing player game logs",
		"startYear", season.ID.StartYear(),
		"playerCount", len(playerIDs),
		"batches", numBatches,
		"concurrency", dayConcurrency)

	err := tracker.RunWorkerPoolWithIncrement(ctx, GroupImportPlayerLogs, 0, numBatches, dayConcurrency,
		func(i int) int { return len(batchSlice(playerIDs, i, playerGameLogBatchSize)) },
		func(_ workflow.Context, i int) workflow.Future {
			batch := batchSlice(playerIDs, i, playerGameLogBatchSize)
			input := ImportPlayerGameLogsBatchInput{
				Season:    season.ID,
				PlayerIDs: batch,
			}
			return workflow.ExecuteActivity(ctx, sa.ImportPlayerGameLogsBatch, input)
		}, nil)
	if err != nil {
		return err
	}

	tracker.CompleteGroup(ctx, GroupImportPlayerLogs,
		fmt.Sprintf("Imported game logs for %d players in %s.",
			len(playerIDs), tracker.GetElapsed(ctx, GroupImportPlayerLogs)))

	logger.Info("ImportSeasonPlayerLogsWorkflow completed",
		"startYear", season.ID.StartYear(),
		"players", len(playerIDs))

	return nil
}

// WorkflowIDImportSeasonPlayerLogs returns the workflow ID for a single season's player log import.
func WorkflowIDImportSeasonPlayerLogs(startYear int) string {
	return fmt.Sprintf("import-season-player-logs-%d", startYear)
}

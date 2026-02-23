package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// FetchSeasonPlayerLogsInput contains parameters for the child workflow.
type FetchSeasonPlayerLogsInput struct {
	Season         SeasonInfo
	RefreshCurrent bool // If true, overwrite files for the current season
}

// FetchSeasonPlayerLogsWorkflow fetches player game logs for all players in a season.
// It extracts player IDs from cached boxscores and downloads their game logs.
func FetchSeasonPlayerLogsWorkflow(ctx workflow.Context, input FetchSeasonPlayerLogsInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("FetchSeasonPlayerLogsWorkflow started",
		"startYear", season.StartYear(),
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat),
		"refreshCurrent", input.RefreshCurrent)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Extract player IDs from cached boxscores
	var extraction BoxscoreExtractionResult
	if err := workflow.ExecuteActivity(ctx, ExtractBoxscoreDataForSeasonActivity, season).Get(ctx, &extraction); err != nil {
		return err
	}

	playerCount := len(extraction.Players)
	if playerCount == 0 {
		logger.Warn("No players found for season", "startYear", season.StartYear())
		return nil
	}

	// Convert BoxscorePlayer to player IDs and calculate batch count
	playerIDs := extractPlayerIDs(extraction.Players)
	batchSize := config.DefaultPlayerLogsBatchSize
	numBatches := (len(playerIDs) + batchSize - 1) / batchSize
	concurrency := config.DefaultPlayerLogsBatchConcurrency

	// Track by batch count (each batch = 1 progress unit)
	tracker := NewProgressTracker(numBatches)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	gameTypes := []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()}

	logger.Info("Extracted players from boxscores",
		"startYear", season.StartYear(),
		"playerCount", playerCount,
		"batches", numBatches,
		"concurrency", concurrency)

	// Run batches concurrently - pool increments by 1 per completion
	startYear := season.StartYear()
	refreshCurrent := input.RefreshCurrent
	err := tracker.RunWorkerPool(ctx, numBatches, concurrency, func(_ workflow.Context, batchIdx int) workflow.Future {
		start := batchIdx * batchSize
		end := start + batchSize
		if end > len(playerIDs) {
			end = len(playerIDs)
		}
		batch := playerIDs[start:end]

		activityInput := DownloadPlayerGameLogsInput{
			PlayerIDs:      batch,
			StartYear:      startYear,
			GameTypes:      gameTypes,
			RefreshCurrent: refreshCurrent,
		}
		return workflow.ExecuteActivity(ctx, DownloadPlayerGameLogsActivity, activityInput)
	})
	if err != nil {
		return err
	}

	logger.Info("FetchSeasonPlayerLogsWorkflow completed",
		"startYear", season.StartYear(),
		"players", playerCount)

	return nil
}

// extractPlayerIDs converts BoxscorePlayer slice to player ID slice.
func extractPlayerIDs(players []BoxscorePlayer) []int64 {
	ids := make([]int64, len(players))
	for i, p := range players {
		ids[i] = p.ID
	}
	return ids
}

// WorkflowIDFetchSeasonPlayerLogs returns the workflow ID for a single season's player logs fetch.
func WorkflowIDFetchSeasonPlayerLogs(startYear int) string {
	return fmt.Sprintf("fetch-season-player-logs-%d", startYear)
}

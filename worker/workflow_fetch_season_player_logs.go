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

	// Set up progress tracking for this season
	tracker := NewProgressTracker(playerCount)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	logger.Info("Extracted players from boxscores",
		"startYear", season.StartYear(),
		"playerCount", playerCount)

	// Convert BoxscorePlayer to player IDs
	playerIDs := extractPlayerIDs(extraction.Players)

	// Download game logs in batches
	batchSize := config.DefaultPlayerLandingBatchSize // Reuse existing batch size constant
	gameTypes := []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()}

	for i := 0; i < len(playerIDs); i += batchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := i + batchSize
		if end > len(playerIDs) {
			end = len(playerIDs)
		}
		batch := playerIDs[i:end]

		activityInput := DownloadPlayerGameLogsInput{
			PlayerIDs:      batch,
			StartYear:      season.StartYear(),
			GameTypes:      gameTypes,
			RefreshCurrent: input.RefreshCurrent,
		}

		var result *DownloadPlayerGameLogsResult
		if err := workflow.ExecuteActivity(ctx, DownloadPlayerGameLogsActivity, activityInput).Get(ctx, &result); err != nil {
			return err
		}

		tracker.progress.Completed += len(batch)

		logger.Debug("Batch complete",
			"startYear", season.StartYear(),
			"batch", i/batchSize+1,
			"downloaded", result.Downloaded,
			"cacheHits", result.CacheHits,
			"skipped", result.Skipped)
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

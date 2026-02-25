package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

// FetchSeasonPlayerLogsInput contains parameters for the child workflow.
type FetchSeasonPlayerLogsInput struct {
	Season         SeasonInfo
	RefreshCurrent bool // If true, overwrite files for the current season
}

// FetchSeasonPlayerLogsWorkflow fetches player game logs for all players in a season.
// It uses cached extraction from Redis (populated by parent workflow) or falls back to
// extracting from boxscores if cache is empty.
func FetchSeasonPlayerLogsWorkflow(ctx workflow.Context, input FetchSeasonPlayerLogsInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("FetchSeasonPlayerLogsWorkflow started",
		"startYear", season.StartYear(),
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat),
		"refreshCurrent", input.RefreshCurrent)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Try to get cached extraction from Redis (populated by parent's counting phase)
	var extraction BoxscoreExtractionResult
	if err := workflow.ExecuteActivity(ctx, GetCachedExtractionActivity, season.StartYear()).Get(ctx, &extraction); err != nil {
		return err
	}

	// Fall back to extracting if cache was empty
	if len(extraction.Players) == 0 {
		logger.Info("Extraction cache miss, extracting from boxscores", "startYear", season.StartYear())
		if err := workflow.ExecuteActivity(ctx, ExtractBoxscoreDataForSeasonActivity, season).Get(ctx, &extraction); err != nil {
			return err
		}
	} else {
		logger.Info("Using cached extraction", "startYear", season.StartYear(), "players", len(extraction.Players))
	}

	playerCount := len(extraction.Players)
	if playerCount == 0 {
		logger.Warn("No players found for season", "startYear", season.StartYear())
		return nil
	}

	// Convert BoxscorePlayer to player IDs and calculate batch count
	playerIDs := extractPlayerIDs(extraction.Players)
	batchSize := viper.GetInt(config.FlagPlayerLogsBatchSize)
	if batchSize <= 0 {
		batchSize = config.DefaultPlayerLogsBatchSize
	}
	numBatches := (len(playerIDs) + batchSize - 1) / batchSize
	concurrency := viper.GetInt(config.FlagPlayerLogsBatchConcurrency)
	if concurrency <= 0 {
		concurrency = config.DefaultPlayerLogsBatchConcurrency
	}

	// Track by player count (more intuitive than batch count)
	tracker := NewProgressTracker(playerCount)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	logger.Info("Progress tracker initialized",
		"total", tracker.progress.Total,
		"playerCount", playerCount,
		"numBatches", numBatches,
		"batchSize", batchSize)

	gameTypes := []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()}

	logger.Info("Extracted players from boxscores",
		"startYear", season.StartYear(),
		"playerCount", playerCount,
		"batches", numBatches,
		"concurrency", concurrency)

	// Run batches concurrently
	startYear := season.StartYear()
	refreshCurrent := input.RefreshCurrent

	startActivity := func(_ workflow.Context, batchIdx int) workflow.Future {
		start := batchIdx * batchSize
		end := start + batchSize
		if end > playerCount {
			end = playerCount
		}
		batch := playerIDs[start:end]

		activityInput := DownloadPlayerGameLogsInput{
			PlayerIDs:      batch,
			StartYear:      startYear,
			GameTypes:      gameTypes,
			RefreshCurrent: refreshCurrent,
		}
		return workflow.ExecuteActivity(ctx, DownloadPlayerGameLogsActivity, activityInput)
	}

	handler := func(_ workflow.Context, batchIdx int, f workflow.Future) error {
		var result DownloadPlayerGameLogsResult
		if err := f.Get(ctx, &result); err != nil {
			return err
		}
		tracker.IncrementBy(result.Players)
		return nil
	}

	err := tracker.RunWorkerPoolForItemBy(ctx, numBatches, concurrency, 0, 0, startActivity, handler)
	if err != nil {
		return err
	}

	logger.Info("FetchSeasonPlayerLogsWorkflow completed",
		"startYear", season.StartYear(),
		"players", playerCount)

	return nil
}

// extractPlayerIDs converts BoxscorePlayer slice to player ID slice.
func extractPlayerIDs(players []store.BoxscorePlayer) []int64 {
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

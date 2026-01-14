package worker

import (
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/date"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// BoxscoreDayBatchSize controls how many days are processed per activity
	// to avoid workflow history size limits
	BoxscoreDayBatchSize = 30
)

// ExtractBoxscorePlayersForSeasonWorkflow extracts players from NHL boxscore files for one season.
// Results are stored in Redis and merged at the end to keep workflow history small.
func ExtractBoxscorePlayersForSeasonWorkflow(ctx workflow.Context, season config.Season) (map[int64]PartialPlayer, error) {
	log.Info().Int("season", season.StartYear()).Msg("Boxscore season: starting")

	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		// Season hasn't started yet, return empty map
		log.Info().Int("season", season.StartYear()).Msg("Boxscore season: hasn't started yet, skipping")
		return make(map[int64]PartialPlayer), nil
	}

	// Use longer timeout for batch activities
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Batch days to reduce workflow history size
	batches := batchDates(dateRange, BoxscoreDayBatchSize)
	log.Info().
		Int("season", season.StartYear()).
		Int("days", len(dateRange)).
		Int("batches", len(batches)).
		Msg("Boxscore season: processing in batches")

	// Launch activity for each batch in parallel
	batchFutures := make([]workflow.Future, len(batches))
	for i, batch := range batches {
		input := BatchInput{
			Days:       batch,
			Season:     season.StartYear(),
			BatchIndex: i,
		}
		batchFutures[i] = workflow.ExecuteActivity(ctx, ExtractBoxscorePlayersForDayBatchActivity, input)
	}

	// Collect Redis keys from batch results
	redisKeys := make([]string, 0, len(batches))
	totalPlayers := 0
	for i, future := range batchFutures {
		var result BatchResult
		if err := future.Get(ctx, &result); err != nil {
			log.Warn().Err(err).Int("batch", i).Int("season", season.StartYear()).Msg("Boxscore season: batch failed, continuing")
			continue
		}
		if result.RedisKey != "" {
			redisKeys = append(redisKeys, result.RedisKey)
			totalPlayers += result.PlayerCount
		}
		log.Info().
			Int("season", season.StartYear()).
			Int("batch", i+1).
			Int("total_batches", len(batches)).
			Int("batch_players", result.PlayerCount).
			Msg("Boxscore season: batch complete")
	}

	// Merge all batches from Redis
	var allPlayers map[int64]PartialPlayer
	if err := workflow.ExecuteActivity(ctx, MergePlayerBatchesFromRedisActivity, redisKeys, "boxscore").Get(ctx, &allPlayers); err != nil {
		return nil, err
	}

	log.Info().
		Int("season", season.StartYear()).
		Int("days", len(dateRange)).
		Int("players", len(allPlayers)).
		Msg("Boxscore season: complete")

	return allPlayers, nil
}

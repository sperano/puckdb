package worker

import (
	"time"
)

const (
	// YahooDayBatchSize controls how many days are processed per activity
	// to avoid workflow history size limits
	YahooDayBatchSize = 30
)

/*
// ExtractYahooPlayersForSeasonWorkflow extracts players from Yahoo game files for one season.
// Results are stored in Redis and merged at the end to keep workflow history small.
func ExtractYahooPlayersForSeasonWorkflow(ctx workflow.Context, season config.Season) (map[int64]PartialPlayer, error) {
	log.Info().Int("season", season.StartYear()).Msg("Yahoo season: starting")

	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		// Season hasn't started yet, return empty map
		log.Info().Int("season", season.StartYear()).Msg("Yahoo season: hasn't started yet, skipping")
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
	batches := batchDates(dateRange, YahooDayBatchSize)
	log.Info().
		Int("season", season.StartYear()).
		Int("days", len(dateRange)).
		Int("batches", len(batches)).
		Msg("Yahoo season: processing in batches")

	// Launch activity for each batch in parallel
	batchFutures := make([]workflow.Future, len(batches))
	for i, batch := range batches {
		input := BatchInput{
			Days:       batch,
			Season:     season.StartYear(),
			BatchIndex: i,
		}
		batchFutures[i] = workflow.ExecuteActivity(ctx, ExtractYahooPlayersForDayBatchActivity, input)
	}

	// Collect Redis keys from batch results
	redisKeys := make([]string, 0, len(batches))
	totalPlayers := 0
	for i, future := range batchFutures {
		var result BatchResult
		if err := future.Get(ctx, &result); err != nil {
			log.Warn().Err(err).Int("batch", i).Int("season", season.StartYear()).Msg("Yahoo season: batch failed, continuing")
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
			Msg("Yahoo season: batch complete")
	}

	// Merge all batches from Redis
	var allPlayers map[int64]PartialPlayer
	if err := workflow.ExecuteActivity(ctx, MergePlayerBatchesFromRedisActivity, redisKeys, "yahoo").Get(ctx, &allPlayers); err != nil {
		return nil, err
	}

	log.Info().
		Int("season", season.StartYear()).
		Int("days", len(dateRange)).
		Int("players", len(allPlayers)).
		Msg("Yahoo season: complete")

	return allPlayers, nil
}
*/

// batchDates splits a date range into batches of the given size
func batchDates(dates []time.Time, batchSize int) [][]time.Time {
	batches := make([][]time.Time, 0, (len(dates)+batchSize-1)/batchSize)
	for i := 0; i < len(dates); i += batchSize {
		end := i + batchSize
		if end > len(dates) {
			end = len(dates)
		}
		batches = append(batches, dates[i:end])
	}
	return batches
}

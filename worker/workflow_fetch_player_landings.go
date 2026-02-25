package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// WorkflowIDFetchPlayerLandings is the workflow ID for fetching player landing pages.
	WorkflowIDFetchPlayerLandings = "fetch-player-landings"

	// GroupFetchPlayerLandings is the group index for fetch progress tracking.
	GroupFetchPlayerLandings = 0

	// DefaultFetchLandingsBatchSize is the default number of players per batch.
	DefaultFetchLandingsBatchSize = 50

	// DefaultFetchLandingsConcurrency is the default number of concurrent batch activities.
	DefaultFetchLandingsConcurrency = 10
)

// FetchPlayerLandingsInput contains parameters for the fetch player landings workflow.
type FetchPlayerLandingsInput struct {
	BatchSize   *int // Players per batch activity (default: 50)
	Concurrency *int // Parallel activities (default: 10)
}

// FetchPlayerLandingsResult contains the final result of the workflow.
type FetchPlayerLandingsResult struct {
	TotalPlayers int
	Downloaded   int
	CacheHits    int
	Missing      int
}

// NewFetchPlayerLandingsProgressReport creates the initial progress structure.
func NewFetchPlayerLandingsProgressReport(total int) *ProgressReport {
	return &ProgressReport{
		Total: total,
		Groups: []ProgressGroup{
			{Header: "Fetching player landing pages...", Bars: []ProgressBar{{Label: "Players", Total: total}}},
		},
	}
}

// FetchPlayerLandingsWorkflow loads the consolidated boxscore players from Redis
// and fetches their landing pages in batches.
func FetchPlayerLandingsWorkflow(ctx workflow.Context, input *FetchPlayerLandingsInput) (*FetchPlayerLandingsResult, error) {
	logger := workflow.GetLogger(ctx)

	// Parse configuration
	batchSize := DefaultFetchLandingsBatchSize
	if input != nil && input.BatchSize != nil && *input.BatchSize > 0 {
		batchSize = *input.BatchSize
	}
	concurrency := DefaultFetchLandingsConcurrency
	if input != nil && input.Concurrency != nil && *input.Concurrency > 0 {
		concurrency = *input.Concurrency
	}

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency > 0 && concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	logger.Info("FetchPlayerLandingsWorkflow started",
		"batchSize", batchSize,
		"concurrency", concurrency)

	// Activity options for loading players from Redis
	loadCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumInterval:    time.Minute,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Load consolidated players from Redis
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(loadCtx, LoadAllBoxscorePlayersActivity).Get(ctx, &players); err != nil {
		return nil, fmt.Errorf("load boxscore players from redis: %w", err)
	}

	if len(players) == 0 {
		logger.Info("No players to process")
		return &FetchPlayerLandingsResult{}, nil
	}

	// Set up progress tracker
	tracker := NewReportTracker(NewFetchPlayerLandingsProgressReport(len(players)))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchPlayerLandings)

	// Activity options for batch fetches
	fetchCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryMaxInterval)) * time.Second,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
			MaximumAttempts:    int32(viper.GetInt(config.FlagTemporalRetryMaxAttempts)),
		},
	})

	// Calculate number of batches
	numBatches := (len(players) + batchSize - 1) / batchSize

	// Aggregate results
	result := &FetchPlayerLandingsResult{TotalPlayers: len(players)}

	// Run batches with concurrency control
	err := tracker.RunWorkerPool(ctx, GroupFetchPlayerLandings, 0, numBatches, concurrency,
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batchStart := batchIndex * batchSize
			batchEnd := batchStart + batchSize
			if batchEnd > len(players) {
				batchEnd = len(players)
			}
			batch := players[batchStart:batchEnd]
			return workflow.ExecuteActivity(fetchCtx, FetchPlayerLandingsBatchActivity, batch)
		},
		func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
			var batchResult FetchPlayerLandingsBatchResult
			if err := f.Get(ctx, &batchResult); err != nil {
				return err
			}
			result.Downloaded += batchResult.Downloaded
			result.CacheHits += batchResult.CacheHits
			result.Missing += batchResult.Missing

			// Calculate actual batch size for progress increment
			batchStart := batchIndex * batchSize
			batchEnd := batchStart + batchSize
			if batchEnd > len(players) {
				batchEnd = len(players)
			}
			actualBatchSize := batchEnd - batchStart
			tracker.IncrementBarBy(GroupFetchPlayerLandings, 0, actualBatchSize-1) // -1 because RunWorkerPool already adds 1
			return nil
		})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchPlayerLandings,
		fmt.Sprintf("Fetched %d players (%d new, %d cached, %d missing) in %s.",
			result.TotalPlayers, result.Downloaded, result.CacheHits, result.Missing,
			tracker.GetElapsed(ctx, GroupFetchPlayerLandings)))

	logger.Info("FetchPlayerLandingsWorkflow completed",
		"totalPlayers", result.TotalPlayers,
		"downloaded", result.Downloaded,
		"cacheHits", result.CacheHits,
		"missing", result.Missing)

	return result, nil
}

// LoadAllBoxscorePlayersActivity loads the consolidated player set from Redis.
func LoadAllBoxscorePlayersActivity(ctx context.Context) ([]store.BoxscorePlayer, error) {
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	return cache.LoadAllBoxscorePlayers(ctx, redisClient)
}

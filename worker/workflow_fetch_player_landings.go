package worker

import (
	"fmt"
	"time"

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

)

// FetchPlayerLandingsInput contains parameters for the fetch player landings workflow.
type FetchPlayerLandingsInput struct {
	BatchSize   *int // Players per batch activity (default: 50)
	Concurrency *int // Parallel activities (default: 10)
}

// FetchPlayerLandingsResult contains the final result of the workflow.
type FetchPlayerLandingsResult struct {
	FetchStats
	TotalPlayers int
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
	var batchOverride, concurrencyOverride *int
	if input != nil {
		batchOverride = input.BatchSize
		concurrencyOverride = input.Concurrency
	}
	batchSize := resolveConfigInt(nil, playerLandingBatchSizeParam, batchOverride)
	concurrency := resolveConfigInt(logger, playerLandingConcurrencyParam, concurrencyOverride)

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
	var playerAct *PlayerActivities
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(loadCtx, playerAct.LoadAllBoxscorePlayers).Get(ctx, &players); err != nil {
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
	numBatches := batchCount(len(players), batchSize)

	// Aggregate results
	result := &FetchPlayerLandingsResult{TotalPlayers: len(players)}

	// Run batches with concurrency control
	err := tracker.RunWorkerPoolWithIncrement(ctx, GroupFetchPlayerLandings, 0, numBatches, concurrency,
		func(i int) int { return len(batchSlice(players, i, batchSize)) },
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := batchSlice(players, batchIndex, batchSize)
			return workflow.ExecuteActivity(fetchCtx, playerAct.FetchPlayerLandingsBatch, batch)
		},
		func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
			var batchResult FetchStats
			if err := f.Get(ctx, &batchResult); err != nil {
				return err
			}
			result.Add(batchResult)
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


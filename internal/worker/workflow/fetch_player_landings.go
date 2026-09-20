package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/log"
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
	shared.FetchStats
	TotalPlayers int
}

// fetchPlayerLandingsConfig is the configuration FetchPlayerLandingsWorkflow
// snapshots once at start: batch size and concurrency decide how many
// FetchPlayerLandingsBatch activities get scheduled, so neither may be
// re-read on replay.
type fetchPlayerLandingsConfig struct {
	BatchSize   int `json:"batchSize"`
	Concurrency int `json:"concurrency"`
}

// loadFetchPlayerLandingsConfig resolves the batch size and concurrency for
// FetchPlayerLandingsWorkflow, honouring the same input overrides used today.
func loadFetchPlayerLandingsConfig(logger log.Logger, batchOverride, concurrencyOverride *int) fetchPlayerLandingsConfig {
	return fetchPlayerLandingsConfig{
		BatchSize:   shared.ResolveConfigInt(nil, shared.PlayerLandingBatchSizeParam, batchOverride),
		Concurrency: shared.ResolveConfigInt(logger, shared.PlayerLandingConcurrencyParam, concurrencyOverride),
	}
}

// snapshotFetchPlayerLandingsConfig records fetchPlayerLandingsConfig in
// history once per execution (see shared.SnapshotConfig).
func snapshotFetchPlayerLandingsConfig(ctx workflow.Context, logger log.Logger, batchOverride, concurrencyOverride *int) (fetchPlayerLandingsConfig, error) {
	return shared.SnapshotConfig(ctx, func() fetchPlayerLandingsConfig {
		return loadFetchPlayerLandingsConfig(logger, batchOverride, concurrencyOverride)
	})
}

// NewFetchPlayerLandingsProgressReport creates the initial progress structure.
func NewFetchPlayerLandingsProgressReport(total int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{
			{Header: "Fetching player landing pages...", Bars: []shared.ProgressBar{{Label: "Players", Total: total}}},
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
	cfg, err := snapshotFetchPlayerLandingsConfig(ctx, logger, batchOverride, concurrencyOverride)
	if err != nil {
		return nil, err
	}
	batchSize := cfg.BatchSize
	concurrency := cfg.Concurrency

	logger.Info("FetchPlayerLandingsWorkflow started",
		"batchSize", batchSize,
		"concurrency", concurrency)

	// Activity options for loading players from Redis
	loadCtx := workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Load consolidated players from Redis
	var playerAct *workplayer.Activities
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(loadCtx, playerAct.LoadAllBoxscorePlayers).Get(ctx, &players); err != nil {
		return nil, fmt.Errorf("load boxscore players from redis: %w", err)
	}

	if len(players) == 0 {
		logger.Info("No players to process")
		return &FetchPlayerLandingsResult{}, nil
	}

	// Set up progress tracker
	tracker, err := shared.InitTracker(ctx, NewFetchPlayerLandingsProgressReport(len(players)))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchPlayerLandings)

	fetchCtx := workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Calculate number of batches
	numBatches := shared.BatchCount(len(players), batchSize)

	// Aggregate results
	result := &FetchPlayerLandingsResult{TotalPlayers: len(players)}

	// Run batches with concurrency control
	err = tracker.RunWorkerPoolWithIncrement(ctx, GroupFetchPlayerLandings, 0, numBatches, concurrency,
		func(i int) int { return len(shared.BatchSlice(players, i, batchSize)) },
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := shared.BatchSlice(players, batchIndex, batchSize)
			return workflow.ExecuteActivity(fetchCtx, playerAct.FetchPlayerLandingsBatch, batch)
		},
		shared.AggregateInto[shared.FetchStats](&result.FetchStats))
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

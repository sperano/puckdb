package worker

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowIDExtractUniquePlayers = "extract-unique-players"
	EnrichmentBatchSize            = 50 // players per batch for API calls
)

func WorkflowIDExtractPlayersForSeason(season int) string {
	return fmt.Sprintf("extract-players-season-%d", season)
}

func WorkflowIDExtractBoxscorePlayersForSeason(season int) string {
	return fmt.Sprintf("extract-boxscore-players-season-%d", season)
}

func WorkflowIDEnrichPlayers() string {
	return "enrich-players"
}

/*
// ExtractUniquePlayersWorkflow orchestrates the entire player extraction process.
// It extracts players from all Yahoo games and NHL boxscores in parallel,
// merges the results, enriches with NHL API data, and saves to the database.
// Returns the total count of players saved.
func ExtractUniquePlayersWorkflow(ctx workflow.Context) (int, error) {
	log.Info().Msg("Starting ExtractUniquePlayersWorkflow")

	// Load all seasons from config
	seasons, err := config.GetSeasonsConfig()
	if err != nil {
		return 0, fmt.Errorf("loading seasons config: %w", err)
	}

	// Progress: seasons + merge + enrichment
	total := len(seasons) + 2
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return 0, err
	}

	// Phase 1: Extract players from all seasons in parallel
	childOpts := workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 30 * time.Minute,
	}

	seasonFutures := make([]workflow.Future, len(seasons))
	for i, season := range seasons {
		ctxChild := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowExecutionTimeout: childOpts.WorkflowExecutionTimeout,
			WorkflowID:               WorkflowIDExtractPlayersForSeason(season.StartYear()),
		})
		future := workflow.ExecuteChildWorkflow(
			ctxChild,
			ExtractPlayersForSeasonWorkflow,
			season,
		)
		seasonFutures[i] = future
	}

	// Collect results from all seasons, fail fast on first error
	// Each child returns a BatchResult with Redis key (not the full player map)
	seasonResults := make([]BatchResult, len(seasons))
	if err := tracker.WaitAllWithResults(ctx, seasonFutures, seasonResults); err != nil {
		return 0, fmt.Errorf("season extraction: %w", err)
	}
	seasonKeys := make([]string, len(seasonResults))
	for i, result := range seasonResults {
		seasonKeys[i] = result.RedisKey
		log.Info().Int("season", seasons[i].StartYear()).Int("players", result.PlayerCount).Msg("Season extraction complete")
	}

	// Phase 2: Merge all seasons from Redis into single map
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	var mergedPlayers map[int64]PartialPlayer
	if err := workflow.ExecuteActivity(ctx, MergeAllSeasonsFromRedisActivity, seasonKeys).Get(ctx, &mergedPlayers); err != nil {
		return 0, fmt.Errorf("merging seasons: %w", err)
	}
	tracker.Increment()
	log.Info().Int("total_unique_players", len(mergedPlayers)).Msg("All seasons merged")

	// Store merged players in Redis for enrichment activities
	var enrichmentKey string
	if err := workflow.ExecuteActivity(ctx, StoreEnrichmentPlayersActivity, mergedPlayers).Get(ctx, &enrichmentKey); err != nil {
		return 0, fmt.Errorf("storing enrichment players: %w", err)
	}

	// Phase 3: Enrich players with NHL API data
	playerIDs := make([]int64, 0, len(mergedPlayers))
	for id := range mergedPlayers {
		playerIDs = append(playerIDs, id)
	}
	sort.Slice(playerIDs, func(i, j int) bool { return playerIDs[i] < playerIDs[j] })

	ctxEnrich := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 60 * time.Minute, // Enrichment may take longer due to API calls
		WorkflowID:               WorkflowIDEnrichPlayers(),
	})

	enrichFuture := workflow.ExecuteChildWorkflow(
		ctxEnrich,
		EnrichPlayersWorkflow,
		playerIDs,
		enrichmentKey,
	)

	var savedCount int
	if err := enrichFuture.Get(ctx, &savedCount); err != nil {
		return 0, fmt.Errorf("enrichment: %w", err)
	}
	tracker.Increment()

	log.Info().Int("final_count", savedCount).Msg("ExtractUniquePlayersWorkflow complete")
	return savedCount, nil
}
*/

/*
// ExtractPlayersForSeasonWorkflow extracts players from both Yahoo and boxscores for one season.
// It runs Yahoo and Boxscore extraction in parallel, merges the results, and stores in Redis.
// Returns a BatchResult with the Redis key to avoid large serialization payloads.
func ExtractPlayersForSeasonWorkflow(ctx workflow.Context, season config.Season) (BatchResult, error) {
	log.Info().Int("season", season.StartYear()).Msg("Starting season extraction")

	childOpts := workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 20 * time.Minute,
	}

	// Launch Yahoo and Boxscore extraction in PARALLEL
	ctxYahoo := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: childOpts.WorkflowExecutionTimeout,
		WorkflowID:               WorkflowIDExtractYahooPlayersForSeason(season.StartYear()),
	})
	yahooFuture := workflow.ExecuteChildWorkflow(ctxYahoo, ExtractYahooPlayersForSeasonWorkflow, season)

	ctxBoxscore := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: childOpts.WorkflowExecutionTimeout,
		WorkflowID:               WorkflowIDExtractBoxscorePlayersForSeason(season.StartYear()),
	})
	boxscoreFuture := workflow.ExecuteChildWorkflow(ctxBoxscore, ExtractBoxscorePlayersForSeasonWorkflow, season)

	// Wait for both, fail fast on first error
	results := make([]map[int64]PartialPlayer, 2)
	if err := waitAllWithResults(ctx, []workflow.Future{yahooFuture, boxscoreFuture}, results); err != nil {
		return BatchResult{}, err
	}
	yahooPlayers, boxscorePlayers := results[0], results[1]

	// Merge Yahoo + Boxscore for this season
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	var merged map[int64]PartialPlayer
	if err := workflow.ExecuteActivity(ctx, MergeSeasonPlayersActivity, yahooPlayers, boxscorePlayers).Get(ctx, &merged); err != nil {
		return BatchResult{}, fmt.Errorf("merge: %w", err)
	}

	// Store merged result in Redis to avoid large serialization in parent workflow
	var result BatchResult
	if err := workflow.ExecuteActivity(ctx, StoreSeasonResultActivity, season, merged).Get(ctx, &result); err != nil {
		return BatchResult{}, fmt.Errorf("store season result: %w", err)
	}

	log.Info().
		Int("season", season.StartYear()).
		Int("yahoo", len(yahooPlayers)).
		Int("boxscore", len(boxscorePlayers)).
		Int("merged", len(merged)).
		Str("redis_key", result.RedisKey).
		Msg("Season extraction complete")

	return result, nil
}
*/

// EnrichPlayersWorkflow enriches partial players with NHL API data.
// It processes players in batches to parallelize API calls.
// The partialPlayersKey is a Redis key where partial players data is stored.
// Players are saved to the database by each batch activity; returns total count saved.
func EnrichPlayersWorkflow(
	ctx workflow.Context,
	playerIDs []int64,
	partialPlayersKey string,
) (int, error) {
	// Split into batches
	batches := splitIntoBatches(playerIDs, EnrichmentBatchSize)

	log.Info().
		Int("players", len(playerIDs)).
		Int("batches", len(batches)).
		Int("batch_size", EnrichmentBatchSize).
		Str("redis_key", partialPlayersKey).
		Msg("Enrichment: starting")

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute, // API calls take longer
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Launch all batches in parallel - each activity fetches partial players from Redis
	batchFutures := make([]workflow.Future, len(batches))
	for i, batch := range batches {
		batchFutures[i] = workflow.ExecuteActivity(ctx, EnrichPlayerBatchActivity, batch, partialPlayersKey)
	}

	// Collect results (counts), fail fast on first error
	totalSaved := 0
	for i, future := range batchFutures {
		var batchCount int
		if err := future.Get(ctx, &batchCount); err != nil {
			return 0, fmt.Errorf("batch enrichment: %w", err)
		}
		totalSaved += batchCount
		log.Info().
			Int("batch", i+1).
			Int("total_batches", len(batches)).
			Int("batch_saved", batchCount).
			Msg("Enrichment: batch complete")
	}

	log.Info().
		Int("total_saved", totalSaved).
		Msg("Enrichment: complete")

	return totalSaved, nil
}

func splitIntoBatches(ids []int64, batchSize int) [][]int64 {
	batches := make([][]int64, 0, (len(ids)+batchSize-1)/batchSize)
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batches = append(batches, ids[i:end])
	}
	return batches
}

// waitAllWithResults waits for all futures using a Selector, returning immediately on the first error.
// Results are stored in the results slice at the corresponding index.
// The results slice must be pre-allocated with the same length as futures.
func waitAllWithResults[T any](ctx workflow.Context, futures []workflow.Future, results []T) error {
	if len(futures) == 0 {
		return nil
	}

	selector := workflow.NewSelector(ctx)
	var firstErr error

	for i, f := range futures {
		idx := i
		future := f
		selector.AddFuture(future, func(f workflow.Future) {
			if err := f.Get(ctx, &results[idx]); err != nil && firstErr == nil {
				firstErr = err
			}
		})
	}

	for range futures {
		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

package worker

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const EnrichmentBatchSize = 50 // players per batch for API calls

func WorkflowIDEnrichPlayers() string {
	return "enrich-players"
}

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

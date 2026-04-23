package shared

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
)

// SaveProgressReportActivity saves a JSON-serialized ProgressReport to Redis.
// Called by workflows at key structural changes so the resolver can read progress
// without querying Temporal.
func SaveProgressReportActivity(ctx context.Context, workflowID string, reportJSON []byte) error {
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close redis client")
		}
	}()

	return cache.SaveProgressReport(ctx, redisClient, workflowID, reportJSON)
}

// LoadProgressReportActivity loads a gob-encoded ProgressReport from Redis.
// Returns nil bytes if no report exists for the given workflow ID.
func LoadProgressReportActivity(ctx context.Context, workflowID string) ([]byte, error) {
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close redis client")
		}
	}()
	return cache.LoadProgressReport(ctx, redisClient, workflowID)
}

// DeleteProgressReportBatchActivity deletes progress reports for multiple workflows.
// Called at workflow start to clear stale reports from previous runs.
func DeleteProgressReportBatchActivity(ctx context.Context, workflowIDs []string) error {
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close redis client")
		}
	}()
	return cache.DeleteProgressReportBatch(ctx, redisClient, workflowIDs)
}

package worker

import (
	"context"

	"github.com/sperano/puckdb/cache"
)

// ClearProgressActivity deletes progress keys from Redis for the given workflow IDs.
// Called at workflow start to clear stale progress from previous runs.
func ClearProgressActivity(ctx context.Context, workflowIDs []string) error {
	if len(workflowIDs) == 0 {
		return nil
	}

	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	return cache.DeleteProgressBatch(ctx, redisClient, workflowIDs)
}

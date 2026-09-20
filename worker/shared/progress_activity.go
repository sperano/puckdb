package shared

import (
	"context"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
)

// ProgressActivities groups local activities that persist ProgressReport state to
// Redis. It holds a shared *redis.Client so each invocation reuses the same
// connection pool — these activities are called frequently (every group
// start/complete, every bar increment), so opening a fresh client per call
// would churn through Redis connections.
type ProgressActivities struct {
	RedisClient *redis.Client
}

// Save persists a gob-encoded ProgressReport to Redis, stamped with the
// calling execution chain's FirstRunID so the API's post-start cleanup can
// tell this run's report from a previous run's. Called by workflows at key
// structural changes so the resolver can read progress without querying
// Temporal.
func (a *ProgressActivities) Save(ctx context.Context, workflowID, runID string, data []byte) error {
	return cache.SaveProgressReport(ctx, a.RedisClient, workflowID, runID, data)
}

// Load retrieves a gob-encoded ProgressReport from Redis. Returns nil bytes if
// no report exists for the given workflow ID.
func (a *ProgressActivities) Load(ctx context.Context, workflowID string) ([]byte, error) {
	return cache.LoadProgressReport(ctx, a.RedisClient, workflowID)
}

// DeleteBatch removes progress reports for multiple workflows. Called at
// workflow start to clear stale reports from previous runs.
func (a *ProgressActivities) DeleteBatch(ctx context.Context, workflowIDs []string) error {
	return cache.DeleteProgressReportBatch(ctx, a.RedisClient, workflowIDs)
}

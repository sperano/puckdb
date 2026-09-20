package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
)

const (
	// ProgressTTL is the expiration time for progress data in Redis.
	ProgressTTL = 1 * time.Hour

	// ProgressReportKeyPrefix is the prefix for full ProgressReport data.
	// v2: the value is a hash (see progressFieldRun / progressFieldData);
	// v1 keys were plain strings, and sharing a prefix would WRONGTYPE
	// against them for up to ProgressTTL after a rolling deploy. They
	// expire on their own.
	ProgressReportKeyPrefix = "puckdb:report:v2:"

	// progressFieldRun holds the FirstRunID of the execution chain that
	// wrote the report — the identity Temporal preserves across
	// ContinueAsNew, so a chain's saves all carry the same stamp. Empty
	// for reports published from activities, which are only ever cleaned
	// up unconditionally by their parent workflow. Kept as a hash field
	// so DeleteStaleProgressReport can compare it server-side without
	// decoding the gob payload.
	progressFieldRun = "run"
	// progressFieldData holds the gob-encoded ProgressReport.
	progressFieldData = "data"
)

// deleteStaleProgressScript deletes KEYS[1] unless its progressFieldRun
// equals ARGV[1]. A missing key has no stamp, so DEL runs and returns 0.
// Runs atomically on the server: a save that lands between the read and
// the delete of a two-step implementation would be wiped; here it can't
// be.
const deleteStaleProgressScript = `
if redis.call('HGET', KEYS[1], '` + progressFieldRun + `') == ARGV[1] then
	return 0
end
return redis.call('DEL', KEYS[1])
`

// progressClient is the minimal Redis surface used by the simple progress
// functions in this file. LoadProgressReportBatch keeps *redis.Client because
// it uses Pipeline, which exposes a much wider surface — narrowing it would
// hide the wider dependency without removing it.
type progressClient interface {
	HGet(ctx context.Context, key, field string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	TxPipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
}

// progressReportKey returns the Redis key holding a workflow's progress report.
func progressReportKey(workflowID string) string {
	return ProgressReportKeyPrefix + workflowID
}

// SaveProgressReport stores a gob-encoded ProgressReport in Redis, stamped
// with runID (the saving execution chain's FirstRunID; "" for activity-
// published reports). Used by parent workflows so the resolver can read
// structural progress without querying Temporal (which requires the worker
// to replay history).
//
// HSET and EXPIRE run in one MULTI/EXEC block so a report can never be
// left without a TTL.
func SaveProgressReport(ctx context.Context, client progressClient, workflowID, runID string, data []byte) error {
	key := progressReportKey(workflowID)
	_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key, progressFieldRun, runID, progressFieldData, data)
		pipe.Expire(ctx, key, ProgressTTL)
		return nil
	})
	if err != nil {
		return fmt.Errorf("save progress report to redis: %w", err)
	}
	return nil
}

// LoadProgressReport loads a gob-encoded ProgressReport from Redis.
// Returns nil, nil if the key doesn't exist.
func LoadProgressReport(ctx context.Context, client progressClient, workflowID string) ([]byte, error) {
	data, err := client.HGet(ctx, progressReportKey(workflowID), progressFieldData).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load progress report from redis: %w", err)
	}
	return data, nil
}

// DeleteStaleProgressReport removes the workflow's progress report unless
// it was written by the execution chain identified by liveRunID. Called by
// the API right after ExecuteWorkflow accepts a start: the worker may
// already have saved the new run's first report by then, and that report
// must survive while anything from an earlier run must go. Returns true
// when a report was deleted.
func DeleteStaleProgressReport(ctx context.Context, client progressClient, workflowID, liveRunID string) (bool, error) {
	if liveRunID == "" {
		// An empty stamp is what activity-published reports carry; matching
		// it would keep exactly the reports this call exists to clear.
		return false, errors.New("delete stale progress report: live run ID is required")
	}
	deleted, err := client.Eval(ctx, deleteStaleProgressScript,
		[]string{progressReportKey(workflowID)}, liveRunID).Int64()
	if err != nil {
		return false, fmt.Errorf("delete stale progress report from redis: %w", err)
	}
	return deleted > 0, nil
}

// DeleteProgressReportBatch removes progress reports for multiple workflows in
// a single Redis call, regardless of which run wrote them. Only safe where
// the caller is ordered before any writer of those keys — the parent
// workflow clearing its children's keys before it spawns them.
func DeleteProgressReportBatch(ctx context.Context, client progressClient, workflowIDs []string) error {
	if len(workflowIDs) == 0 {
		return nil
	}
	keys := make([]string, len(workflowIDs))
	for i, id := range workflowIDs {
		keys[i] = progressReportKey(id)
	}
	return client.Del(ctx, keys...).Err()
}

// LoadProgressReportBatch loads progress reports for multiple workflows in a single Redis round-trip.
// Returns a map of workflowID -> gob-encoded ProgressReport bytes. Missing keys are omitted.
func LoadProgressReportBatch(ctx context.Context, client *redis.Client, workflowIDs []string) (map[string][]byte, error) {
	if len(workflowIDs) == 0 {
		return make(map[string][]byte), nil
	}

	pipe := client.Pipeline()
	cmds := make([]*redis.StringCmd, len(workflowIDs))
	for i, id := range workflowIDs {
		cmds[i] = pipe.HGet(ctx, progressReportKey(id), progressFieldData)
	}
	// pipe.Exec returns the first command error, which is redis.Nil when a key
	// is merely missing — a normal miss, not a failure. Any other error (e.g.
	// Redis unreachable) is kept and inspected after building the result below.
	_, execErr := pipe.Exec(ctx)

	result := make(map[string][]byte, len(workflowIDs))
	for i, cmd := range cmds {
		data, err := cmd.Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			log.Warn().Err(err).Str("workflowID", workflowIDs[i]).Msg("Failed to get progress report")
			continue
		}
		result[workflowIDs[i]] = data
	}

	// If the pipeline failed for a reason other than missing keys and we read
	// nothing, surface the error: an empty map here would be indistinguishable
	// from "no reports exist", masking a Redis outage.
	if len(result) == 0 && execErr != nil && !errors.Is(execErr, redis.Nil) {
		return nil, fmt.Errorf("load progress report batch from redis: %w", execErr)
	}

	return result, nil
}

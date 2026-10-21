package player

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
)

// yahooPoolRebuildChunk is how many Yahoo player files a pool rebuild parses
// between two heartbeats.
const yahooPoolRebuildChunk = 200

// EnsureYahooIDPoolInput is the pool size phase 1 of a ProcessPlayers chain
// measured after saving it (SaveYahooIDPoolResult.PoolSize).
type EnsureYahooIDPoolInput struct {
	PoolSize int
}

// EnsureYahooIDPoolResult reports what EnsureYahooIDPool found and did.
type EnsureYahooIDPoolResult struct {
	FoundSize         int  // pool hash size before any rebuild
	Rebuilt           bool // the pool was missing or incomplete and was rebuilt
	AvailableRestored bool // the hash was complete but the available set was lost, and was restored (not set when it was only empty)
	PoolSize          int  // pool hash size now; the next check compares against it
	Available         int  // available set size after a rebuild or restore (0 otherwise)
	ReadErrors        int  // Yahoo player files a rebuild could not read
	ParseErrors       int  // Yahoo player files a rebuild could not parse
}

// EnsureYahooIDPool keeps the Yahoo ID pool of a ProcessPlayers chain alive
// across executions. The pool keys expire after ImportPlayersTTL and a Redis
// flush drops them; a missing pool makes LoadYahooIDPool return an empty map
// without an error, so phase 2 would stop matching silently. When the pool
// hash still has the size phase 1 measured, both keys' TTL is refreshed.
// Otherwise the pool is rebuilt from the stored Yahoo player files, and the
// Yahoo IDs already matched in the database are removed from the available
// set. During a run entries are only removed from the available set, never
// from the hash, so the hash size is a complete check of the hash. The
// available set is only checked for existence (see restoreYahooIDAvailable).
// Concurrent rebuilds write the same values and converge. A rebuilt
// available set leaves out every Yahoo ID matched in the database, also
// those matched by earlier runs that this run would not match again; phase 3
// then does not report those as unmatched.
func (a *Activities) EnsureYahooIDPool(ctx context.Context, input EnsureYahooIDPoolInput) (EnsureYahooIDPoolResult, error) {
	pipe := a.RedisClient.Pipeline()
	found := pipe.HLen(ctx, YahooIDPoolKey)
	availableKeys := pipe.Exists(ctx, YahooIDAvailableKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return EnsureYahooIDPoolResult{}, fmt.Errorf("measure yahoo id pool: %w", err)
	}
	result := EnsureYahooIDPoolResult{FoundSize: int(found.Val()), PoolSize: int(found.Val())}
	if result.FoundSize != input.PoolSize {
		activity.GetLogger(ctx).Warn("Yahoo ID pool lost, rebuilding", "expected", input.PoolSize, "found", result.FoundSize)
		return a.rebuildYahooIDPool(ctx, result, input.PoolSize)
	}

	if availableKeys.Val() == 0 {
		available, err := a.restoreYahooIDAvailable(ctx)
		if err != nil {
			return result, err
		}
		result.AvailableRestored = available > 0
		result.Available = available
	}
	return result, a.refreshYahooIDPoolTTL(ctx)
}

// refreshYahooIDPoolTTL pushes both pool keys' expiry back to
// ImportPlayersTTL.
func (a *Activities) refreshYahooIDPoolTTL(ctx context.Context) error {
	pipe := a.RedisClient.Pipeline()
	pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
	pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("refresh yahoo id pool ttl: %w", err)
	}
	return nil
}

// restoreYahooIDAvailable rebuilds the available set from the pool hash when
// the hash survived but the set did not (an eviction can drop one key and
// keep the other). Phase 2 matches against the hash only; phase 3 reports
// the available set as unmatched, so a lost set would make it verify
// nothing. Redis also deletes the set once its last member is removed, so a
// missing set can be legitimately empty: the restore then writes nothing
// and costs a hash key listing and a database read, without parsing any
// file. The set gets the hash's Yahoo IDs minus the verified non-NHL ones
// (as phase 1 leaves out) and minus those matched in the database.
func (a *Activities) restoreYahooIDAvailable(ctx context.Context) (int, error) {
	ids, err := a.RedisClient.HKeys(ctx, YahooIDPoolKey).Result()
	if err != nil {
		return 0, fmt.Errorf("list yahoo id pool: %w", err)
	}
	excluded, err := LoadVerifiedNonNHLIDs(ctx, a.RedisClient)
	if err != nil {
		return 0, err
	}
	matched, err := a.Manifests.MatchedYahooIDs(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range matched {
		excluded[id] = struct{}{}
	}
	available := make([]any, 0, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		if _, skip := excluded[store.YahooPlayerID(id)]; !skip {
			available = append(available, idStr)
		}
	}
	if len(available) == 0 {
		activity.GetLogger(ctx).Debug("Yahoo ID available set empty: every pooled Yahoo ID is matched or verified non-NHL")
		return 0, nil
	}
	if err := a.RedisClient.SAdd(ctx, YahooIDAvailableKey, available...).Err(); err != nil {
		return 0, fmt.Errorf("restore yahoo id available set: %w", err)
	}
	activity.GetLogger(ctx).Warn("Yahoo ID available set missing, restored from the pool", "available", len(available))
	return len(available), nil
}

// rebuildYahooIDPool writes the pool again from the stored Yahoo player
// files, then removes the Yahoo IDs already matched in the database from the
// available set. It never deletes the keys first: several batches may
// rebuild at once, and a batch reading between a delete and the next write
// would match against an empty pool. Writing the same entries over each
// other converges. A rebuild that leaves the hash smaller than expectedSize
// (the size phase 1 measured; files phase 1 read could not be read or
// parsed this time) fails, so it is retried instead of matching against a
// partial pool.
func (a *Activities) rebuildYahooIDPool(ctx context.Context, result EnsureYahooIDPoolResult, expectedSize int) (EnsureYahooIDPoolResult, error) {
	parsed, err := a.parseAllYahooPlayers(ctx)
	if err != nil {
		return result, err
	}
	result.ReadErrors, result.ParseErrors = parsed.ReadErrors, parsed.ParseErrors
	saved, err := saveYahooPlayersToRedisImpl(ctx, a.RedisClient, parsed.Players)
	if err != nil {
		return result, err
	}
	if saved.PoolSize < expectedSize {
		return result, fmt.Errorf("rebuilt yahoo id pool holds %d of the %d entries phase 1 saved (%d files unreadable, %d unparsable)",
			saved.PoolSize, expectedSize, parsed.ReadErrors, parsed.ParseErrors)
	}
	if parsed.ReadErrors > 0 || parsed.ParseErrors > 0 {
		activity.GetLogger(ctx).Warn("Some Yahoo player files failed during the pool rebuild",
			"readErrors", parsed.ReadErrors, "parseErrors", parsed.ParseErrors)
	}
	matched, err := a.Manifests.MatchedYahooIDs(ctx)
	if err != nil {
		return result, err
	}
	if err := RemoveFromYahooIDPool(ctx, a.RedisClient, matched); err != nil {
		return result, err
	}

	available, err := a.RedisClient.SCard(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return result, fmt.Errorf("measure rebuilt yahoo id available set: %w", err)
	}
	result.Rebuilt = true
	result.PoolSize = saved.PoolSize
	result.Available = int(available)
	return result, nil
}

// parseAllYahooPlayers lists and parses every stored Yahoo player file,
// heartbeating between chunks, and counts the files it could not read or
// parse.
func (a *Activities) parseAllYahooPlayers(ctx context.Context) (ParseYahooPlayerBatchResult, error) {
	ids, err := listYahooPlayers(ctx, a.Storage)
	if err != nil {
		return ParseYahooPlayerBatchResult{}, fmt.Errorf("list yahoo player files: %w", err)
	}
	all := ParseYahooPlayerBatchResult{Players: make([]store.YahooPlayer, 0, len(ids))}
	for start := 0; start < len(ids); start += yahooPoolRebuildChunk {
		if err := ctx.Err(); err != nil {
			return ParseYahooPlayerBatchResult{}, err
		}
		activity.RecordHeartbeat(ctx, start)
		end := min(start+yahooPoolRebuildChunk, len(ids))
		chunk := parseYahooPlayerBatchImpl(ctx, a.Storage, a.GobCache, ids[start:end])
		all.Players = append(all.Players, chunk.Players...)
		all.ReadErrors += chunk.ReadErrors
		all.ParseErrors += chunk.ParseErrors
	}
	return all, nil
}

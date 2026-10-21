package player

import (
	"context"
	"fmt"

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
	FoundSize int  // pool hash size before any rebuild
	Rebuilt   bool // the pool was missing or incomplete and was rebuilt
	PoolSize  int  // pool hash size now; the next check compares against it
	Available int  // available set size after a rebuild (0 when not rebuilt)
}

// EnsureYahooIDPool keeps the Yahoo ID pool of a ProcessPlayers chain alive
// across executions. The pool keys expire after ImportPlayersTTL and a Redis
// flush drops them; a missing pool makes LoadYahooIDPool return an empty map
// without an error, so phase 2 would stop matching silently. When the pool
// hash still has the size phase 1 measured, both keys' TTL is refreshed.
// Otherwise the pool is rebuilt from the stored Yahoo player files, and the
// Yahoo IDs already matched in the database are removed from the available
// set. During a run entries are only removed from the available set, never
// from the hash, so the hash size is a complete check. Concurrent rebuilds
// write the same values and converge.
func (a *Activities) EnsureYahooIDPool(ctx context.Context, input EnsureYahooIDPoolInput) (EnsureYahooIDPoolResult, error) {
	found, err := a.RedisClient.HLen(ctx, YahooIDPoolKey).Result()
	if err != nil {
		return EnsureYahooIDPoolResult{}, fmt.Errorf("measure yahoo id pool: %w", err)
	}
	result := EnsureYahooIDPoolResult{FoundSize: int(found), PoolSize: int(found)}
	if result.FoundSize == input.PoolSize {
		pipe := a.RedisClient.Pipeline()
		pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
		pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			return result, fmt.Errorf("refresh yahoo id pool ttl: %w", err)
		}
		return result, nil
	}

	activity.GetLogger(ctx).Warn("Yahoo ID pool lost, rebuilding", "expected", input.PoolSize, "found", found)
	return a.rebuildYahooIDPool(ctx, result)
}

// rebuildYahooIDPool replaces the pool with one built from the stored Yahoo
// player files, minus the Yahoo IDs already matched in the database.
func (a *Activities) rebuildYahooIDPool(ctx context.Context, result EnsureYahooIDPoolResult) (EnsureYahooIDPoolResult, error) {
	players, err := a.parseAllYahooPlayers(ctx)
	if err != nil {
		return result, err
	}
	if err := CleanupYahooIDPool(ctx, a.RedisClient); err != nil {
		return result, err
	}
	if _, err := saveYahooPlayersToRedisImpl(ctx, a.RedisClient, players); err != nil {
		return result, err
	}
	matched, err := a.Manifests.MatchedYahooIDs(ctx)
	if err != nil {
		return result, err
	}
	if err := RemoveFromYahooIDPool(ctx, a.RedisClient, matched); err != nil {
		return result, err
	}

	size, err := a.RedisClient.HLen(ctx, YahooIDPoolKey).Result()
	if err != nil {
		return result, fmt.Errorf("measure rebuilt yahoo id pool: %w", err)
	}
	available, err := a.RedisClient.SCard(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return result, fmt.Errorf("measure rebuilt yahoo id available set: %w", err)
	}
	result.Rebuilt = true
	result.PoolSize = int(size)
	result.Available = int(available)
	return result, nil
}

// parseAllYahooPlayers lists and parses every stored Yahoo player file,
// heartbeating between chunks.
func (a *Activities) parseAllYahooPlayers(ctx context.Context) ([]store.YahooPlayer, error) {
	ids, err := listYahooPlayers(ctx, a.Storage)
	if err != nil {
		return nil, fmt.Errorf("list yahoo player files: %w", err)
	}
	players := make([]store.YahooPlayer, 0, len(ids))
	for start := 0; start < len(ids); start += yahooPoolRebuildChunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		activity.RecordHeartbeat(ctx, start)
		end := min(start+yahooPoolRebuildChunk, len(ids))
		players = append(players, parseYahooPlayerBatchImpl(ctx, a.Storage, a.GobCache, ids[start:end]).Players...)
	}
	return players, nil
}

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
// measured after saving it (SaveYahooIDPoolResult.PoolSize), and how many of
// its entries no page backs (SaveYahooIDPoolResult.NonPageEntries), both as
// the last rebuild updated them.
type EnsureYahooIDPoolInput struct {
	PoolSize       int
	NonPageEntries int
}

// EnsureYahooIDPoolResult reports what EnsureYahooIDPool found and did.
type EnsureYahooIDPoolResult struct {
	FoundSize         int  // pool hash size before any rebuild
	Rebuilt           bool // the pool was missing or incomplete and was rebuilt
	AvailableRestored bool // the hash was complete but the available set was lost, and was restored (not set when it was only empty)
	PoolSize          int  // pool hash size now; the next check compares against it
	NonPageEntries    int  // entries of a rebuilt pool that no page backs (the input's count otherwise)
	Available         int  // available set size after a rebuild or restore (0 otherwise)
	ReadErrors        int  // Yahoo player files a rebuild could not read
	ParseErrors       int  // Yahoo player files a rebuild could not parse
}

// EnsureYahooIDPool keeps the Yahoo ID pool of a ProcessPlayers chain alive
// across executions. The pool keys expire after ImportPlayersTTL and a Redis
// flush drops them; a missing pool makes LoadYahooIDPool return an empty map
// without an error, so phase 2 would stop matching silently. When the pool
// hash still has the size phase 1 measured, both keys' TTL is refreshed.
// Otherwise the pool is rebuilt from the stored Yahoo player files and the
// yahoo_league_players rows, and the Yahoo IDs already matched in the
// database are removed from the available set. During a run entries are
// only removed from the available set, never from the hash, so the hash size
// is a complete check of the hash. The available set is only checked for
// existence (see restoreYahooIDAvailable).
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
	result := EnsureYahooIDPoolResult{
		FoundSize: int(found.Val()), PoolSize: int(found.Val()), NonPageEntries: input.NonPageEntries,
	}
	if result.FoundSize != input.PoolSize {
		activity.GetLogger(ctx).Warn("Yahoo ID pool lost, rebuilding", "expected", input.PoolSize, "found", result.FoundSize)
		return a.rebuildYahooIDPool(ctx, result, input)
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
// files and the current yahoo_league_players rows, then removes the Yahoo
// IDs already matched in the database from the available set. It never
// deletes the keys first: several batches may rebuild at once, and a batch
// reading between a delete and the next write would match against an empty
// pool. Writing the same entries over each other converges. A rebuild that
// parses fewer pages than phase 1 saved (expected.PoolSize minus
// expected.NonPageEntries: files phase 1 read could not be read or parsed
// this time) fails, so it is retried instead of matching against a partial
// pool. The other entries are left out of that check: PostgreSQL, not
// Redis, holds the league players, and a newer pool import may have deleted
// some since phase 1; an entry an earlier save left in the hash is not
// written again. Without a non-page count (a chain started before league
// players joined the pool, whose input decodes it as zero, or a pool no
// league player or stale entry reached) the check stays the whole hash
// after the rebuild against expected.PoolSize, as before: such a chain's
// count holds its stale entries, which the page count cannot cover.
func (a *Activities) rebuildYahooIDPool(ctx context.Context, result EnsureYahooIDPoolResult, expected EnsureYahooIDPoolInput) (EnsureYahooIDPoolResult, error) {
	parsed, err := a.parseAllYahooPlayers(ctx)
	if err != nil {
		return result, err
	}
	result.ReadErrors, result.ParseErrors = parsed.ReadErrors, parsed.ParseErrors
	players, pageEntries, err := a.withLeaguePlayers(ctx, parsed.Players)
	if err != nil {
		return result, err
	}
	saved, err := saveYahooPlayersToRedisImpl(ctx, a.RedisClient, players)
	if err != nil {
		return result, err
	}
	if err := checkRebuiltYahooIDPool(expected, saved.PoolSize, pageEntries, parsed); err != nil {
		return result, err
	}
	if parsed.ReadErrors > 0 || parsed.ParseErrors > 0 {
		activity.GetLogger(ctx).Warn("Some Yahoo player files failed during the pool rebuild",
			"read_errors", parsed.ReadErrors, "parse_errors", parsed.ParseErrors)
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
	result.NonPageEntries = saved.PoolSize - pageEntries
	result.Available = int(available)
	return result, nil
}

// checkRebuiltYahooIDPool fails a rebuild that holds fewer entries than
// phase 1 saved: fewer page entries than its pages, or, without a non-page
// count, a smaller hash than its pool (see rebuildYahooIDPool).
func checkRebuiltYahooIDPool(expected EnsureYahooIDPoolInput, poolSize, pageEntries int, parsed ParseYahooPlayerBatchResult) error {
	if expected.NonPageEntries == 0 {
		if poolSize < expected.PoolSize {
			return fmt.Errorf("rebuilt yahoo id pool holds %d of the %d entries phase 1 saved (%d files unreadable, %d unparsable)",
				poolSize, expected.PoolSize, parsed.ReadErrors, parsed.ParseErrors)
		}
		return nil
	}
	expectedPageEntries := expected.PoolSize - expected.NonPageEntries
	if pageEntries < expectedPageEntries {
		return fmt.Errorf("rebuilt yahoo id pool holds %d of the %d page entries phase 1 saved (%d files unreadable, %d unparsable)",
			pageEntries, expectedPageEntries, parsed.ReadErrors, parsed.ParseErrors)
	}
	return nil
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

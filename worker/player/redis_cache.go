package player

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

const (
	// ImportPlayersTTL is the expiration time for import players data in Redis.
	ImportPlayersTTL = 1 * time.Hour

	// VerifiedNonNHLTTL is the expiration time for verified non-NHL player IDs.
	// These are Yahoo players confirmed to have 0 NHL games - we don't need to
	// re-verify them frequently. 30 days is reasonable since prospects rarely
	// make NHL debuts.
	VerifiedNonNHLTTL = 30 * 24 * time.Hour
)

// Redis key constants for YahooID pool
const (
	importPlayersPrefix = "puckdb:import:"
	YahooIDPoolKey      = importPlayersPrefix + "yahoo-id-pool"      // Hash: yahooID -> gob(YahooPlayer)
	YahooIDAvailableKey = importPlayersPrefix + "yahoo-id-available" // Set: available YahooIDs
	VerifiedNonNHLKey   = "puckdb:verified-non-nhl"                  // Set: Yahoo IDs verified to have 0 NHL games
)

// SaveYahooIDPoolResult contains the result of saving Yahoo players to Redis.
type SaveYahooIDPoolResult struct {
	TotalPlayers     int // Total Yahoo players provided
	AvailablePlayers int // Players added to the available pool
	SkippedNonNHL    int // Players skipped because they're verified non-NHL
}

// LoadYahooIDPool loads all Yahoo players from Redis.
func LoadYahooIDPool(ctx context.Context, client cache.Client) (map[store.YahooPlayerID]*store.YahooPlayer, error) {
	data, err := client.HGetAll(ctx, YahooIDPoolKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load yahoo id pool from redis: %w", err)
	}

	pool := make(map[store.YahooPlayerID]*store.YahooPlayer, len(data))
	for idStr, encoded := range data {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}

		var player store.YahooPlayer
		if err := gob.NewDecoder(bytes.NewReader([]byte(encoded))).Decode(&player); err != nil {
			log.Warn().Err(err).Int("yahooID", id).Msg("Failed to decode Yahoo player from Redis")
			continue
		}
		pool[store.YahooPlayerID(id)] = &player
	}

	log.Debug().
		Int("players", len(pool)).
		Msg("Loaded YahooID pool from Redis")

	return pool, nil
}

// LoadAvailableYahooIDs loads only the available (unmatched) Yahoo IDs from Redis.
func LoadAvailableYahooIDs(ctx context.Context, client cache.Client) (map[store.YahooPlayerID]struct{}, error) {
	ids, err := client.SMembers(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load available yahoo ids from redis: %w", err)
	}

	available := make(map[store.YahooPlayerID]struct{}, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		available[store.YahooPlayerID(id)] = struct{}{}
	}

	log.Debug().
		Int("available", len(available)).
		Msg("Loaded available YahooIDs from Redis")

	return available, nil
}

// RemoveFromYahooIDPool removes matched Yahoo IDs from the available set.
func RemoveFromYahooIDPool(ctx context.Context, client cache.Client, ids []store.YahooPlayerID) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = int(id)
	}

	if err := client.SRem(ctx, YahooIDAvailableKey, args...).Err(); err != nil {
		return fmt.Errorf("remove from yahoo id pool: %w", err)
	}

	log.Debug().
		Int("removed", len(ids)).
		Msg("Removed matched YahooIDs from available set")

	return nil
}

// GetUnmatchedYahooIDs returns all YahooIDs that were never matched.
func GetUnmatchedYahooIDs(ctx context.Context, client cache.Client) ([]store.YahooPlayerID, error) {
	ids, err := client.SMembers(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return nil, fmt.Errorf("get unmatched yahoo ids: %w", err)
	}

	result := make([]store.YahooPlayerID, 0, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		result = append(result, store.YahooPlayerID(id))
	}

	return result, nil
}

// GetYahooPlayerByID fetches a single Yahoo player from the pool by ID.
func GetYahooPlayerByID(ctx context.Context, client cache.Client, yahooID store.YahooPlayerID) (*store.YahooPlayer, error) {
	encoded, err := client.HGet(ctx, YahooIDPoolKey, yahooID.String()).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get yahoo player %d: %w", yahooID, err)
	}

	var player store.YahooPlayer
	if err := gob.NewDecoder(bytes.NewReader(encoded)).Decode(&player); err != nil {
		return nil, fmt.Errorf("decode yahoo player %d: %w", yahooID, err)
	}

	return &player, nil
}

// CleanupYahooIDPool deletes the YahooID pool keys from Redis.
func CleanupYahooIDPool(ctx context.Context, client cache.Client) error {
	if err := client.Del(ctx, YahooIDPoolKey, YahooIDAvailableKey).Err(); err != nil {
		return fmt.Errorf("cleanup yahoo id pool: %w", err)
	}

	log.Debug().Msg("Cleaned up YahooID pool from Redis")

	return nil
}

// SaveVerifiedNonNHLIDs stores Yahoo IDs that have been verified to have 0 NHL games.
// These IDs will be excluded from future unmatched reports.
func SaveVerifiedNonNHLIDs(ctx context.Context, client cache.Client, ids []store.YahooPlayerID) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = int(id)
	}

	pipe := client.Pipeline()
	pipe.SAdd(ctx, VerifiedNonNHLKey, args...)
	pipe.Expire(ctx, VerifiedNonNHLKey, VerifiedNonNHLTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save verified non-nhl ids: %w", err)
	}

	log.Info().
		Int("count", len(ids)).
		Msg("Saved verified non-NHL Yahoo IDs to Redis")

	return nil
}

// LoadVerifiedNonNHLIDs loads the set of Yahoo IDs verified to have 0 NHL games.
func LoadVerifiedNonNHLIDs(ctx context.Context, client cache.Client) (map[store.YahooPlayerID]struct{}, error) {
	ids, err := client.SMembers(ctx, VerifiedNonNHLKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load verified non-nhl ids: %w", err)
	}

	verified := make(map[store.YahooPlayerID]struct{}, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		verified[store.YahooPlayerID(id)] = struct{}{}
	}

	log.Debug().
		Int("count", len(verified)).
		Msg("Loaded verified non-NHL Yahoo IDs from Redis")

	return verified, nil
}

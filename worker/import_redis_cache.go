package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
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
	importPlayersPrefix   = "puckdb:import:"
	YahooIDPoolKey        = importPlayersPrefix + "yahoo-id-pool"      // Hash: yahooID -> gob(YahooPlayer)
	YahooIDAvailableKey   = importPlayersPrefix + "yahoo-id-available" // Set: available YahooIDs
	VerifiedNonNHLKey     = "puckdb:verified-non-nhl"                  // Set: Yahoo IDs verified to have 0 NHL games
)

// SaveYahooIDPoolResult contains the result of saving Yahoo players to Redis.
type SaveYahooIDPoolResult struct {
	TotalPlayers   int // Total Yahoo players provided
	AvailablePlayers int // Players added to the available pool
	SkippedNonNHL  int // Players skipped because they're verified non-NHL
}

// SaveYahooIDPool stores all Yahoo players in Redis as a hash and populates the available set.
// Players whose IDs are in the verified non-NHL set are excluded from the available set
// (they have been confirmed to have 0 NHL games and don't need to be matched).
// Returns the count of skipped players for reporting.
func SaveYahooIDPool(ctx context.Context, client redis.Client, players []cache.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	if len(players) == 0 {
		return &SaveYahooIDPoolResult{}, nil
	}

	// Load verified non-NHL IDs to exclude from available set
	verifiedNonNHL, err := LoadVerifiedNonNHLIDs(ctx, client)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load verified non-NHL IDs, including all players")
		verifiedNonNHL = make(map[int]struct{})
	}

	pipe := client.Pipeline()

	// Store each player in the hash (we still store all for reference)
	for _, player := range players {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(player); err != nil {
			return nil, fmt.Errorf("encode yahoo player %d: %w", player.YahooID, err)
		}
		pipe.HSet(ctx, YahooIDPoolKey, strconv.Itoa(player.YahooID), buf.Bytes())
	}

	// Add only non-verified IDs to the available set
	var availableIDs []interface{}
	excludedCount := 0
	for _, player := range players {
		if _, isVerifiedNonNHL := verifiedNonNHL[player.YahooID]; !isVerifiedNonNHL {
			availableIDs = append(availableIDs, player.YahooID)
		} else {
			excludedCount++
		}
	}

	if len(availableIDs) > 0 {
		pipe.SAdd(ctx, YahooIDAvailableKey, availableIDs...)
	}

	// Set TTL on both keys
	pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
	pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("save yahoo id pool to redis: %w", err)
	}

	log.Info().
		Int("total_players", len(players)).
		Int("available", len(availableIDs)).
		Int("excluded_verified_non_nhl", excludedCount).
		Msg("Saved YahooID pool to Redis")

	return &SaveYahooIDPoolResult{
		TotalPlayers:     len(players),
		AvailablePlayers: len(availableIDs),
		SkippedNonNHL:    excludedCount,
	}, nil
}

// LoadYahooIDPool loads all Yahoo players from Redis.
func LoadYahooIDPool(ctx context.Context, client redis.Client) (map[int]*cache.YahooPlayer, error) {
	data, err := client.HGetAll(ctx, YahooIDPoolKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load yahoo id pool from redis: %w", err)
	}

	pool := make(map[int]*cache.YahooPlayer, len(data))
	for idStr, encoded := range data {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}

		var player cache.YahooPlayer
		if err := gob.NewDecoder(bytes.NewReader([]byte(encoded))).Decode(&player); err != nil {
			log.Warn().Err(err).Int("yahooID", id).Msg("Failed to decode Yahoo player from Redis")
			continue
		}
		pool[id] = &player
	}

	log.Debug().
		Int("players", len(pool)).
		Msg("Loaded YahooID pool from Redis")

	return pool, nil
}

// LoadAvailableYahooIDs loads only the available (unmatched) Yahoo IDs from Redis.
func LoadAvailableYahooIDs(ctx context.Context, client redis.Client) (map[int]struct{}, error) {
	ids, err := client.SMembers(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load available yahoo ids from redis: %w", err)
	}

	available := make(map[int]struct{}, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		available[id] = struct{}{}
	}

	log.Debug().
		Int("available", len(available)).
		Msg("Loaded available YahooIDs from Redis")

	return available, nil
}

// RemoveFromYahooIDPool removes matched Yahoo IDs from the available set.
func RemoveFromYahooIDPool(ctx context.Context, client redis.Client, ids []int) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
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
func GetUnmatchedYahooIDs(ctx context.Context, client redis.Client) ([]int, error) {
	ids, err := client.SMembers(ctx, YahooIDAvailableKey).Result()
	if err != nil {
		return nil, fmt.Errorf("get unmatched yahoo ids: %w", err)
	}

	result := make([]int, 0, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		result = append(result, id)
	}

	return result, nil
}

// GetYahooPlayerByID fetches a single Yahoo player from the pool by ID.
func GetYahooPlayerByID(ctx context.Context, client redis.Client, yahooID int) (*cache.YahooPlayer, error) {
	encoded, err := client.HGet(ctx, YahooIDPoolKey, strconv.Itoa(yahooID)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get yahoo player %d: %w", yahooID, err)
	}

	var player cache.YahooPlayer
	if err := gob.NewDecoder(bytes.NewReader(encoded)).Decode(&player); err != nil {
		return nil, fmt.Errorf("decode yahoo player %d: %w", yahooID, err)
	}

	return &player, nil
}

// CleanupYahooIDPool deletes the YahooID pool keys from Redis.
func CleanupYahooIDPool(ctx context.Context, client redis.Client) error {
	if err := client.Del(ctx, YahooIDPoolKey, YahooIDAvailableKey).Err(); err != nil {
		return fmt.Errorf("cleanup yahoo id pool: %w", err)
	}

	log.Debug().Msg("Cleaned up YahooID pool from Redis")

	return nil
}

// SaveVerifiedNonNHLIDs stores Yahoo IDs that have been verified to have 0 NHL games.
// These IDs will be excluded from future unmatched reports.
func SaveVerifiedNonNHLIDs(ctx context.Context, client redis.Client, ids []int) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
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
func LoadVerifiedNonNHLIDs(ctx context.Context, client redis.Client) (map[int]struct{}, error) {
	ids, err := client.SMembers(ctx, VerifiedNonNHLKey).Result()
	if err != nil {
		return nil, fmt.Errorf("load verified non-nhl ids: %w", err)
	}

	verified := make(map[int]struct{}, len(ids))
	for _, idStr := range ids {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		verified[id] = struct{}{}
	}

	log.Debug().
		Int("count", len(verified)).
		Msg("Loaded verified non-NHL Yahoo IDs from Redis")

	return verified, nil
}


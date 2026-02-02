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
)

// Redis key constants for YahooID pool
const (
	importPlayersPrefix   = "puckdb:import:"
	YahooIDPoolKey        = importPlayersPrefix + "yahoo-id-pool"      // Hash: yahooID -> gob(YahooPlayer)
	YahooIDAvailableKey   = importPlayersPrefix + "yahoo-id-available" // Set: available YahooIDs
)

// SaveYahooIDPool stores all Yahoo players in Redis as a hash and populates the available set.
func SaveYahooIDPool(ctx context.Context, client redis.Client, players []cache.YahooPlayer) error {
	if len(players) == 0 {
		return nil
	}

	pipe := client.Pipeline()

	// Store each player in the hash
	for _, player := range players {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(player); err != nil {
			return fmt.Errorf("encode yahoo player %d: %w", player.YahooID, err)
		}
		pipe.HSet(ctx, YahooIDPoolKey, strconv.Itoa(player.YahooID), buf.Bytes())
	}

	// Add all IDs to the available set
	ids := make([]interface{}, len(players))
	for i, player := range players {
		ids[i] = player.YahooID
	}
	pipe.SAdd(ctx, YahooIDAvailableKey, ids...)

	// Set TTL on both keys
	pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
	pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save yahoo id pool to redis: %w", err)
	}

	log.Debug().
		Int("players", len(players)).
		Msg("Saved YahooID pool to Redis")

	return nil
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

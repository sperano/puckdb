package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// PlayerBatchTTL is the expiration time for player batch data in Redis.
	// Set to 1 hour to allow workflows to complete while ensuring cleanup on failure.
	PlayerBatchTTL = 1 * time.Hour
)

// PlayerBatchKeyPrefix is the prefix for all player batch keys
const PlayerBatchKeyPrefix = "yfh:players:"

// PlayerBatchKey generates a Redis key for a player batch.
// Format: yfh:players:{source}:{season}:{batchIndex}
func PlayerBatchKey(source string, season int, batchIndex int) string {
	return fmt.Sprintf("%s%s:%d:%d", PlayerBatchKeyPrefix, source, season, batchIndex)
}

// SeasonResultKey generates a Redis key for a merged season result.
// Format: yfh:players:season:{season}
func SeasonResultKey(season int) string {
	return fmt.Sprintf("%sseason:%d", PlayerBatchKeyPrefix, season)
}

// EnrichmentPlayersKey generates a Redis key for storing players to be enriched.
// Format: yfh:players:enrichment
func EnrichmentPlayersKey() string {
	return fmt.Sprintf("%senrichment", PlayerBatchKeyPrefix)
}

// SavePlayerBatch saves gob-encoded data to Redis with TTL.
// The caller is responsible for encoding the data.
func SavePlayerBatch(ctx context.Context, client Client, key string, data []byte) error {
	if err := client.Set(ctx, key, data, PlayerBatchTTL).Err(); err != nil {
		return fmt.Errorf("save player batch to redis: %w", err)
	}

	log.Debug().
		Str("key", key).
		Int("bytes", len(data)).
		Msg("Saved player batch to Redis")

	return nil
}

// LoadPlayerBatch loads gob-encoded data from Redis.
// The caller is responsible for decoding the data.
func LoadPlayerBatch(ctx context.Context, client Client, key string) ([]byte, error) {
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("load player batch from redis: %w", err)
	}

	log.Debug().
		Str("key", key).
		Int("bytes", len(data)).
		Msg("Loaded player batch from Redis")

	return data, nil
}

// DeletePlayerBatches deletes multiple player batch keys from Redis.
func DeletePlayerBatches(ctx context.Context, client Client, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	if err := client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("delete player batches from redis: %w", err)
	}

	log.Debug().
		Int("keys", len(keys)).
		Msg("Deleted player batches from Redis")

	return nil
}

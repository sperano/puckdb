package worker

import (
	"bytes"
	"context"
	"encoding/gob"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/redis"
)

// MergePlayerBatchesFromRedisActivity loads player batches from Redis and merges them.
// It deletes the Redis keys after successful merge.
// source is either "yahoo" or "boxscore" to determine merge strategy.
func MergePlayerBatchesFromRedisActivity(
	ctx context.Context,
	keys []string,
	source string,
) (map[int64]PartialPlayer, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return mergePlayerBatchesFromRedisImpl(ctx, redisClient, keys, source)
}

// mergePlayerBatchesFromRedisImpl is the testable implementation.
func mergePlayerBatchesFromRedisImpl(
	ctx context.Context,
	redisClient redis.Client,
	keys []string,
	source string,
) (map[int64]PartialPlayer, error) {
	if len(keys) == 0 {
		return make(map[int64]PartialPlayer), nil
	}

	log.Info().
		Int("batches", len(keys)).
		Str("source", source).
		Msg("Merge from Redis: starting")

	merged := make(map[int64]PartialPlayer)

	for i, key := range keys {
		data, err := redis.LoadPlayerBatch(ctx, redisClient, key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load batch from Redis, skipping")
			continue
		}

		var players map[int64]PartialPlayer
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to decode batch, skipping")
			continue
		}

		// Merge based on source type
		for id, p := range players {
			if source == "yahoo" {
				if existing, ok := merged[id]; !ok || !existing.HasYahooData {
					merged[id] = p
				}
			} else {
				if existing, ok := merged[id]; !ok || !existing.HasBoxscoreData {
					merged[id] = p
				}
			}
		}

		log.Debug().
			Int("batch", i+1).
			Int("total_batches", len(keys)).
			Int("batch_players", len(players)).
			Int("merged_players", len(merged)).
			Msg("Merge from Redis: batch merged")
	}

	// Clean up Redis keys
	if err := redis.DeletePlayerBatches(ctx, redisClient, keys); err != nil {
		log.Warn().Err(err).Msg("Failed to delete Redis keys, they will expire via TTL")
	}

	log.Info().
		Int("batches", len(keys)).
		Int("total_players", len(merged)).
		Str("source", source).
		Msg("Merge from Redis: complete")

	return merged, nil
}

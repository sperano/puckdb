package player

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/store"
)

// saveYahooPlayersToRedisImpl writes players over the pool keys and measures
// the pool hash size (PoolSize) in the same round trip as the write.
func saveYahooPlayersToRedisImpl(ctx context.Context, client *redis.Client, players []store.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	if len(players) == 0 {
		size, err := client.HLen(ctx, YahooIDPoolKey).Result()
		if err != nil {
			return nil, fmt.Errorf("measure yahoo id pool: %w", err)
		}
		return &SaveYahooIDPoolResult{PoolSize: int(size)}, nil
	}

	verifiedNonNHL, err := LoadVerifiedNonNHLIDs(ctx, client)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load verified non-NHL IDs, including all players")
		verifiedNonNHL = make(map[store.YahooPlayerID]struct{})
	}

	pipe := client.Pipeline()

	for _, player := range players {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(player); err != nil {
			return nil, fmt.Errorf("encode yahoo player %d: %w", player.YahooID, err)
		}
		pipe.HSet(ctx, YahooIDPoolKey, player.YahooID.String(), buf.Bytes())
	}

	var availableIDs []any
	excludedCount := 0
	for _, player := range players {
		if _, isVerifiedNonNHL := verifiedNonNHL[player.YahooID]; !isVerifiedNonNHL {
			availableIDs = append(availableIDs, player.YahooID.String())
		} else {
			excludedCount++
		}
	}

	if len(availableIDs) > 0 {
		pipe.SAdd(ctx, YahooIDAvailableKey, availableIDs...)
	}

	pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
	pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)
	poolSize := pipe.HLen(ctx, YahooIDPoolKey)

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
		PoolSize:         int(poolSize.Val()),
	}, nil
}

package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
)

const (
	// BoxscorePlayersKeyPrefix is the prefix for all boxscore players keys.
	BoxscorePlayersKeyPrefix = "puckdb:boxscore-players:"
	// AllBoxscorePlayersKey is the Redis key for the consolidated set of all players.
	AllBoxscorePlayersKey = "puckdb:boxscore-players:all"
)

// boxscoreClient is the minimal Redis surface used by boxscore players functions.
// Declared on the consumer side ("accept interfaces") so callers can pass any
// implementation — production uses *redis.Client; tests can pass narrower fakes.
type boxscoreClient interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

// BoxscorePlayersKey generates a Redis key for boxscore players for a season.
// Format: puckdb:boxscore-players:{season}
func BoxscorePlayersKey(season nhl.Season) string {
	return fmt.Sprintf("%s%d", BoxscorePlayersKeyPrefix, season.ID())
}

// SaveBoxscorePlayers saves boxscore players to Redis with the specified TTL.
// Players are gob-encoded for efficient binary storage.
func SaveBoxscorePlayers(ctx context.Context, client boxscoreClient, season nhl.Season, players []store.BoxscorePlayer, ttl time.Duration) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(players); err != nil {
		return fmt.Errorf("encode boxscore players: %w", err)
	}

	key := BoxscorePlayersKey(season)
	if err := client.Set(ctx, key, buf.Bytes(), ttl).Err(); err != nil {
		return fmt.Errorf("save boxscore players to redis: %w", err)
	}

	log.Debug().
		Str("key", key).
		Int("players", len(players)).
		Int("bytes", buf.Len()).
		Dur("ttl", ttl).
		Msg("Saved boxscore players to Redis")

	return nil
}

// LoadBoxscorePlayers loads boxscore players from Redis for a season.
// Returns nil slice if the key doesn't exist.
func LoadBoxscorePlayers(ctx context.Context, client boxscoreClient, season nhl.Season) ([]store.BoxscorePlayer, error) {
	key := BoxscorePlayersKey(season)
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("load boxscore players from redis: %w", err)
	}

	var players []store.BoxscorePlayer
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
		return nil, fmt.Errorf("decode boxscore players: %w", err)
	}

	log.Debug().
		Str("key", key).
		Int("players", len(players)).
		Int("bytes", len(data)).
		Msg("Loaded boxscore players from Redis")

	return players, nil
}

// SaveAllBoxscorePlayers saves the consolidated unique player set to Redis.
func SaveAllBoxscorePlayers(ctx context.Context, client boxscoreClient, players []store.BoxscorePlayer, ttl time.Duration) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(players); err != nil {
		return fmt.Errorf("encode all boxscore players: %w", err)
	}

	if err := client.Set(ctx, AllBoxscorePlayersKey, buf.Bytes(), ttl).Err(); err != nil {
		return fmt.Errorf("save all boxscore players to redis: %w", err)
	}

	log.Info().
		Int("players", len(players)).
		Int("bytes", buf.Len()).
		Dur("ttl", ttl).
		Msg("Saved consolidated boxscore players to Redis")

	return nil
}

// LoadAllBoxscorePlayers loads the consolidated player set from Redis.
func LoadAllBoxscorePlayers(ctx context.Context, client boxscoreClient) ([]store.BoxscorePlayer, error) {
	data, err := client.Get(ctx, AllBoxscorePlayersKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("load all boxscore players from redis: %w", err)
	}

	var players []store.BoxscorePlayer
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
		return nil, fmt.Errorf("decode all boxscore players: %w", err)
	}

	return players, nil
}

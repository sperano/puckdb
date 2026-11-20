package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"strings"
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
	// AllBoxscorePlayerSeasonsKey is the Redis key for the start years of the
	// seasons the consolidated set was built from.
	AllBoxscorePlayerSeasonsKey = "puckdb:boxscore-players:all-seasons"
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

// consolidatedClient is the Redis surface of the consolidated set's
// atomic save and load.
type consolidatedClient interface {
	TxPipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
}

// SaveConsolidatedBoxscorePlayers saves the consolidated unique player set
// and the start years of the seasons it was built from in one MULTI/EXEC
// transaction, both with ttl, so no reader sees one without the other.
func SaveConsolidatedBoxscorePlayers(ctx context.Context, client consolidatedClient, players []store.BoxscorePlayer, seasons []int, ttl time.Duration) error {
	var playersBuf, seasonsBuf bytes.Buffer
	if err := gob.NewEncoder(&playersBuf).Encode(players); err != nil {
		return fmt.Errorf("encode all boxscore players: %w", err)
	}
	if err := gob.NewEncoder(&seasonsBuf).Encode(seasons); err != nil {
		return fmt.Errorf("encode boxscore player seasons: %w", err)
	}
	_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, AllBoxscorePlayersKey, playersBuf.Bytes(), ttl)
		pipe.Set(ctx, AllBoxscorePlayerSeasonsKey, seasonsBuf.Bytes(), ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("save consolidated boxscore players to redis: %w", err)
	}
	log.Info().
		Int("players", len(players)).
		Ints("seasons", seasons).
		Int("bytes", playersBuf.Len()).
		Dur("ttl", ttl).
		Msg("Saved consolidated boxscore players to Redis")
	return nil
}

// LoadConsolidatedBoxscorePlayers reads the consolidated player set and its
// seasons in one MGET, so both come from the same save. A missing set is nil
// players; missing seasons (a set saved before the seasons were, or expired
// first) are nil seasons, unknown, while an empty saved list is known.
func LoadConsolidatedBoxscorePlayers(ctx context.Context, client consolidatedClient) ([]store.BoxscorePlayer, []int, error) {
	values, err := client.MGet(ctx, AllBoxscorePlayersKey, AllBoxscorePlayerSeasonsKey).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("load consolidated boxscore players from redis: %w", err)
	}
	var players []store.BoxscorePlayer
	if data, ok := values[0].(string); ok {
		if err := gob.NewDecoder(strings.NewReader(data)).Decode(&players); err != nil {
			return nil, nil, fmt.Errorf("decode all boxscore players: %w", err)
		}
	}
	data, ok := values[1].(string)
	if !ok {
		return players, nil, nil
	}
	seasons := []int{}
	if err := gob.NewDecoder(strings.NewReader(data)).Decode(&seasons); err != nil {
		return nil, nil, fmt.Errorf("decode boxscore player seasons: %w", err)
	}
	return players, seasons, nil
}

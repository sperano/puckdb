package cache

import (
	"context"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
)

// NewClient creates a new Redis client using the configured settings.
// Returns the concrete go-redis client. Consumers that need a narrower
// surface should declare their own interface (the "accept interfaces,
// return concretes" idiom) — see worker/shared/progress.go for an example.
func NewClient() *redis.Client {
	url := viper.GetString(config.FlagRedisURL)
	db := viper.GetInt(config.FlagRedisDB)
	log.Debug().Str("url", url).Int("db", db).Msg("Initializing Redis")
	return redis.NewClient(&redis.Options{
		Addr:     url,
		Password: viper.GetString(config.FlagRedisPassword),
		DB:       db,
	})
}

// FlushDB clears all keys from the current Redis database.
func FlushDB(ctx context.Context, client *redis.Client) error {
	return client.FlushDB(ctx).Err()
}

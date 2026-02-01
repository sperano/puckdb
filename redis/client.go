package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
)

type Client interface {
	redislock.RedisClient
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	FlushDB(ctx context.Context) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Keys(ctx context.Context, pattern string) *redis.StringSliceCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	TTL(ctx context.Context, key string) *redis.DurationCmd
	Close() error
}

func NewClient() Client {
	url := viper.GetString(config.FlagRedisURL)
	db := viper.GetInt(config.FlagRedisDB)
	log.Debug().Str("url", url).Int("db", db).Msg("Initializing Redis")
	return redis.NewClient(&redis.Options{
		Addr:     url,
		Password: viper.GetString(config.FlagRedisPassword),
		DB:       db,
	})
}

func FlushDB(ctx context.Context, client Client) error {
	return client.FlushDB(ctx).Err()
}

func clearCacheWithFilter(ctx context.Context, client Client, filter string) error {
	keys := client.Keys(ctx, filter)
	result, err := keys.Result()
	if err != nil {
		return fmt.Errorf("error clearing cache: %w", err)
	}
	log.Debug().Strs("keys", result).Msg("Clearing")
	return client.Del(ctx, result...).Err()
}

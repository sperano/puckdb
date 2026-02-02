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
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
	FlushDB(ctx context.Context) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	HGet(ctx context.Context, key, field string) *redis.StringCmd
	HGetAll(ctx context.Context, key string) *redis.StringStringMapCmd
	HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Keys(ctx context.Context, pattern string) *redis.StringSliceCmd
	Pipeline() redis.Pipeliner
	SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	SMembers(ctx context.Context, key string) *redis.StringSliceCmd
	SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
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

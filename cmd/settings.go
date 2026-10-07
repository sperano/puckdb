package cmd

import (
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
)

// The readers below are the only place where library client settings come
// out of viper: commands pass viper.GetViper(), tests a viper.New() of
// their own. The library packages take the returned values and never read
// viper themselves.

// temporalOptionsFrom reads the --temporal-* connection flags.
func temporalOptionsFrom(v *viper.Viper) temporal.Options {
	return temporal.Options{
		HostPort:  v.GetString(config.FlagTemporalHostPort),
		Namespace: v.GetString(config.FlagTemporalNamespace),
	}
}

// redisOptionsFrom reads the --redis-* connection flags.
func redisOptionsFrom(v *viper.Viper) cache.Options {
	return cache.Options{
		Addr:     v.GetString(config.FlagRedisURL),
		Password: v.GetString(config.FlagRedisPassword),
		DB:       v.GetInt(config.FlagRedisDB),
	}
}

// dataPathFrom reads --data-path, the root of the file cache.
func dataPathFrom(v *viper.Viper) string {
	return v.GetString(config.FlagDataPath)
}

// newTemporalClient dials Temporal with the configured settings.
func newTemporalClient() (client.Client, error) {
	return temporal.NewClient(temporalOptionsFrom(viper.GetViper()))
}

// newRedisClient opens a Redis client with the configured settings.
func newRedisClient() *redis.Client {
	return cache.NewClient(redisOptionsFrom(viper.GetViper()))
}

// newDefaultStorage opens the file cache under the configured data path.
func newDefaultStorage() store.Storage {
	return store.NewDefaultStorage(dataPathFrom(viper.GetViper()))
}

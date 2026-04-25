package cmd

import (
	"time"

	"github.com/rs/zerolog/log"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
)

// newGobCache creates a GobCache from viper flags.
// If --gob-cache-config is set, loads per-type config from the YAML file.
// Otherwise falls back to the flat --gob-cache-ttl value.
func newGobCache(client *redis.Client) (*cache.GobCache, error) {
	configPath := viper.GetString(config.FlagGobCacheConfig)
	if configPath != "" {
		cfg, err := cache.LoadGobCacheConfig(configPath)
		if err != nil {
			return nil, err
		}
		log.Info().Str("path", configPath).Msg("loaded gob cache config")
		return cache.NewGobCacheWithConfig(client, cfg), nil
	}

	ttl := time.Duration(viper.GetInt(config.FlagGobCacheTTL)) * time.Minute
	return cache.NewGobCacheWithTTL(client, ttl), nil
}

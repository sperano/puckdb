package cmd

import (
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
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
		return cache.NewGobCache(client, cache.WithConfig(cfg)), nil
	}

	ttl := time.Duration(viper.GetInt(config.FlagGobCacheTTL)) * time.Minute
	return cache.NewGobCache(client, cache.WithTTL(ttl)), nil
}

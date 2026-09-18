package cmd

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
)

// runRedisCollector periodically checks Redis for OAuth token existence
func runRedisCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting Redis collector")

	redisClient := cache.NewClient()
	defer redisClient.Close()

	// Collect immediately on startup
	collectRedisMetrics(ctx, redisClient)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectRedisMetrics(ctx, redisClient)
		}
	}
}

func collectRedisMetrics(ctx context.Context, redisClient *redis.Client) {
	start := time.Now()
	hasToken, err := cache.HasUsableToken(ctx, redisClient, config.DefaultUser)
	if err != nil {
		log.Error().Err(err).Str("user", config.DefaultUser).Msg("Failed to check OAuth token")
		return
	}
	metrics.SetRedisOAuthTokenValid(config.DefaultUser, hasToken)
	metrics.SetRedisMetricsTimestamp()
	log.Info().
		Str("user", config.DefaultUser).
		Bool("has_token", hasToken).
		Dur("duration", time.Since(start)).
		Msg("Redis metrics updated")
}

package cmd

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
)

// runRedisCollector periodically reads the Redis server statistics and checks
// for OAuth token existence
func runRedisCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting Redis collector")

	redisClient := newRedisClient()
	defer closeOrWarn(redisClient, "redis client")

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
	collectRedisServerStats(ctx, redisClient)
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

// collectRedisServerStats publishes the server's INFO statistics (memory,
// clients, keyspace). A failure is logged and leaves the previous reading.
func collectRedisServerStats(ctx context.Context, redisClient *redis.Client) {
	info, err := redisClient.Info(ctx).Result()
	if err != nil {
		log.Error().Err(err).Msg("Failed to read Redis INFO")
		return
	}
	stats, err := parseRedisInfo(info)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse Redis INFO")
		return
	}
	metrics.SetRedisServerStats(stats)
}

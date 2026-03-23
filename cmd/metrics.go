package cmd

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Local flag name for port (aliased from config.FlagMetricsPort for the metrics command)
const FlagMetricsPortLocal = "port"

func cmdMetrics() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "metrics",
		Short: "Expose cache, Redis, and database metrics as Prometheus metrics",
		Long: `Run a metrics server that periodically computes various metrics
and exposes them via /metrics endpoint for Prometheus scraping.

Collectors:
  - Cache: File cache completeness statistics (requires --data-path)
  - Redis: OAuth token existence check (requires --redis-url)
  - Database: Table row counts (requires --postgres-* flags)

Each collector runs independently at its own interval.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			// FlagMetricsPortLocal is aliased to config.FlagMetricsPort
			if err := viper.BindPFlag(config.FlagMetricsPort, flags.Lookup(FlagMetricsPortLocal)); err != nil {
				return err
			}
			return config.BindFlags(flags,
				&config.DataPathFlags,
				&config.YahooSeasonsFlags,
				&config.MetricsIntervalFlags,
				&config.RedisFlags,
				&config.PostgresFlags,
				&config.GobCacheFlags,
				&config.SeasonRangeFlags,
			)
		},
		RunE: runMetrics,
	}
	flags := cmd.Flags()
	config.InitFlags(flags,
		&config.DataPathFlags,
		&config.YahooSeasonsFlags,
		&config.SeasonRangeFlags,
		&config.RedisFlags,
		&config.PostgresFlags,
		&config.GobCacheFlags,
		&config.MetricsIntervalFlags,
	)
	flags.Int(FlagMetricsPortLocal, config.DefaultMetricsPort, "Port for metrics endpoint")
	return cmd
}

func runMetrics(cmd *cobra.Command, _ []string) error {
	config.LogFlagValues()

	ctx := cmd.Context()
	port := viper.GetInt(config.FlagMetricsPort)
	cacheInterval := time.Duration(viper.GetInt(config.FlagCacheIntervalSeconds)) * time.Second
	redisInterval := time.Duration(viper.GetInt(config.FlagRedisIntervalSeconds)) * time.Second
	dbInterval := time.Duration(viper.GetInt(config.FlagDBIntervalSeconds)) * time.Second

	log.Info().
		Int("port", port).
		Dur("cache_interval_ms", cacheInterval).
		Dur("redis_interval_ms", redisInterval).
		Dur("db_interval_ms", dbInterval).
		Msg("Starting metrics server")

	metrics.SetBuildInfo(config.BuildNumber)

	// Start collectors with independent intervals
	go runCacheCollector(ctx, cacheInterval)
	go runRedisCollector(ctx, redisInterval)
	go runDatabaseCollector(ctx, dbInterval)

	// Start metrics server (blocks)
	addr := fmt.Sprintf(":%d", port)
	metrics.StartServer(addr)
	return nil
}

// runCacheCollector periodically collects cache file metrics.
// Uses an atomic flag to prevent overlapping runs when collection takes longer than the interval.
func runCacheCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting cache collector")

	var running atomic.Bool

	collect := func() {
		if !running.CompareAndSwap(false, true) {
			log.Warn().Msg("Cache collection still running, skipping this tick")
			return
		}
		defer running.Store(false)
		if err := computeAndUpdateCacheMetrics(ctx); err != nil {
			log.Error().Err(err).Msg("Cache metrics computation failed")
		}
	}

	// Collect immediately on startup
	collect()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}

func computeAndUpdateCacheMetrics(ctx context.Context) error {
	start := time.Now()

	redisClient := cache.NewClient()
	defer redisClient.Close()

	cacheData, err := getAllMetrics(ctx, redisClient)
	if err != nil {
		return err
	}

	// Update Prometheus gauges per season and file type
	for _, s := range cacheData {
		season := fmt.Sprintf("%d", s.seasonYear)
		metrics.SetCacheMetrics(season, s.fileType, s.expected, s.found)
	}

	// Compute and set totals
	var totalExpected, totalFound int
	for _, s := range cacheData {
		totalExpected += s.expected
		totalFound += s.found
	}
	metrics.SetCacheMetrics("total", "all", totalExpected, totalFound)

	// Calculate disk usage and file type stats in a single directory walk
	dataPath := viper.GetString(config.FlagDataPath)
	if dataPath != "" {
		pathStats := collectDataPathStats(dataPath)
		metrics.SetCacheDiskSizeBytes(pathStats.totalBytes)
		for fileType, stats := range pathStats.byType {
			metrics.SetDataPathFileStats(fileType, stats.count, stats.bytes)
		}
	}

	metrics.SetCacheMetricsTimestamp()
	metrics.SetCacheComputeDuration(time.Since(start))

	seasonCount := countUniqueSeasons(cacheData)
	log.Info().
		Int("seasons", seasonCount).
		Int("total_expected", totalExpected).
		Int("total_found", totalFound).
		Dur("duration", time.Since(start)).
		Msg("Cache metrics updated")

	return nil
}

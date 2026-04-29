package cmd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cacheSeasonLabelAll is the Prometheus season label for season-independent
// cache metrics (asset caches that don't belong to any single year).
const cacheSeasonLabelAll = "all"

// cacheSeasonLabelTotal is the Prometheus season label used by the rolled-up
// "everything across every season" metric pair emitted alongside the per-
// season gauges.
const cacheSeasonLabelTotal = "total"

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

	// Open the Postgres pool once and share it across the cache and database
	// collectors. The pool is expensive to construct (TLS handshakes,
	// connection warmup) so creating it per-tick would be wasteful.
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}

	// Wait for collectors before closing the pool. Without the join, ctx
	// cancellation would let the deferred pool.Close fire while a collector
	// was mid-tick using a connection.
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); runCacheCollector(ctx, cacheInterval, pool) }()
	go func() { defer wg.Done(); runRedisCollector(ctx, redisInterval) }()
	go func() { defer wg.Done(); runDatabaseCollector(ctx, dbInterval, pool) }()

	// Start metrics server (blocks until ctx canceled).
	addr := fmt.Sprintf(":%d", port)
	metrics.StartServer(ctx, addr)

	wg.Wait()
	pool.Close()
	return nil
}

// runCacheCollector periodically collects cache file metrics.
// Uses an atomic flag to prevent overlapping runs when collection takes longer than the interval.
func runCacheCollector(ctx context.Context, interval time.Duration, pool *pgxpool.Pool) {
	log.Info().Dur("interval", interval).Msg("Starting cache collector")

	var running atomic.Bool

	collect := func() {
		if !running.CompareAndSwap(false, true) {
			log.Warn().Msg("Cache collection still running, skipping this tick")
			return
		}
		defer running.Store(false)
		if err := computeAndUpdateCacheMetrics(ctx, pool); err != nil {
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

func computeAndUpdateCacheMetrics(ctx context.Context, pool *pgxpool.Pool) error {
	start := time.Now()

	redisClient := cache.NewClient()
	defer redisClient.Close()

	queries := sqlcdb.New(pool)

	cacheData, err := getAllMetrics(ctx, redisClient, queries)
	if err != nil {
		return err
	}

	// Update Prometheus gauges per season and file type
	for _, s := range cacheData {
		metrics.SetCacheMetrics(s.seasonLabel(), s.fileType.String(), s.expected, s.found)
	}

	// Compute and set totals
	var totalExpected, totalFound int
	for _, s := range cacheData {
		totalExpected += s.expected
		totalFound += s.found
	}
	metrics.SetCacheMetrics(cacheSeasonLabelTotal, cacheSeasonLabelAll, totalExpected, totalFound)

	// Calculate disk usage and file type stats in a single directory walk
	dataPath := viper.GetString(config.FlagDataPath)
	if dataPath != "" {
		pathStats := collectDataPathStats(dataPath)
		metrics.SetCacheDiskSizeBytes(pathStats.totalBytes)
		for _, ft := range core.AllFileTypes {
			stats := pathStats.byType[ft]
			var count, bytes int64
			if stats != nil {
				count, bytes = stats.count, stats.bytes
			}
			metrics.SetDataPathFileStats(ft.String(), count, bytes)
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

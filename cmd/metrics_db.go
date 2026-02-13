package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
)

// runDatabaseCollector periodically collects database table row counts
func runDatabaseCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting database collector")

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open database for metrics collector")
		return
	}
	defer pool.Close()

	// Collect immediately on startup
	collectDatabaseMetrics(ctx, pool)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectDatabaseMetrics(ctx, pool)
		}
	}
}

func collectDatabaseMetrics(ctx context.Context, pool *pgxpool.Pool) {
	start := time.Now()

	tables := []string{"players", "franchises", "seasons", "season_teams", "games", "game_skater_stats", "game_goalie_stats"}
	var totalRows int64
	for _, table := range tables {
		var count int64
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := pool.QueryRow(ctx, query).Scan(&count); err != nil {
			log.Warn().Err(err).Str("table", table).Msg("Failed to count rows")
			continue
		}
		metrics.SetDBTableRowCount(table, count)
		totalRows += count
	}

	// Query database size
	var dbSizeBytes int64
	if err := pool.QueryRow(ctx, "SELECT pg_database_size(current_database())").Scan(&dbSizeBytes); err != nil {
		log.Warn().Err(err).Msg("Failed to get database size")
	} else {
		metrics.SetDBSizeBytes(dbSizeBytes)
	}

	metrics.SetDBMetricsTimestamp()
	log.Info().
		Int("tables", len(tables)).
		Int64("total_rows", totalRows).
		Int64("db_size_bytes", dbSizeBytes).
		Dur("duration", time.Since(start)).
		Msg("Database metrics updated")
}

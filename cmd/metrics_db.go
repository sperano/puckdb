package cmd

import (
	"context"
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

// rowCountQuery uses pg_stat_user_tables estimates instead of COUNT(*) to avoid
// full sequential scans on large tables. The statistics are kept fresh by the
// hourly ANALYZE cron job.
const rowCountQuery = `SELECT relname::text, n_live_tup
	FROM pg_stat_user_tables
	WHERE schemaname = 'public'
	ORDER BY relname`

func collectDatabaseMetrics(ctx context.Context, pool *pgxpool.Pool) {
	start := time.Now()

	rows, err := pool.Query(ctx, rowCountQuery)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to query table row estimates")
	}

	var totalRows int64
	var tableCount int
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var table string
			var count int64
			if err := rows.Scan(&table, &count); err != nil {
				log.Warn().Err(err).Msg("Failed to scan row estimate")
				continue
			}
			metrics.SetDBTableRowCount(table, count)
			totalRows += count
			tableCount++
		}
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
		Int("tables", tableCount).
		Int64("total_rows", totalRows).
		Int64("db_size_bytes", dbSizeBytes).
		Dur("duration", time.Since(start)).
		Msg("Database metrics updated")
}

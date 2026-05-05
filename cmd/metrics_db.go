package cmd

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
)

// runDatabaseCollector periodically collects database table row counts. The
// pool is owned by runMetrics and shared with runCacheCollector.
func runDatabaseCollector(ctx context.Context, interval time.Duration, pool *pgxpool.Pool) {
	log.Info().Dur("interval", interval).Msg("Starting database collector")

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

// tableStatsQuery returns the row count and total on-disk size per public
// table. reltuples is replicated via WAL (works on standby replicas) — unlike
// pg_stat_user_tables.n_live_tup which is part of the per-instance cumulative
// stats system. GREATEST clamps the -1 sentinel PG14+ returns for tables that
// have never been analyzed. pg_total_relation_size covers heap + indexes +
// TOAST, matching what pg_database_size totals to.
const tableStatsQuery = `SELECT c.relname::text,
	   GREATEST(0, c.reltuples)::bigint AS rows,
	   pg_total_relation_size(c.oid)::bigint AS bytes
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = 'public' AND c.relkind = 'r'
	ORDER BY c.relname`

func collectDatabaseMetrics(ctx context.Context, pool *pgxpool.Pool) {
	start := time.Now()

	rows, err := pool.Query(ctx, tableStatsQuery)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to query table stats")
	}

	var totalRows, totalTableBytes int64
	var tableCount int
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var table string
			var count, bytes int64
			if err := rows.Scan(&table, &count, &bytes); err != nil {
				log.Warn().Err(err).Msg("Failed to scan table stats")
				continue
			}
			metrics.SetDBTableRowCount(table, count)
			metrics.SetDBTableSizeBytes(table, bytes)
			totalRows += count
			totalTableBytes += bytes
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
		Int64("total_table_bytes", totalTableBytes).
		Int64("db_size_bytes", dbSizeBytes).
		Dur("duration", time.Since(start)).
		Msg("Database metrics updated")
}

package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Collector metrics (cache, Redis, database), registered on CollectorRegistry
// in metrics.go.
var (
	cacheFilesExpected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_files_expected",
		Help: "Expected number of cache files",
	}, []string{"season", "file_type"})

	cacheFilesFound = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_files_found",
		Help: "Actual number of cache files found",
	}, []string{"season", "file_type"})

	cacheCompletenessPercent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_cache_completeness_percent",
		Help: "Cache completeness percentage (found/expected * 100)",
	}, []string{"season", "file_type"})

	cacheLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_stats_last_updated_timestamp",
		Help: "Unix timestamp of last cache stats update",
	})

	cacheComputeDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_stats_compute_duration_seconds",
		Help: "Time taken to compute cache statistics",
	})

	cacheDiskSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_cache_disk_size_bytes",
		Help: "Total disk space used by cache directory in bytes",
	})

	redisOAuthTokenValid = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_redis_oauth_token_valid",
		Help: "Whether a valid OAuth token exists for a user (1=valid, 0=missing/expired)",
	}, []string{"user"})

	redisLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_redis_last_updated_timestamp",
		Help: "Unix timestamp of last Redis metrics update",
	})

	dbTableRowCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_db_table_row_count",
		Help: "Number of rows in database tables",
	}, []string{"table"})

	dbTableSizeBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_db_table_size_bytes",
		Help: "Total on-disk size in bytes per table (heap + indexes + TOAST)",
	}, []string{"table"})

	dbSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_db_size_bytes",
		Help: "Total size of the database in bytes",
	})

	dbLastUpdated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "puckdb_db_last_updated_timestamp",
		Help: "Unix timestamp of last database metrics update",
	})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_build_info",
		Help: "Build version of the running binary",
	}, []string{"version"})

	dataPathFilesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_path_files_total",
		Help: "Total number of files by type in data path",
	}, []string{"file_type"})

	dataPathBytesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "puckdb_data_path_bytes_total",
		Help: "Total bytes by file type in data path",
	}, []string{"file_type"})
)

// SetCacheMetrics updates cache statistics gauges for a season and file type
func SetCacheMetrics(season, fileType string, expected, found int) {
	cacheFilesExpected.WithLabelValues(season, fileType).Set(float64(expected))
	cacheFilesFound.WithLabelValues(season, fileType).Set(float64(found))
	if expected > 0 {
		cacheCompletenessPercent.WithLabelValues(season, fileType).Set(float64(found) / float64(expected) * 100)
	} else {
		cacheCompletenessPercent.WithLabelValues(season, fileType).Set(0)
	}
}

// SetCacheMetricsTimestamp records when cache metrics were last computed
func SetCacheMetricsTimestamp() {
	cacheLastUpdated.Set(float64(time.Now().Unix()))
}

// SetCacheComputeDuration records how long the stats computation took
func SetCacheComputeDuration(d time.Duration) {
	cacheComputeDuration.Set(d.Seconds())
}

// SetCacheDiskSizeBytes records the total disk space used by the cache
func SetCacheDiskSizeBytes(bytes int64) {
	cacheDiskSizeBytes.Set(float64(bytes))
}

// SetRedisOAuthTokenValid records whether a user has a valid OAuth token
func SetRedisOAuthTokenValid(user string, valid bool) {
	val := 0.0
	if valid {
		val = 1.0
	}
	redisOAuthTokenValid.WithLabelValues(user).Set(val)
}

// SetRedisMetricsTimestamp records when Redis metrics were last computed
func SetRedisMetricsTimestamp() {
	redisLastUpdated.Set(float64(time.Now().Unix()))
}

// SetDBTableRowCount records the row count for a database table
func SetDBTableRowCount(table string, count int64) {
	dbTableRowCount.WithLabelValues(table).Set(float64(count))
}

// SetDBTableSizeBytes records the on-disk size in bytes for a database table.
// The value should come from pg_total_relation_size, which includes the heap,
// indexes, and TOAST.
func SetDBTableSizeBytes(table string, bytes int64) {
	dbTableSizeBytes.WithLabelValues(table).Set(float64(bytes))
}

// SetDBSizeBytes records the total database size in bytes
func SetDBSizeBytes(bytes int64) {
	dbSizeBytes.Set(float64(bytes))
}

// SetDBMetricsTimestamp records when database metrics were last computed
func SetDBMetricsTimestamp() {
	dbLastUpdated.Set(float64(time.Now().Unix()))
}

// SetBuildInfo records the build version as a label with gauge value 1
func SetBuildInfo(version string) {
	buildInfo.WithLabelValues(version).Set(1)
}

// SetDataPathFileStats records file count and size for a file type
func SetDataPathFileStats(fileType string, count int64, bytes int64) {
	dataPathFilesTotal.WithLabelValues(fileType).Set(float64(count))
	dataPathBytesTotal.WithLabelValues(fileType).Set(float64(bytes))
}

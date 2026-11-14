package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// RedisServerStats is one reading of the Redis server's INFO output.
type RedisServerStats struct {
	UptimeSeconds    int64
	ConnectedClients int64
	UsedMemoryBytes  int64
	// MaxMemoryBytes is the maxmemory setting; 0 means no limit.
	MaxMemoryBytes int64
	KeyspaceHits   int64
	KeyspaceMisses int64
	ExpiredKeys    int64
	EvictedKeys    int64
	// Databases holds the keyspace section: only databases with keys appear.
	Databases []RedisDatabaseStats
}

// RedisDatabaseStats is one logical database of the keyspace section.
type RedisDatabaseStats struct {
	// DB is the database number as Redis names it ("0" for db0).
	DB      string
	Keys    int64
	Expires int64
}

var (
	redisUptimeDesc = prometheus.NewDesc("puckdb_redis_uptime_seconds",
		"Seconds since the Redis server started", nil, nil)
	redisConnectedClientsDesc = prometheus.NewDesc("puckdb_redis_connected_clients",
		"Client connections open on the Redis server", nil, nil)
	redisUsedMemoryDesc = prometheus.NewDesc("puckdb_redis_used_memory_bytes",
		"Memory allocated by the Redis server", nil, nil)
	redisMaxMemoryDesc = prometheus.NewDesc("puckdb_redis_maxmemory_bytes",
		"The Redis server's maxmemory setting (0 = no limit)", nil, nil)
	redisKeyspaceHitsDesc = prometheus.NewDesc("puckdb_redis_keyspace_hits_total",
		"Successful key lookups since the Redis server started", nil, nil)
	redisKeyspaceMissesDesc = prometheus.NewDesc("puckdb_redis_keyspace_misses_total",
		"Failed key lookups since the Redis server started", nil, nil)
	redisExpiredKeysDesc = prometheus.NewDesc("puckdb_redis_expired_keys_total",
		"Keys removed on expiry since the Redis server started", nil, nil)
	redisEvictedKeysDesc = prometheus.NewDesc("puckdb_redis_evicted_keys_total",
		"Keys evicted by the maxmemory policy since the Redis server started", nil, nil)
	redisDBKeysDesc = prometheus.NewDesc("puckdb_redis_db_keys",
		"Keys in a Redis logical database", []string{"db"}, nil)
	redisDBExpiringKeysDesc = prometheus.NewDesc("puckdb_redis_db_expiring_keys",
		"Keys with an expiry in a Redis logical database", []string{"db"}, nil)
)

// redisServerCollector serves the last RedisServerStats reading, so a
// database emptied since the previous reading leaves no stale series. It
// serves nothing until the first reading.
type redisServerCollector struct {
	mu    sync.Mutex
	stats *RedisServerStats
}

var redisServer = &redisServerCollector{}

// SetRedisServerStats replaces the Redis server reading served by the
// collector registry.
func SetRedisServerStats(stats RedisServerStats) {
	redisServer.set(stats)
}

func (c *redisServerCollector) set(stats RedisServerStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats = &stats
}

func (c *redisServerCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		redisUptimeDesc, redisConnectedClientsDesc, redisUsedMemoryDesc, redisMaxMemoryDesc,
		redisKeyspaceHitsDesc, redisKeyspaceMissesDesc, redisExpiredKeysDesc, redisEvictedKeysDesc,
		redisDBKeysDesc, redisDBExpiringKeysDesc,
	} {
		ch <- d
	}
}

func (c *redisServerCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	stats := c.stats
	c.mu.Unlock()
	if stats == nil {
		return
	}
	gauge := func(d *prometheus.Desc, v int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, float64(v), labels...)
	}
	counter := func(d *prometheus.Desc, v int64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v))
	}
	gauge(redisUptimeDesc, stats.UptimeSeconds)
	gauge(redisConnectedClientsDesc, stats.ConnectedClients)
	gauge(redisUsedMemoryDesc, stats.UsedMemoryBytes)
	gauge(redisMaxMemoryDesc, stats.MaxMemoryBytes)
	counter(redisKeyspaceHitsDesc, stats.KeyspaceHits)
	counter(redisKeyspaceMissesDesc, stats.KeyspaceMisses)
	counter(redisExpiredKeysDesc, stats.ExpiredKeys)
	counter(redisEvictedKeysDesc, stats.EvictedKeys)
	for _, db := range stats.Databases {
		gauge(redisDBKeysDesc, db.Keys, db.DB)
		gauge(redisDBExpiringKeysDesc, db.Expires, db.DB)
	}
}

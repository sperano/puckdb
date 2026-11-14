package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisServerCollector_ServesNothingBeforeFirstReading(t *testing.T) {
	assert.Zero(t, testutil.CollectAndCount(&redisServerCollector{}))
}

func TestRedisServerCollector_ServesLatestReading(t *testing.T) {
	c := &redisServerCollector{}
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))

	c.set(RedisServerStats{
		UptimeSeconds: 100, ConnectedClients: 3, UsedMemoryBytes: 2048, MaxMemoryBytes: 4096,
		KeyspaceHits: 7, KeyspaceMisses: 2, ExpiredKeys: 5, EvictedKeys: 1,
		Databases: []RedisDatabaseStats{{DB: "0", Keys: 10, Expires: 4}, {DB: "3", Keys: 1}},
	})
	// db0 and db3 emptied since: their series must go. Every field differs,
	// so a value served under another series fails the comparison.
	c.set(RedisServerStats{
		UptimeSeconds: 101, ConnectedClients: 4, UsedMemoryBytes: 1024, MaxMemoryBytes: 8192,
		KeyspaceHits: 8, KeyspaceMisses: 6, ExpiredKeys: 11, EvictedKeys: 12,
		Databases: []RedisDatabaseStats{{DB: "1", Keys: 9, Expires: 2}},
	})

	expected := `
# HELP puckdb_redis_connected_clients Client connections open on the Redis server
# TYPE puckdb_redis_connected_clients gauge
puckdb_redis_connected_clients 4
# HELP puckdb_redis_db_expiring_keys Keys with an expiry in a Redis logical database
# TYPE puckdb_redis_db_expiring_keys gauge
puckdb_redis_db_expiring_keys{db="1"} 2
# HELP puckdb_redis_db_keys Keys in a Redis logical database
# TYPE puckdb_redis_db_keys gauge
puckdb_redis_db_keys{db="1"} 9
# HELP puckdb_redis_evicted_keys_total Keys evicted by the maxmemory policy since the Redis server started
# TYPE puckdb_redis_evicted_keys_total counter
puckdb_redis_evicted_keys_total 12
# HELP puckdb_redis_expired_keys_total Keys removed on expiry since the Redis server started
# TYPE puckdb_redis_expired_keys_total counter
puckdb_redis_expired_keys_total 11
# HELP puckdb_redis_keyspace_hits_total Successful key lookups since the Redis server started
# TYPE puckdb_redis_keyspace_hits_total counter
puckdb_redis_keyspace_hits_total 8
# HELP puckdb_redis_keyspace_misses_total Failed key lookups since the Redis server started
# TYPE puckdb_redis_keyspace_misses_total counter
puckdb_redis_keyspace_misses_total 6
# HELP puckdb_redis_maxmemory_bytes The Redis server's maxmemory setting (0 = no limit)
# TYPE puckdb_redis_maxmemory_bytes gauge
puckdb_redis_maxmemory_bytes 8192
# HELP puckdb_redis_uptime_seconds Seconds since the Redis server started
# TYPE puckdb_redis_uptime_seconds gauge
puckdb_redis_uptime_seconds 101
# HELP puckdb_redis_used_memory_bytes Memory allocated by the Redis server
# TYPE puckdb_redis_used_memory_bytes gauge
puckdb_redis_used_memory_bytes 1024
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected)))
}

func TestSetRedisServerStats_RegisteredOnCollectorRegistry(t *testing.T) {
	SetRedisServerStats(RedisServerStats{ConnectedClients: 2, Databases: []RedisDatabaseStats{{DB: "0", Keys: 5, Expires: 1}}})

	expected := `
# HELP puckdb_redis_connected_clients Client connections open on the Redis server
# TYPE puckdb_redis_connected_clients gauge
puckdb_redis_connected_clients 2
# HELP puckdb_redis_db_expiring_keys Keys with an expiry in a Redis logical database
# TYPE puckdb_redis_db_expiring_keys gauge
puckdb_redis_db_expiring_keys{db="0"} 1
`
	require.NoError(t, testutil.GatherAndCompare(CollectorRegistry, strings.NewReader(expected),
		"puckdb_redis_connected_clients", "puckdb_redis_db_expiring_keys"))
}

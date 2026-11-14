package cmd

import (
	"testing"

	"github.com/sperano/puckdb/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleRedisInfo = "# Server\r\n" +
	"redis_version:7.2.4\r\n" +
	"uptime_in_seconds:86400\r\n" +
	"\r\n" +
	"# Clients\r\n" +
	"connected_clients:12\r\n" +
	"\r\n" +
	"# Memory\r\n" +
	"used_memory:1048576\r\n" +
	"used_memory_human:1.00M\r\n" +
	"maxmemory:0\r\n" +
	"\r\n" +
	"# Stats\r\n" +
	"expired_keys:40\r\n" +
	"evicted_keys:3\r\n" +
	"keyspace_hits:900\r\n" +
	"keyspace_misses:100\r\n" +
	"\r\n" +
	"# Replication\r\n" +
	"role:master\r\n" +
	"\r\n" +
	"# Keyspace\r\n" +
	"db0:keys=1234,expires=56,avg_ttl=789\r\n" +
	"db2:keys=7,expires=0,avg_ttl=0,subexpiry=0\r\n"

func TestParseRedisInfo(t *testing.T) {
	stats, err := parseRedisInfo(sampleRedisInfo)
	require.NoError(t, err)

	assert.Equal(t, metrics.RedisServerStats{
		UptimeSeconds:    86400,
		ConnectedClients: 12,
		UsedMemoryBytes:  1048576,
		MaxMemoryBytes:   0,
		KeyspaceHits:     900,
		KeyspaceMisses:   100,
		ExpiredKeys:      40,
		EvictedKeys:      3,
		Databases: []metrics.RedisDatabaseStats{
			{DB: "0", Keys: 1234, Expires: 56},
			{DB: "2", Keys: 7, Expires: 0},
		},
	}, stats)
}

func TestParseRedisInfo_EmptyKeyspace(t *testing.T) {
	stats, err := parseRedisInfo("# Keyspace\r\n")
	require.NoError(t, err)
	assert.Empty(t, stats.Databases)
}

func TestParseRedisInfo_IgnoresNonKeyspaceDBFields(t *testing.T) {
	// "dbsize"-like fields share the "db" prefix without being a database.
	stats, err := parseRedisInfo("dbfilename:dump.rdb\r\ndb0:keys=1,expires=0\r\n")
	require.NoError(t, err)
	assert.Equal(t, []metrics.RedisDatabaseStats{{DB: "0", Keys: 1}}, stats.Databases)
}

func TestParseRedisInfo_RejectsMalformedNumbers(t *testing.T) {
	for name, info := range map[string]string{
		"field":    "used_memory:lots\r\n",
		"keyspace": "db0:keys=many,expires=0\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseRedisInfo(info)
			assert.Error(t, err)
		})
	}
}

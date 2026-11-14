package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/stretchr/testify/require"
)

// seededRedisExposition is the collector registry's reading of
// seededRedisStats for the series the tests compare.
const seededRedisExposition = `
# HELP puckdb_redis_connected_clients Client connections open on the Redis server
# TYPE puckdb_redis_connected_clients gauge
puckdb_redis_connected_clients 5
# HELP puckdb_redis_db_keys Keys in a Redis logical database
# TYPE puckdb_redis_db_keys gauge
puckdb_redis_db_keys{db="0"} 7
`

var seededRedisStats = metrics.RedisServerStats{
	ConnectedClients: 5,
	Databases:        []metrics.RedisDatabaseStats{{DB: "0", Keys: 7}},
}

func TestCollectRedisServerStats(t *testing.T) {
	tests := []struct {
		name     string
		expect   func(mock redismock.ClientMock)
		expected string
	}{
		{
			name:     "INFO error keeps the previous reading",
			expect:   func(mock redismock.ClientMock) { mock.ExpectInfo().SetErr(errors.New("connection refused")) },
			expected: seededRedisExposition,
		},
		{
			name:     "unparsable INFO keeps the previous reading",
			expect:   func(mock redismock.ClientMock) { mock.ExpectInfo().SetVal("# Clients\r\nconnected_clients:many\r\n") },
			expected: seededRedisExposition,
		},
		{
			name: "INFO replaces the reading",
			expect: func(mock redismock.ClientMock) {
				mock.ExpectInfo().SetVal("# Clients\r\nconnected_clients:2\r\n# Keyspace\r\ndb2:keys=3,expires=0,avg_ttl=0\r\n")
			},
			expected: `
# HELP puckdb_redis_connected_clients Client connections open on the Redis server
# TYPE puckdb_redis_connected_clients gauge
puckdb_redis_connected_clients 2
# HELP puckdb_redis_db_keys Keys in a Redis logical database
# TYPE puckdb_redis_db_keys gauge
puckdb_redis_db_keys{db="2"} 3
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.SetRedisServerStats(seededRedisStats)
			client, mock := redismock.NewClientMock()
			tt.expect(mock)

			collectRedisServerStats(context.Background(), client)

			require.NoError(t, mock.ExpectationsWereMet())
			require.NoError(t, testutil.GatherAndCompare(metrics.CollectorRegistry, strings.NewReader(tt.expected),
				"puckdb_redis_connected_clients", "puckdb_redis_db_keys"))
		})
	}
}

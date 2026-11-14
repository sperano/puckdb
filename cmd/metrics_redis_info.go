package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sperano/puckdb/internal/metrics"
)

// redisKeyspacePrefix starts each keyspace line of INFO ("db0:keys=1,...").
const redisKeyspacePrefix = "db"

// parseRedisInfo reads the INFO fields metrics.RedisServerStats holds. A
// field the server does not report stays zero; a field it reports with a
// value that is not an integer is an error.
func parseRedisInfo(info string) (metrics.RedisServerStats, error) {
	var stats metrics.RedisServerStats
	fields := map[string]*int64{
		"uptime_in_seconds": &stats.UptimeSeconds,
		"connected_clients": &stats.ConnectedClients,
		"used_memory":       &stats.UsedMemoryBytes,
		"maxmemory":         &stats.MaxMemoryBytes,
		"keyspace_hits":     &stats.KeyspaceHits,
		"keyspace_misses":   &stats.KeyspaceMisses,
		"expired_keys":      &stats.ExpiredKeys,
		"evicted_keys":      &stats.EvictedKeys,
	}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if target, known := fields[key]; known {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return metrics.RedisServerStats{}, fmt.Errorf("redis info %s=%q: %w", key, value, err)
			}
			*target = n
			continue
		}
		if db, isDB := strings.CutPrefix(key, redisKeyspacePrefix); isDB && isDecimal(db) {
			dbStats, err := parseRedisKeyspace(db, value)
			if err != nil {
				return metrics.RedisServerStats{}, err
			}
			stats.Databases = append(stats.Databases, dbStats)
		}
	}
	return stats, nil
}

// parseRedisKeyspace reads one keyspace value ("keys=12,expires=3,avg_ttl=0").
func parseRedisKeyspace(db, value string) (metrics.RedisDatabaseStats, error) {
	stats := metrics.RedisDatabaseStats{DB: db}
	for _, pair := range strings.Split(value, ",") {
		name, raw, _ := strings.Cut(pair, "=")
		var target *int64
		switch name {
		case "keys":
			target = &stats.Keys
		case "expires":
			target = &stats.Expires
		default:
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return metrics.RedisDatabaseStats{}, fmt.Errorf("redis keyspace db%s %s=%q: %w", db, name, raw, err)
		}
		*target = n
	}
	return stats, nil
}

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

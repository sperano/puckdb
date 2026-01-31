package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
)

// Redis key constants
const (
	DataPathStatsKeyPrefix = "puckdb:worker:data-stats:"
)

// DataPathStats holds the file statistics for a worker's data path
type DataPathStats struct {
	FileCount    int64  `json:"file_count"`
	TotalBytes   int64  `json:"total_bytes"`
	LastScanUnix int64  `json:"last_scan_unix"`
	DataPath     string `json:"data_path"`
	WorkerID     string `json:"worker_id"`
}

// DataPathScanner periodically scans the data path and writes stats to Redis
type DataPathScanner struct {
	redisClient *redis.Client
	dataPath    string
	workerID    string
	interval    time.Duration
	ttl         time.Duration
	stop        chan struct{}
	done        chan struct{}
}

// NewDataPathScanner creates a new scanner for the given data path
func NewDataPathScanner(redisClient *redis.Client, dataPath string) *DataPathScanner {
	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s:%d", hostname, os.Getpid())

	intervalSecs := viper.GetInt(config.FlagDataPathScanInterval)
	if intervalSecs <= 0 {
		intervalSecs = config.DefaultDataPathScanInterval
	}

	ttlSecs := viper.GetInt(config.FlagDataPathStatsTTL)
	if ttlSecs <= 0 {
		ttlSecs = config.DefaultDataPathStatsTTL
	}

	return &DataPathScanner{
		redisClient: redisClient,
		dataPath:    dataPath,
		workerID:    workerID,
		interval:    time.Duration(intervalSecs) * time.Second,
		ttl:         time.Duration(ttlSecs) * time.Second,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Start begins the periodic scanning goroutine
func (s *DataPathScanner) Start() {
	go func() {
		defer close(s.done)

		// Scan immediately on start
		s.scanAndWrite()

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-s.stop:
				// Clean up on graceful shutdown
				s.cleanup()
				return
			case <-ticker.C:
				s.scanAndWrite()
			}
		}
	}()

	log.Info().
		Str("data_path", s.dataPath).
		Str("worker_id", s.workerID).
		Dur("interval", s.interval).
		Dur("ttl", s.ttl).
		Msg("Data path scanner started")
}

// Stop signals the scanner to stop and waits for completion
func (s *DataPathScanner) Stop() {
	close(s.stop)
	<-s.done
	log.Info().Str("worker_id", s.workerID).Msg("Data path scanner stopped")
}

// scanAndWrite scans the data path and writes stats to Redis and Prometheus
func (s *DataPathScanner) scanAndWrite() {
	stats, err := s.scan()
	if err != nil {
		log.Error().Err(err).Str("data_path", s.dataPath).Msg("Failed to scan data path")
		return
	}

	// Update Prometheus metrics (exposed on this worker's /metrics endpoint)
	SetDataPathMetrics(s.workerID, s.dataPath, stats.FileCount, stats.TotalBytes, stats.LastScanUnix)

	// Write to Redis (for aggregation across all workers)
	if err := s.writeToRedis(stats); err != nil {
		log.Error().Err(err).Msg("Failed to write data path stats to Redis")
		return
	}

	log.Debug().
		Int64("file_count", stats.FileCount).
		Int64("total_bytes", stats.TotalBytes).
		Str("worker_id", s.workerID).
		Msg("Data path stats updated")
}

// scan walks the data path and collects file statistics
func (s *DataPathScanner) scan() (*DataPathStats, error) {
	var fileCount int64
	var totalBytes int64

	err := filepath.WalkDir(s.dataPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Log but continue on permission errors
			log.Warn().Err(err).Str("path", path).Msg("Error accessing path")
			return nil
		}

		if !d.IsDir() {
			fileCount++
			info, err := d.Info()
			if err == nil {
				totalBytes += info.Size()
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking data path: %w", err)
	}

	return &DataPathStats{
		FileCount:    fileCount,
		TotalBytes:   totalBytes,
		LastScanUnix: time.Now().Unix(),
		DataPath:     s.dataPath,
		WorkerID:     s.workerID,
	}, nil
}

// writeToRedis writes the stats to Redis with TTL
func (s *DataPathScanner) writeToRedis(stats *DataPathStats) error {
	ctx := context.Background()
	key := DataPathStatsKeyPrefix + s.workerID

	data, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("marshaling stats: %w", err)
	}

	return s.redisClient.Set(ctx, key, data, s.ttl).Err()
}

// cleanup removes this worker's stats from Redis and Prometheus on graceful shutdown
func (s *DataPathScanner) cleanup() {
	// Clear Prometheus metrics
	ClearDataPathMetrics(s.workerID, s.dataPath)

	// Delete Redis key
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := DataPathStatsKeyPrefix + s.workerID
	if err := s.redisClient.Del(ctx, key).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Failed to delete data path stats on shutdown")
	} else {
		log.Debug().Str("key", key).Msg("Deleted data path stats on shutdown")
	}
}

// GetAllDataPathStats retrieves stats from all workers stored in Redis
func GetAllDataPathStats(ctx context.Context, redisClient *redis.Client) ([]DataPathStats, error) {
	pattern := DataPathStatsKeyPrefix + "*"
	keys, err := redisClient.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}

	if len(keys) == 0 {
		return nil, nil
	}

	var stats []DataPathStats
	for _, key := range keys {
		data, err := redisClient.Get(ctx, key).Bytes()
		if err != nil {
			if err == redis.Nil {
				continue // Key expired between Keys() and Get()
			}
			log.Warn().Err(err).Str("key", key).Msg("Failed to get data path stats")
			continue
		}

		var s DataPathStats
		if err := json.Unmarshal(data, &s); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to unmarshal data path stats")
			continue
		}
		stats = append(stats, s)
	}

	return stats, nil
}

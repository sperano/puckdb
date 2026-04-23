package admin

import (
	"context"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"go.temporal.io/sdk/activity"
)

// DropDatabaseActivity drops all database tables by running down migrations.
func DropDatabaseActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("DropDatabaseActivity: dropping all tables")
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	return database.DropEverything(ctx, pool)
}

// MigrateDatabaseActivity runs database migrations.
func MigrateDatabaseActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("MigrateDatabaseActivity: running migrations")
	return database.DoMigration()
}

// FlushRedisActivity flushes all keys from the configured Redis database.
func FlushRedisActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("FlushRedisActivity: flushing Redis DB")
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("failed to close redis client", "error", err)
		}
	}()
	return cache.FlushDB(ctx, redisClient)
}

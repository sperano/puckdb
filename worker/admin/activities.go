package admin

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
)

// DropDatabaseActivity drops all database tables by running down migrations.
func DropDatabaseActivity(ctx context.Context) error {
	log.Info().Msg("DropDatabaseActivity: dropping all tables")
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	return database.DropEverything(ctx, pool)
}

// MigrateDatabaseActivity runs database migrations.
func MigrateDatabaseActivity(ctx context.Context) error {
	log.Info().Msg("MigrateDatabaseActivity: running migrations")
	return database.DoMigration()
}

// FlushRedisActivity flushes all keys from the configured Redis database.
func FlushRedisActivity(ctx context.Context) error {
	log.Info().Msg("FlushRedisActivity: flushing Redis DB")
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()
	return cache.FlushDB(ctx, redisClient)
}

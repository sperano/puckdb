package admin

import (
	"context"
	"errors"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/database"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// dirtyMigrationErrorType is the Temporal application error type reported
// for a dirty migration state.
const dirtyMigrationErrorType = "DirtyMigrationError"

// failFastIfDirty makes a dirty migration state non-retryable: only an
// operator can repair it, so retrying just delays the actionable error.
func failFastIfDirty(err error) error {
	var dirtyErr *database.DirtyMigrationError
	if errors.As(err, &dirtyErr) {
		return temporal.NewNonRetryableApplicationError(err.Error(), dirtyMigrationErrorType, err)
	}
	return err
}

// Activities holds the worker's long-lived clients for the admin
// activities. Temporal registers each method under its own name, so the
// activity type names in workflow history are the method names.
type Activities struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

// DropDatabaseActivity drops all database tables by running down migrations.
func (a *Activities) DropDatabaseActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("DropDatabaseActivity: dropping all tables")
	return failFastIfDirty(database.DropEverything(ctx, a.Pool))
}

// MigrateDatabaseActivity runs database migrations.
func (a *Activities) MigrateDatabaseActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("MigrateDatabaseActivity: running migrations")
	return failFastIfDirty(database.DoMigration())
}

// FlushRedisActivity flushes all keys from the configured Redis database.
func (a *Activities) FlushRedisActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("FlushRedisActivity: flushing Redis DB")
	return cache.FlushDB(ctx, a.Redis)
}

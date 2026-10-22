// Package admin holds the database and Redis administration
// workflows/activities (DropDatabaseWorkflow, MigrateDatabaseWorkflow,
// ResetDatabaseWorkflow, FlushRedisWorkflow) that the worker command
// executes; the api command's admin GraphQL mutations (dropDatabase,
// createDatabase, clearDatabase, flushRedisDB) start them. Of the CLI's
// db/redis subcommands, only `db init`, `db drop` and `redis flush` reach
// those mutations over HTTP; `db migrate`, `db force-version`,
// `db provision` and `db check-teams` operate on PostgreSQL directly,
// bypassing the API.
package admin

import (
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowIDDropDatabase    = "drop-database"
	WorkflowIDMigrateDatabase = "migrate-database"
	WorkflowIDResetDatabase   = "reset-database"
	WorkflowIDFlushRedis      = "flush-redis"
)

// DropDatabaseWorkflow drops all database tables.
func DropDatabaseWorkflow(ctx workflow.Context) error {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return workflow.ExecuteActivity(ctx, a.DropDatabaseActivity).Get(ctx, nil)
}

// MigrateDatabaseWorkflow runs database migrations.
func MigrateDatabaseWorkflow(ctx workflow.Context) error {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return workflow.ExecuteActivity(ctx, a.MigrateDatabaseActivity).Get(ctx, nil)
}

// ResetDatabaseWorkflow drops all tables and recreates them via migrations.
func ResetDatabaseWorkflow(ctx workflow.Context) error {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Drop all tables
	if err := workflow.ExecuteActivity(ctx, a.DropDatabaseActivity).Get(ctx, nil); err != nil {
		return err
	}

	// Run migrations
	return workflow.ExecuteActivity(ctx, a.MigrateDatabaseActivity).Get(ctx, nil)
}

// FlushRedisWorkflow flushes all keys from the configured Redis database.
func FlushRedisWorkflow(ctx workflow.Context) error {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return workflow.ExecuteActivity(ctx, a.FlushRedisActivity).Get(ctx, nil)
}

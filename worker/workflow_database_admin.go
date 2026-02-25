package worker

import (
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
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	return workflow.ExecuteActivity(ctx, DropDatabaseActivity).Get(ctx, nil)
}

// MigrateDatabaseWorkflow runs database migrations.
func MigrateDatabaseWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	return workflow.ExecuteActivity(ctx, MigrateDatabaseActivity).Get(ctx, nil)
}

// ResetDatabaseWorkflow drops all tables and recreates them via migrations.
func ResetDatabaseWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Drop all tables
	if err := workflow.ExecuteActivity(ctx, DropDatabaseActivity).Get(ctx, nil); err != nil {
		return err
	}

	// Run migrations
	return workflow.ExecuteActivity(ctx, MigrateDatabaseActivity).Get(ctx, nil)
}

// FlushRedisWorkflow flushes all keys from the configured Redis database.
func FlushRedisWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	return workflow.ExecuteActivity(ctx, FlushRedisActivity).Get(ctx, nil)
}

package temporal

const (
	QueueTasks = "puckdb-tasks"
	QueueAdmin = "puckdb-admin"
	// The asset task queue lives in worker/shared.TaskQueueAssets so the
	// worker/workflow package can reference it without depending on this
	// (client-config) package.
)

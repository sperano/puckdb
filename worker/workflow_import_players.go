package worker

import (
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDImportPlayers = "import-players"

// ImportPlayersWorkflow imports player data into the database.
func ImportPlayersWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	// Register progress query handler
	tracker := NewProgressTracker(1)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	logger.Info("ImportPlayersWorkflow started")

	// TODO: Implement actual import logic
	logger.Info("Hello World from ImportPlayersWorkflow!")

	tracker.Increment()

	logger.Info("ImportPlayersWorkflow completed")

	return nil
}

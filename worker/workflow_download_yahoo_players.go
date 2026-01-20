package worker

import (
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDDownloadYahooPlayers = "download-yahoo-players"

// DownloadYahooPlayersInput contains parameters for the workflow, supporting ContinueAsNew.
type DownloadYahooPlayersInput struct {
	StartPlayerID  int // First player ID to process in this execution (1-based)
	TotalCompleted int // Cumulative completed count from previous executions
}

// DownloadYahooPlayersWorkflow downloads all Yahoo player pages from ID 1 to max-yahoo-player-id.
// Uses ContinueAsNew to avoid hitting Temporal's history size limit.
func DownloadYahooPlayersWorkflow(ctx workflow.Context, input *DownloadYahooPlayersInput) error {
	logger := workflow.GetLogger(ctx)

	startID := 1
	totalCompleted := 0
	if input != nil {
		if input.StartPlayerID > 0 {
			startID = input.StartPlayerID
		}
		totalCompleted = input.TotalCompleted
	}

	maxPlayerID := viper.GetInt(config.FlagMaxYahooPlayerID)
	concurrency := viper.GetInt(config.FlagYahooPlayerBatchSize)
	playersPerExecution := viper.GetInt(config.FlagYahooPlayersPerExecution)

	// Calculate this execution's range
	endID := startID + playersPerExecution - 1
	if endID > maxPlayerID {
		endID = maxPlayerID
	}
	batchSize := endID - startID + 1

	logger.Info("DownloadYahooPlayersWorkflow started",
		"startID", startID,
		"endID", endID,
		"maxPlayerID", maxPlayerID,
		"concurrency", concurrency,
		"totalCompleted", totalCompleted)

	// Track progress for this execution, reporting cumulative total
	tracker := NewProgressTrackerWithOffset(batchSize, totalCompleted, maxPlayerID)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	activityCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())
	startActivity := func(ctx workflow.Context, index int) workflow.Future {
		playerID := startID + index
		return workflow.ExecuteActivity(activityCtx, DownloadYahooPlayer, playerID)
	}

	if err := tracker.RunWorkerPool(ctx, batchSize, concurrency, startActivity); err != nil {
		return err
	}

	// Continue with next batch if more players remain
	if endID < maxPlayerID {
		return workflow.NewContinueAsNewError(ctx, DownloadYahooPlayersWorkflow,
			&DownloadYahooPlayersInput{
				StartPlayerID:  endID + 1,
				TotalCompleted: totalCompleted + batchSize,
			})
	}

	logger.Info("DownloadYahooPlayersWorkflow completed", "maxPlayerID", maxPlayerID)
	return nil
}

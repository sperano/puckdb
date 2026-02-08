package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDDownloadYahooPlayers = "download-yahoo-players"

// DownloadYahooPlayersInput contains parameters for the workflow, supporting ContinueAsNew.
type DownloadYahooPlayersInput struct {
	StartPlayerID  int       // First player ID to process in this execution (1-based)
	TotalCompleted int       // Cumulative completed count from previous executions
	TotalFound     int       // Cumulative found players (downloaded + cached, excludes 404s)
	StartedAt      time.Time // Original workflow start time (for elapsed calculation)
}

// DownloadYahooPlayersWorkflow downloads all Yahoo player pages from ID 1 to max-yahoo-player-id.
// Uses ContinueAsNew to avoid hitting Temporal's history size limit.
func DownloadYahooPlayersWorkflow(ctx workflow.Context, input *DownloadYahooPlayersInput) error {
	logger := workflow.GetLogger(ctx)

	startID := 1
	totalCompleted := 0
	totalFound := 0
	startedAt := workflow.Now(ctx)
	if input != nil {
		if input.StartPlayerID > 0 {
			startID = input.StartPlayerID
		}
		totalCompleted = input.TotalCompleted
		totalFound = input.TotalFound
		if !input.StartedAt.IsZero() {
			startedAt = input.StartedAt
		}
	}

	maxPlayerID := viper.GetInt(config.FlagMaxYahooPlayerID)
	concurrency := viper.GetInt(config.FlagYahooPlayerBatchSize)
	activityBatchSize := viper.GetInt(config.FlagYahooPlayerActivityBatchSize)
	playersPerExecution := viper.GetInt(config.FlagYahooPlayersPerExecution)

	// Calculate this execution's range
	endID := startID + playersPerExecution - 1
	if endID > maxPlayerID {
		endID = maxPlayerID
	}
	totalPlayers := endID - startID + 1

	// Calculate number of activity batches needed
	numActivityBatches := (totalPlayers + activityBatchSize - 1) / activityBatchSize

	logger.Info("DownloadYahooPlayersWorkflow started",
		"startID", startID,
		"endID", endID,
		"maxPlayerID", maxPlayerID,
		"concurrency", concurrency,
		"activityBatchSize", activityBatchSize,
		"numActivityBatches", numActivityBatches,
		"totalCompleted", totalCompleted)

	// Track progress with single phase for phase-based display
	const phaseID = 1
	tracker := NewProgressTrackerSinglePhase("Downloading Yahoo! players...", maxPlayerID, totalCompleted)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	activityCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStartID := startID + (batchIndex * activityBatchSize)
		batchEndID := batchStartID + activityBatchSize - 1
		if batchEndID > endID {
			batchEndID = endID
		}
		return workflow.ExecuteActivity(activityCtx, DownloadYahooPlayerBatch, batchStartID, batchEndID)
	}

	executionFound := 0
	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var result DownloadYahooPlayerBatchResult
		if err := future.Get(ctx, &result); err != nil {
			return err
		}
		executionFound += result.Downloaded + result.Cached
		return nil
	}

	if err := tracker.RunWorkerPoolForItem(ctx, numActivityBatches, concurrency, phaseID, startActivity, handler); err != nil {
		return err
	}

	// Continue with next batch if more players remain
	if endID < maxPlayerID {
		return workflow.NewContinueAsNewError(ctx, DownloadYahooPlayersWorkflow,
			&DownloadYahooPlayersInput{
				StartPlayerID:  endID + 1,
				TotalCompleted: totalCompleted + totalPlayers,
				TotalFound:     totalFound + executionFound,
				StartedAt:      startedAt,
			})
	}

	// Mark phase complete with final count and total elapsed time
	finalFound := totalFound + executionFound
	finalCount := totalCompleted + totalPlayers
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.SetItemCompletedDescription(phaseID, fmt.Sprintf("Found %d/%d Yahoo! players in %s.", finalFound, finalCount, elapsed))
	tracker.MarkItemCompleted(ctx, phaseID)

	logger.Info("DownloadYahooPlayersWorkflow completed", "maxPlayerID", maxPlayerID)
	return nil
}

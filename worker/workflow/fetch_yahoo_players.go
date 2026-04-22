package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/yahoo"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDFetchYahooPlayers = "fetch-yahoo-players"

// FetchYahooPlayersInput contains parameters for the workflow, supporting ContinueAsNew.
type FetchYahooPlayersInput struct {
	StartPlayerID  int       // First player ID to process in this execution (1-based)
	TotalCompleted int       // Cumulative completed count from previous executions
	TotalFound     int       // Cumulative found players (network downloads + cache hits, excludes 404s)
	StartedAt      time.Time // Original workflow start time (for elapsed calculation)
}

// Group index for FetchYahooPlayers workflow progress
const GroupFetchYahooPlayers = 0

// NewFetchYahooPlayersProgressReport creates the initial progress structure.
// completed is the cumulative count from previous ContinueAsNew executions.
func NewFetchYahooPlayersProgressReport(total, completed int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Total:     total,
		Completed: completed,
		Groups: []shared.ProgressGroup{
			{Header: "Fetching Yahoo! players...", Bars: []shared.ProgressBar{{Total: total, Current: completed}}},
		},
	}
}

// FetchYahooPlayersWorkflow fetches all Yahoo player pages from ID 1 to max-yahoo-player-id.
// Uses ContinueAsNew to avoid hitting Temporal's history size limit.
func FetchYahooPlayersWorkflow(ctx workflow.Context, input *FetchYahooPlayersInput) error {
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

	logger.Info("FetchYahooPlayersWorkflow started",
		"startID", startID,
		"endID", endID,
		"maxPlayerID", maxPlayerID,
		"concurrency", concurrency,
		"activityBatchSize", activityBatchSize,
		"numActivityBatches", numActivityBatches,
		"totalCompleted", totalCompleted)

	// Set up progress tracking with the new ReportTracker
	tracker, err := shared.InitTracker(ctx, NewFetchYahooPlayersProgressReport(maxPlayerID, totalCompleted))
	if err != nil {
		return err
	}
	tracker.StartGroup(ctx, GroupFetchYahooPlayers)

	var yahooAct *yahoo.FetchActivities
	activityCtx := workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStartID := startID + (batchIndex * activityBatchSize)
		batchEndID := batchStartID + activityBatchSize - 1
		if batchEndID > endID {
			batchEndID = endID
		}
		return workflow.ExecuteActivity(activityCtx, yahooAct.FetchYahooPlayerBatch, batchStartID, batchEndID)
	}

	executionFound := 0
	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var result shared.FetchStats
		if err := future.Get(ctx, &result); err != nil {
			return err
		}
		executionFound += result.Downloaded + result.CacheHits
		return nil
	}

	// Run worker pool with activity batch size as increment (each activity handles a batch of players)
	if err := tracker.RunWorkerPoolWithIncrement(ctx, GroupFetchYahooPlayers, 0, numActivityBatches, concurrency, func(_ int) int { return activityBatchSize }, startActivity, handler); err != nil {
		return err
	}

	// Continue with next batch if more players remain
	if endID < maxPlayerID {
		return workflow.NewContinueAsNewError(ctx, FetchYahooPlayersWorkflow,
			&FetchYahooPlayersInput{
				StartPlayerID:  endID + 1,
				TotalCompleted: totalCompleted + totalPlayers,
				TotalFound:     totalFound + executionFound,
				StartedAt:      startedAt,
			})
	}

	// Mark group complete with final count and total elapsed time
	finalFound := totalFound + executionFound
	finalCount := totalCompleted + totalPlayers
	elapsed := shared.FormatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.CompleteGroup(ctx, GroupFetchYahooPlayers,
		fmt.Sprintf("Found %d/%d Yahoo! players in %s.", finalFound, finalCount, elapsed))

	logger.Info("FetchYahooPlayersWorkflow completed", "maxPlayerID", maxPlayerID)
	return nil
}

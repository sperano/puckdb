package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDFetchYahooPlayers = "fetch-yahoo-players"

// FetchYahooPlayersInput contains parameters for the workflow, supporting ContinueAsNew.
type FetchYahooPlayersInput struct {
	StartPlayerID  int       // First player ID to process in this execution (1-based)
	TotalCompleted int       // Cumulative completed count from previous executions
	TotalFound     int       // Cumulative found players (network downloads + cache hits, excludes 404s)
	StartedAt      time.Time // Original workflow start time (for elapsed calculation)

	// Config is snapshotted once, on the first execution of a logical run, and
	// carried across every ContinueAsNew so every execution of that run uses
	// the settings the run started with instead of re-reading process-local
	// viper flags that may have changed by the time a later execution
	// replays. nil on the first execution and on inputs produced before this
	// field existed, in which case the workflow snapshots fresh.
	Config *FetchYahooPlayersConfig
}

// FetchYahooPlayersConfig is the configuration FetchYahooPlayersWorkflow
// snapshots once per logical run: every value here shapes how many activity
// batches get scheduled and where the run stops before ContinueAsNew, so none
// of them may be re-read on replay.
type FetchYahooPlayersConfig struct {
	MaxPlayerID         int `json:"maxPlayerID"`
	Concurrency         int `json:"concurrency"`
	ActivityBatchSize   int `json:"activityBatchSize"`
	PlayersPerExecution int `json:"playersPerExecution"`
}

// loadFetchYahooPlayersConfig resolves FetchYahooPlayersConfig from viper with
// the package defaults as fallback, so a carried snapshot never freezes a zero
// (which would loop ContinueAsNew forever) into a run.
func loadFetchYahooPlayersConfig() FetchYahooPlayersConfig {
	return FetchYahooPlayersConfig{
		MaxPlayerID:         shared.ResolveConfigInt(nil, shared.MaxYahooPlayerIDParam, nil),
		Concurrency:         shared.ResolveConfigInt(nil, shared.YahooPlayerConcurrencyParam, nil),
		ActivityBatchSize:   shared.ResolveConfigInt(nil, shared.YahooPlayerActivityBatchSizeParam, nil),
		PlayersPerExecution: shared.ResolveConfigInt(nil, shared.YahooPlayersPerExecutionParam, nil),
	}
}

// resolveFetchYahooPlayersConfig returns carried when the caller supplied one
// (a ContinueAsNew resumption within the same logical run), or snapshots a
// fresh FetchYahooPlayersConfig for the first execution of a run.
func resolveFetchYahooPlayersConfig(ctx workflow.Context, carried *FetchYahooPlayersConfig) (*FetchYahooPlayersConfig, error) {
	if carried != nil {
		return carried, nil
	}
	snapshot, err := shared.SnapshotConfig(ctx, loadFetchYahooPlayersConfig)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// fetchYahooPlayersState is the continuation state carried across
// ContinueAsNew executions of one logical run. Distinct from
// FetchYahooPlayersConfig, which is the snapshotted settings for the run.
type fetchYahooPlayersState struct {
	StartID        int
	TotalCompleted int
	TotalFound     int
	StartedAt      time.Time
}

// parseFetchYahooPlayersState extracts the continuation state from input,
// defaulting to a fresh run's starting point when input is nil (the first
// execution).
func parseFetchYahooPlayersState(ctx workflow.Context, input *FetchYahooPlayersInput) fetchYahooPlayersState {
	state := fetchYahooPlayersState{StartID: 1, StartedAt: workflow.Now(ctx)}
	if input == nil {
		return state
	}
	if input.StartPlayerID > 0 {
		state.StartID = input.StartPlayerID
	}
	state.TotalCompleted = input.TotalCompleted
	state.TotalFound = input.TotalFound
	if !input.StartedAt.IsZero() {
		state.StartedAt = input.StartedAt
	}
	return state
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

	state := parseFetchYahooPlayersState(ctx, input)
	startID := state.StartID
	totalCompleted := state.TotalCompleted
	totalFound := state.TotalFound
	startedAt := state.StartedAt

	var carriedConfig *FetchYahooPlayersConfig
	if input != nil {
		carriedConfig = input.Config
	}
	cfg, err := resolveFetchYahooPlayersConfig(ctx, carriedConfig)
	if err != nil {
		return err
	}

	maxPlayerID := cfg.MaxPlayerID
	concurrency := cfg.Concurrency
	activityBatchSize := cfg.ActivityBatchSize
	playersPerExecution := cfg.PlayersPerExecution

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
				Config:         cfg,
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

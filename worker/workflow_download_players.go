package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDDownloadPlayers = "download-players"

const (
	phaseExtractPlayerIDs       = 1
	phaseDownloadPlayerLandings = 2
)

// downloadPlayersInternalInput supports ContinueAsNew between phases.
type downloadPlayersInternalInput struct {
	// Phase 1 params (from original input)
	StartSeason       *int
	EndSeason         *int
	SeasonConcurrency *int

	// Phase 2 params (populated after Phase 1)
	PlayerIDs      []int64
	StartIndex     int
	TotalCompleted int
	Phase          int
	StartedAt      time.Time // Original start time for elapsed calculation

	// Progress state preserved across ContinueAsNew
	Phase1CompletedDesc string // Completion message from Phase 1
}

// DownloadPlayersWorkflow extracts player IDs from boxscores and downloads their landing pages.
func DownloadPlayersWorkflow(ctx workflow.Context, input *model.DownloadSeasonsInput) error {
	// Entry point: start with Phase 1
	internalInput := &downloadPlayersInternalInput{
		StartSeason:       input.StartSeason,
		EndSeason:         input.EndSeason,
		SeasonConcurrency: input.SeasonConcurrency,
		Phase:             phaseExtractPlayerIDs,
	}
	return downloadPlayersWorkflowImpl(ctx, internalInput)
}

// DownloadPlayersWorkflowContinue is the entry point for ContinueAsNew.
func DownloadPlayersWorkflowContinue(ctx workflow.Context, input *downloadPlayersInternalInput) error {
	return downloadPlayersWorkflowImpl(ctx, input)
}

func downloadPlayersWorkflowImpl(ctx workflow.Context, input *downloadPlayersInternalInput) error {
	if input.Phase == phaseExtractPlayerIDs {
		return runPhase1ExtractPlayerIDs(ctx, input)
	}
	return runPhase2DownloadLandings(ctx, input)
}

// runPhase1ExtractPlayerIDs extracts all unique player IDs from boxscores by calling
// ImportNHLTeamsAndPlayersWorkflow as a child workflow.
func runPhase1ExtractPlayerIDs(ctx workflow.Context, input *downloadPlayersInternalInput) error {
	logger := workflow.GetLogger(ctx)
	startedAt := workflow.Now(ctx)

	logger.Info("DownloadPlayersWorkflow Phase 1: extracting player IDs via child workflow",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason)

	// Set up phase-based progress tracker
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseExtractPlayerIDs, Description: "Extracting player IDs..."},
		{ID: phaseDownloadPlayerLandings, Description: "Downloading players..."},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}
	tracker.MarkItemStarted(ctx, phaseExtractPlayerIDs)

	// Execute ImportNHLTeamsAndPlayersWorkflow as a child workflow
	// This extracts both teams and player IDs, upserts teams, and returns player IDs
	childInput := &model.DownloadSeasonsInput{
		StartSeason:       input.StartSeason,
		EndSeason:         input.EndSeason,
		SeasonConcurrency: input.SeasonConcurrency,
	}

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: WorkflowIDImportNHLTeamsAndPlayers,
	})

	var result *ImportTeamsAndPlayersResult
	if err := workflow.ExecuteChildWorkflow(childCtx, ImportNHLTeamsAndPlayersWorkflow, childInput).Get(ctx, &result); err != nil {
		return err
	}

	// Mark Phase 1 complete
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	phase1CompletedDesc := fmt.Sprintf("Extracted %d player IDs in %s.", len(result.PlayerIDs), elapsed)
	tracker.SetItemCompletedDescription(phaseExtractPlayerIDs, phase1CompletedDesc)
	tracker.MarkItemCompleted(ctx, phaseExtractPlayerIDs)

	logger.Info("Phase 1 complete, transitioning to Phase 2",
		"total_unique_players", len(result.PlayerIDs))

	// ContinueAsNew into Phase 2, preserving Phase 1 completion info
	return workflow.NewContinueAsNewError(ctx, DownloadPlayersWorkflowContinue,
		&downloadPlayersInternalInput{
			PlayerIDs:           result.PlayerIDs,
			StartIndex:          0,
			TotalCompleted:      0,
			Phase:               phaseDownloadPlayerLandings,
			StartedAt:           workflow.Now(ctx),
			Phase1CompletedDesc: phase1CompletedDesc,
		})
}

// runPhase2DownloadLandings downloads player landing pages with bounded concurrency.
func runPhase2DownloadLandings(ctx workflow.Context, input *downloadPlayersInternalInput) error {
	logger := workflow.GetLogger(ctx)
	concurrency := viper.GetInt(config.FlagPlayerLandingConcurrency)
	if concurrency <= 0 {
		concurrency = config.DefaultPlayerLandingConcurrency
	}

	batchSize := viper.GetInt(config.FlagPlayerLandingBatchSize)
	if batchSize <= 0 {
		batchSize = config.DefaultPlayerLandingBatchSize
	}

	playersPerExec := viper.GetInt(config.FlagPlayerLandingPlayersPerExec)
	if playersPerExec <= 0 {
		playersPerExec = config.DefaultPlayerLandingPlayersPerExec
	}

	totalPlayers := len(input.PlayerIDs)
	startIdx := input.StartIndex
	endIdx := startIdx + playersPerExec
	if endIdx > totalPlayers {
		endIdx = totalPlayers
	}

	// Track start time for elapsed calculation
	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = workflow.Now(ctx)
	}

	playersThisExec := endIdx - startIdx
	numBatches := (playersThisExec + batchSize - 1) / batchSize

	logger.Info("DownloadPlayersWorkflow Phase 2: downloading player landings",
		"total_players", totalPlayers,
		"start_index", startIdx,
		"end_index", endIdx,
		"players_this_exec", playersThisExec,
		"num_batches", numBatches,
		"concurrency", concurrency,
		"batch_size", batchSize)

	// Set up phase-based progress tracker with Phase 1 already completed
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseExtractPlayerIDs, Description: "Extracting player IDs...", CompletedDescription: input.Phase1CompletedDesc, Total: 1},
		{ID: phaseDownloadPlayerLandings, Description: "Downloading players...", Total: totalPlayers},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	// Mark Phase 1 as already completed, Phase 2 as started with current progress
	tracker.MarkItemCompleted(ctx, phaseExtractPlayerIDs)
	tracker.MarkItemStarted(ctx, phaseDownloadPlayerLandings)
	tracker.IncrementItemBy(phaseDownloadPlayerLandings, input.TotalCompleted)

	// Activity options with longer timeout for API calls
	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryMaxInterval)) * time.Second,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
			MaximumAttempts:    int32(viper.GetInt(config.FlagTemporalRetryMaxAttempts)),
		},
	})

	// Run worker pool - increment by batchSize per completion
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStart := startIdx + (batchIndex * batchSize)
		batchEnd := batchStart + batchSize
		if batchEnd > endIdx {
			batchEnd = endIdx
		}
		batch := input.PlayerIDs[batchStart:batchEnd]
		return workflow.ExecuteActivity(activityCtx, DownloadPlayerLandingBatchActivity, batch)
	}

	if err := tracker.RunWorkerPoolForItemBy(ctx, numBatches, concurrency, phaseDownloadPlayerLandings, batchSize, startActivity, nil); err != nil {
		return err
	}

	// ContinueAsNew if more players remain
	if endIdx < totalPlayers {
		logger.Info("Continuing to next execution",
			"completed_so_far", input.TotalCompleted+playersThisExec,
			"remaining", totalPlayers-endIdx)

		return workflow.NewContinueAsNewError(ctx, DownloadPlayersWorkflowContinue,
			&downloadPlayersInternalInput{
				PlayerIDs:           input.PlayerIDs,
				StartIndex:          endIdx,
				TotalCompleted:      input.TotalCompleted + playersThisExec,
				Phase:               phaseDownloadPlayerLandings,
				StartedAt:           startedAt,
				Phase1CompletedDesc: input.Phase1CompletedDesc,
			})
	}

	// Mark phase complete with final count and elapsed time
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.SetItemCompletedDescription(phaseDownloadPlayerLandings, fmt.Sprintf("Downloaded %d players in %s.", totalPlayers, elapsed))
	tracker.MarkItemCompleted(ctx, phaseDownloadPlayerLandings)

	logger.Info("DownloadPlayersWorkflow completed",
		"total_players", totalPlayers)

	return nil
}

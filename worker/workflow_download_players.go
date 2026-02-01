package worker

import (
	"sort"
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

// runPhase1ExtractPlayerIDs extracts all unique player IDs from boxscores.
func runPhase1ExtractPlayerIDs(ctx workflow.Context, input *downloadPlayersInternalInput) error {
	logger := workflow.GetLogger(ctx)
	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = config.DefaultMaxSeasonConcurrency
	}

	concurrency := config.DefaultSeasonConcurrency
	if input.SeasonConcurrency != nil && *input.SeasonConcurrency > 0 {
		concurrency = *input.SeasonConcurrency
	}
	if concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	logger.Info("DownloadPlayersWorkflow Phase 1: extracting player IDs",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Fetch seasons
	modelInput := &model.DownloadSeasonsInput{
		StartSeason:       input.StartSeason,
		EndSeason:         input.EndSeason,
		SeasonConcurrency: input.SeasonConcurrency,
	}
	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, modelInput).Get(ctx, &seasons); err != nil {
		return err
	}

	tracker := NewProgressTracker(len(seasons))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	// Extract player IDs from all seasons
	allPlayerIDs, err := extractPlayerIDsWithConcurrency(ctx, tracker, seasons, concurrency)
	if err != nil {
		return err
	}

	// Convert map to sorted slice for determinism
	playerIDSlice := make([]int64, 0, len(allPlayerIDs))
	for id := range allPlayerIDs {
		playerIDSlice = append(playerIDSlice, id)
	}
	sort.Slice(playerIDSlice, func(i, j int) bool { return playerIDSlice[i] < playerIDSlice[j] })

	logger.Info("Phase 1 complete, transitioning to Phase 2",
		"seasons_processed", len(seasons),
		"total_unique_players", len(playerIDSlice))

	// ContinueAsNew into Phase 2
	return workflow.NewContinueAsNewError(ctx, DownloadPlayersWorkflowContinue,
		&downloadPlayersInternalInput{
			PlayerIDs:      playerIDSlice,
			StartIndex:     0,
			TotalCompleted: 0,
			Phase:          phaseDownloadPlayerLandings,
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

	// Progress tracker with offset for cumulative tracking
	tracker := NewProgressTrackerWithOffset(numBatches, input.TotalCompleted, totalPlayers)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

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

	// Run worker pool
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStart := startIdx + (batchIndex * batchSize)
		batchEnd := batchStart + batchSize
		if batchEnd > endIdx {
			batchEnd = endIdx
		}
		batch := input.PlayerIDs[batchStart:batchEnd]
		return workflow.ExecuteActivity(activityCtx, DownloadPlayerLandingBatchActivity, batch)
	}

	if err := tracker.RunWorkerPool(ctx, numBatches, concurrency, startActivity); err != nil {
		return err
	}

	// ContinueAsNew if more players remain
	if endIdx < totalPlayers {
		logger.Info("Continuing to next execution",
			"completed_so_far", input.TotalCompleted+playersThisExec,
			"remaining", totalPlayers-endIdx)

		return workflow.NewContinueAsNewError(ctx, DownloadPlayersWorkflowContinue,
			&downloadPlayersInternalInput{
				PlayerIDs:      input.PlayerIDs,
				StartIndex:     endIdx,
				TotalCompleted: input.TotalCompleted + playersThisExec,
				Phase:          phaseDownloadPlayerLandings,
			})
	}

	logger.Info("DownloadPlayersWorkflow completed",
		"total_players", totalPlayers)

	return nil
}

// extractPlayerIDsWithConcurrency processes seasons with bounded concurrency,
// collecting player IDs from each and merging into a single set.
func extractPlayerIDsWithConcurrency(
	ctx workflow.Context,
	tracker *ProgressTracker,
	seasons []SeasonInfo,
	concurrency int,
) (map[int64]struct{}, error) {
	if len(seasons) == 0 {
		return make(map[int64]struct{}), nil
	}

	logger := workflow.GetLogger(ctx)
	allPlayerIDs := make(map[int64]struct{})

	// Track active futures
	type activeWork struct {
		season SeasonInfo
		future workflow.Future
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	// Start initial batch
	for i := 0; i < concurrency && nextIdx < len(seasons); i++ {
		season := seasons[nextIdx]
		future := workflow.ExecuteActivity(ctx, ExtractPlayerIDsForSeasonActivity, season)
		active[nextIdx] = &activeWork{season: season, future: future}
		nextIdx++
	}

	var firstErr error

	// Process until all work is done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		for idx, work := range active {
			capturedIdx := idx
			capturedWork := work
			selector.AddFuture(capturedWork.future, func(f workflow.Future) {
				var playerIDs []int64
				if err := f.Get(ctx, &playerIDs); err != nil && firstErr == nil {
					firstErr = err
					return
				}

				// Merge player IDs into the combined set
				for _, id := range playerIDs {
					allPlayerIDs[id] = struct{}{}
				}

				logger.Info("Season extraction complete",
					"startYear", capturedWork.season.StartYear,
					"players_found", len(playerIDs),
					"total_unique", len(allPlayerIDs))

				tracker.Increment()
				delete(active, capturedIdx)

				// Start next season if available
				if nextIdx < len(seasons) {
					season := seasons[nextIdx]
					future := workflow.ExecuteActivity(ctx, ExtractPlayerIDsForSeasonActivity, season)
					active[nextIdx] = &activeWork{season: season, future: future}
					nextIdx++
				}
			})
		}

		selector.Select(ctx)

		if firstErr != nil {
			return nil, firstErr
		}
	}

	return allPlayerIDs, nil
}

package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDProcessPlayers = "process-players"

// Phase IDs for process players workflow
const (
	phaseProcessExtractIDs    = 1
	phaseProcessLoadYahoo     = 2
	phaseProcessPlayers       = 3
	phaseProcessVerifyUnmatch = 4
)

// Default batch sizes for player processing
const (
	DefaultProcessPlayersBatchSize   = 50
	DefaultProcessPlayersConcurrency = 10
)

// ProcessPlayersInput combines download and import configuration.
type ProcessPlayersInput struct {
	// Season range for player extraction
	StartSeason       *int
	EndSeason         *int
	SeasonConcurrency *int

	// Processing config
	BatchSize   *int // Players per batch activity (default: 50)
	Concurrency *int // Parallel activities (default: 10)
}

// processPlayersInternalInput supports ContinueAsNew between phases.
type processPlayersInternalInput struct {
	// Original input params
	StartSeason       *int
	EndSeason         *int
	SeasonConcurrency *int
	BatchSize         int
	Concurrency       int

	// Phase 1 result
	Players []BoxscorePlayer

	// Phase 2 result (Yahoo pool metadata)
	YahooPoolResult *SaveYahooIDPoolResult

	// Processing state
	StartIndex     int
	TotalCompleted int
	Phase          int
	StartedAt      time.Time

	// Aggregated results across ContinueAsNew
	TotalDownloaded int
	TotalCacheHits  int
	TotalMissing    int
	TotalImported   int
	TotalMatched    int
	AllErrors       []string

	// Progress state preserved across ContinueAsNew
	Phase1CompletedDesc string
	Phase2CompletedDesc string
}

// ProcessPlayersResult contains the final result of the unified workflow.
type ProcessPlayersResult struct {
	// Player counts
	TotalPlayers    int
	ImportedPlayers int
	MatchedWithYahoo int

	// Download stats
	Downloaded int
	CacheHits  int
	Missing    int

	// Yahoo stats
	TotalYahooPlayers     int
	SkippedNonNHL         int
	VerifiedNonNHLThisRun int
	TrulyUnmatched        []VerifiedPlayer

	// Errors
	Errors []string
}

// ProcessPlayersWorkflow extracts player IDs, downloads landing pages, and imports to database.
// This combines DownloadPlayersWorkflow and ImportPlayersWorkflow into a single pass.
func ProcessPlayersWorkflow(ctx workflow.Context, input *ProcessPlayersInput) (*ProcessPlayersResult, error) {
	// Parse configuration with defaults
	batchSize := DefaultProcessPlayersBatchSize
	if input != nil && input.BatchSize != nil && *input.BatchSize > 0 {
		batchSize = *input.BatchSize
	}
	concurrency := DefaultProcessPlayersConcurrency
	if input != nil && input.Concurrency != nil && *input.Concurrency > 0 {
		concurrency = *input.Concurrency
	}

	internalInput := &processPlayersInternalInput{
		StartSeason:       nil,
		EndSeason:         nil,
		SeasonConcurrency: nil,
		BatchSize:         batchSize,
		Concurrency:       concurrency,
		Phase:             phaseProcessExtractIDs,
	}
	if input != nil {
		internalInput.StartSeason = input.StartSeason
		internalInput.EndSeason = input.EndSeason
		internalInput.SeasonConcurrency = input.SeasonConcurrency
	}

	return processPlayersWorkflowImpl(ctx, internalInput)
}

// ProcessPlayersWorkflowContinue is the entry point for ContinueAsNew.
func ProcessPlayersWorkflowContinue(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	return processPlayersWorkflowImpl(ctx, input)
}

func processPlayersWorkflowImpl(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	switch input.Phase {
	case phaseProcessExtractIDs:
		return runProcessPhase1ExtractIDs(ctx, input)
	case phaseProcessLoadYahoo:
		return runProcessPhase2LoadYahoo(ctx, input)
	case phaseProcessPlayers:
		return runProcessPhase3ProcessPlayers(ctx, input)
	case phaseProcessVerifyUnmatch:
		return runProcessPhase4VerifyUnmatched(ctx, input)
	default:
		return nil, fmt.Errorf("unknown phase: %d", input.Phase)
	}
}

// runProcessPhase1ExtractIDs extracts player IDs from boxscores via child workflow.
func runProcessPhase1ExtractIDs(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	startedAt := workflow.Now(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 1: extracting player IDs",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason)

	// Set up progress tracker
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseProcessExtractIDs, Description: "Extracting player IDs..."},
		{ID: phaseProcessLoadYahoo, Description: "Loading Yahoo player pool..."},
		{ID: phaseProcessPlayers, Description: "Processing players..."},
		{ID: phaseProcessVerifyUnmatch, Description: "Verifying unmatched players..."},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	tracker.MarkItemStarted(ctx, phaseProcessExtractIDs)

	// Execute child workflow to extract players
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
		return nil, err
	}

	// Mark Phase 1 complete
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	phase1CompletedDesc := fmt.Sprintf("Extracted %d player IDs in %s.", len(result.Players), elapsed)
	tracker.SetItemCompletedDescription(phaseProcessExtractIDs, phase1CompletedDesc)
	tracker.MarkItemCompleted(ctx, phaseProcessExtractIDs)

	logger.Info("Phase 1 complete, transitioning to Phase 2",
		"total_unique_players", len(result.Players))

	// ContinueAsNew into Phase 2
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
		&processPlayersInternalInput{
			StartSeason:         input.StartSeason,
			EndSeason:           input.EndSeason,
			SeasonConcurrency:   input.SeasonConcurrency,
			BatchSize:           input.BatchSize,
			Concurrency:         input.Concurrency,
			Players:             result.Players,
			Phase:               phaseProcessLoadYahoo,
			StartedAt:           workflow.Now(ctx),
			Phase1CompletedDesc: phase1CompletedDesc,
		})
}

// runProcessPhase2LoadYahoo loads Yahoo player pool into Redis.
func runProcessPhase2LoadYahoo(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	startedAt := workflow.Now(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 2: loading Yahoo player pool")

	// Set up progress tracker with Phase 1 already completed
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseProcessExtractIDs, Description: "Extracting player IDs...", CompletedDescription: input.Phase1CompletedDesc, Total: 1},
		{ID: phaseProcessLoadYahoo, Description: "Loading Yahoo player pool..."},
		{ID: phaseProcessPlayers, Description: "Processing players...", Total: len(input.Players)},
		{ID: phaseProcessVerifyUnmatch, Description: "Verifying unmatched players..."},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	tracker.MarkItemCompleted(ctx, phaseProcessExtractIDs)
	tracker.MarkItemStarted(ctx, phaseProcessLoadYahoo)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumInterval:    time.Minute,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Step 2a: List all Yahoo player files
	var yahooPlayerIDs []int
	if err := workflow.ExecuteActivity(ctx, ListYahooPlayerFilesActivity).Get(ctx, &yahooPlayerIDs); err != nil {
		logger.Error("Failed to list Yahoo player files", "error", err)
		return nil, err
	}
	logger.Info("Listed Yahoo player files", "count", len(yahooPlayerIDs))

	// Step 2b: Parse Yahoo players in batches
	var allYahooPlayers []cache.YahooPlayer
	if err := runYahooParseBatches(ctx, yahooPlayerIDs, input.BatchSize, input.Concurrency, &allYahooPlayers); err != nil {
		logger.Error("Failed to parse Yahoo players", "error", err)
		return nil, err
	}
	logger.Info("Parsed Yahoo players", "count", len(allYahooPlayers))

	// Step 2c: Save all players to Redis
	var saveResult *SaveYahooIDPoolResult
	if err := workflow.ExecuteActivity(ctx, SaveYahooPlayersToRedisActivity, allYahooPlayers).Get(ctx, &saveResult); err != nil {
		logger.Error("Failed to save Yahoo players to Redis", "error", err)
		return nil, err
	}
	logger.Info("Saved Yahoo pool to Redis",
		"total", saveResult.TotalPlayers,
		"available", saveResult.AvailablePlayers,
		"skipped_non_nhl", saveResult.SkippedNonNHL)

	// Mark Phase 2 complete
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	phase2CompletedDesc := fmt.Sprintf("Loaded %d Yahoo players in %s.", saveResult.TotalPlayers, elapsed)
	tracker.SetItemCompletedDescription(phaseProcessLoadYahoo, phase2CompletedDesc)
	tracker.MarkItemCompleted(ctx, phaseProcessLoadYahoo)

	logger.Info("Phase 2 complete, transitioning to Phase 3")

	// ContinueAsNew into Phase 3
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
		&processPlayersInternalInput{
			StartSeason:         input.StartSeason,
			EndSeason:           input.EndSeason,
			SeasonConcurrency:   input.SeasonConcurrency,
			BatchSize:           input.BatchSize,
			Concurrency:         input.Concurrency,
			Players:             input.Players,
			YahooPoolResult:     saveResult,
			StartIndex:          0,
			TotalCompleted:      0,
			Phase:               phaseProcessPlayers,
			StartedAt:           workflow.Now(ctx),
			Phase1CompletedDesc: input.Phase1CompletedDesc,
			Phase2CompletedDesc: phase2CompletedDesc,
		})
}

// runProcessPhase3ProcessPlayers downloads and imports players in batches.
func runProcessPhase3ProcessPlayers(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	playersPerExec := viper.GetInt(config.FlagPlayerLandingPlayersPerExec)
	if playersPerExec <= 0 {
		playersPerExec = config.DefaultPlayerLandingPlayersPerExec
	}

	totalPlayers := len(input.Players)
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
	numBatches := (playersThisExec + input.BatchSize - 1) / input.BatchSize

	logger.Info("ProcessPlayersWorkflow Phase 3: processing players",
		"total_players", totalPlayers,
		"start_index", startIdx,
		"end_index", endIdx,
		"players_this_exec", playersThisExec,
		"num_batches", numBatches,
		"concurrency", input.Concurrency,
		"batch_size", input.BatchSize)

	// Set up progress tracker with Phases 1 and 2 already completed
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseProcessExtractIDs, Description: "Extracting player IDs...", CompletedDescription: input.Phase1CompletedDesc, Total: 1},
		{ID: phaseProcessLoadYahoo, Description: "Loading Yahoo player pool...", CompletedDescription: input.Phase2CompletedDesc, Total: 1},
		{ID: phaseProcessPlayers, Description: "Processing players...", Total: totalPlayers},
		{ID: phaseProcessVerifyUnmatch, Description: "Verifying unmatched players..."},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	tracker.MarkItemCompleted(ctx, phaseProcessExtractIDs)
	tracker.MarkItemCompleted(ctx, phaseProcessLoadYahoo)
	tracker.MarkItemStarted(ctx, phaseProcessPlayers)
	tracker.IncrementItemBy(phaseProcessPlayers, input.TotalCompleted)

	// Activity options
	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryMaxInterval)) * time.Second,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
			MaximumAttempts:    int32(viper.GetInt(config.FlagTemporalRetryMaxAttempts)),
		},
	})

	// Aggregate results
	totalDownloaded := input.TotalDownloaded
	totalCacheHits := input.TotalCacheHits
	totalMissing := input.TotalMissing
	totalImported := input.TotalImported
	totalMatched := input.TotalMatched
	allErrors := input.AllErrors
	if allErrors == nil {
		allErrors = []string{}
	}

	// Run batches with concurrency control using worker pool
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStart := startIdx + (batchIndex * input.BatchSize)
		batchEnd := batchStart + input.BatchSize
		if batchEnd > endIdx {
			batchEnd = endIdx
		}
		batch := input.Players[batchStart:batchEnd]
		return workflow.ExecuteActivity(activityCtx, ProcessPlayerBatchActivity, batch)
	}

	// Collect results
	var results []ProcessPlayerBatchResult
	err := runProcessBatches(ctx, tracker, phaseProcessPlayers, numBatches, input.Concurrency, input.BatchSize, playersThisExec, startActivity, &results)
	if err != nil {
		return nil, err
	}

	// Aggregate batch results
	for _, r := range results {
		totalDownloaded += r.Downloaded
		totalCacheHits += r.CacheHits
		totalMissing += r.Missing
		totalImported += r.Imported
		totalMatched += r.Matched
		allErrors = append(allErrors, r.Errors...)
	}

	// ContinueAsNew if more players remain
	if endIdx < totalPlayers {
		logger.Info("Continuing to next execution",
			"completed_so_far", input.TotalCompleted+playersThisExec,
			"remaining", totalPlayers-endIdx)

		return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
			&processPlayersInternalInput{
				StartSeason:         input.StartSeason,
				EndSeason:           input.EndSeason,
				SeasonConcurrency:   input.SeasonConcurrency,
				BatchSize:           input.BatchSize,
				Concurrency:         input.Concurrency,
				Players:             input.Players,
				YahooPoolResult:     input.YahooPoolResult,
				StartIndex:          endIdx,
				TotalCompleted:      input.TotalCompleted + playersThisExec,
				Phase:               phaseProcessPlayers,
				StartedAt:           startedAt,
				TotalDownloaded:     totalDownloaded,
				TotalCacheHits:      totalCacheHits,
				TotalMissing:        totalMissing,
				TotalImported:       totalImported,
				TotalMatched:        totalMatched,
				AllErrors:           allErrors,
				Phase1CompletedDesc: input.Phase1CompletedDesc,
				Phase2CompletedDesc: input.Phase2CompletedDesc,
			})
	}

	// Mark Phase 3 complete
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.SetItemCompletedDescription(phaseProcessPlayers,
		fmt.Sprintf("Processed %d players (%d imported, %d matched) in %s.", totalPlayers, totalImported, totalMatched, elapsed))
	tracker.MarkItemCompleted(ctx, phaseProcessPlayers)

	logger.Info("Phase 3 complete, transitioning to Phase 4",
		"downloaded", totalDownloaded,
		"cache_hits", totalCacheHits,
		"missing", totalMissing,
		"imported", totalImported,
		"matched", totalMatched)

	// ContinueAsNew into Phase 4
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
		&processPlayersInternalInput{
			StartSeason:         input.StartSeason,
			EndSeason:           input.EndSeason,
			SeasonConcurrency:   input.SeasonConcurrency,
			BatchSize:           input.BatchSize,
			Concurrency:         input.Concurrency,
			Players:             input.Players,
			YahooPoolResult:     input.YahooPoolResult,
			Phase:               phaseProcessVerifyUnmatch,
			StartedAt:           workflow.Now(ctx),
			TotalDownloaded:     totalDownloaded,
			TotalCacheHits:      totalCacheHits,
			TotalMissing:        totalMissing,
			TotalImported:       totalImported,
			TotalMatched:        totalMatched,
			AllErrors:           allErrors,
			Phase1CompletedDesc: input.Phase1CompletedDesc,
			Phase2CompletedDesc: input.Phase2CompletedDesc,
		})
}

// runProcessPhase4VerifyUnmatched verifies unmatched Yahoo players.
func runProcessPhase4VerifyUnmatched(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	startedAt := workflow.Now(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 4: verifying unmatched players")

	// Set up progress tracker with Phases 1-3 already completed
	tracker := NewProgressTrackerWithPhases([]PhaseInfo{
		{ID: phaseProcessExtractIDs, Description: "Extracting player IDs...", CompletedDescription: input.Phase1CompletedDesc, Total: 1},
		{ID: phaseProcessLoadYahoo, Description: "Loading Yahoo player pool...", CompletedDescription: input.Phase2CompletedDesc, Total: 1},
		{ID: phaseProcessPlayers, Description: "Processing players...", CompletedDescription: fmt.Sprintf("Processed %d players.", len(input.Players)), Total: 1},
		{ID: phaseProcessVerifyUnmatch, Description: "Verifying unmatched players..."},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	tracker.MarkItemCompleted(ctx, phaseProcessExtractIDs)
	tracker.MarkItemCompleted(ctx, phaseProcessLoadYahoo)
	tracker.MarkItemCompleted(ctx, phaseProcessPlayers)
	tracker.MarkItemStarted(ctx, phaseProcessVerifyUnmatch)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumInterval:    time.Minute,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Load unmatched players
	var unmatchedPlayers []UnmatchedYahooPlayer
	if err := workflow.ExecuteActivity(ctx, LoadUnmatchedYahooPlayersActivity).Get(ctx, &unmatchedPlayers); err != nil {
		logger.Warn("Failed to load unmatched Yahoo players", "error", err)
		unmatchedPlayers = []UnmatchedYahooPlayer{}
	}

	tracker.SetItemTotal(phaseProcessVerifyUnmatch, len(unmatchedPlayers))
	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatchedPlayers))

	// Verify unmatched players in batches
	unmatchedReport := &UnmatchedReport{
		TrulyUnmatched: make([]VerifiedPlayer, 0),
	}

	if len(unmatchedPlayers) > 0 {
		tracker.SetMessage("Verifying unmatched players against NHL API")

		verifyBatchSize := DefaultVerifyBatchSize
		for i := 0; i < len(unmatchedPlayers); i += verifyBatchSize {
			end := i + verifyBatchSize
			if end > len(unmatchedPlayers) {
				end = len(unmatchedPlayers)
			}
			batch := unmatchedPlayers[i:end]

			var batchResult *VerifyUnmatchedResult
			if err := workflow.ExecuteActivity(ctx, VerifyUnmatchedBatchActivity, batch).Get(ctx, &batchResult); err != nil {
				logger.Warn("Failed to verify batch", "error", err, "batch_start", i)
			} else {
				unmatchedReport.TrulyUnmatched = append(unmatchedReport.TrulyUnmatched, batchResult.TrulyUnmatched...)
				unmatchedReport.VerifiedNonNHLCount += len(batchResult.VerifiedNonNHL)
				unmatchedReport.NotFoundCount += len(batchResult.NotFoundInNHL)
			}

			tracker.IncrementItemBy(phaseProcessVerifyUnmatch, len(batch))
		}

		// Log truly unmatched players
		for _, p := range unmatchedReport.TrulyUnmatched {
			logger.Warn("Truly unmatched player with NHL games",
				"yahooID", p.YahooID,
				"yahoo_name", p.FirstName+" "+p.LastName,
				"nhl_name", p.NHLName,
				"nhl_games", p.NHLGames)
		}

		// Cleanup Redis keys
		if err := workflow.ExecuteActivity(ctx, CleanupYahooIDPoolActivity).Get(ctx, nil); err != nil {
			logger.Warn("Failed to cleanup Yahoo ID pool", "error", err)
		}
	}

	// Mark Phase 4 complete
	elapsed := formatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.SetItemCompletedDescription(phaseProcessVerifyUnmatch,
		fmt.Sprintf("Verified %d unmatched (%d truly unmatched) in %s.", len(unmatchedPlayers), len(unmatchedReport.TrulyUnmatched), elapsed))
	tracker.MarkItemCompleted(ctx, phaseProcessVerifyUnmatch)

	logger.Info("ProcessPlayersWorkflow completed",
		"totalPlayers", len(input.Players),
		"downloaded", input.TotalDownloaded,
		"cacheHits", input.TotalCacheHits,
		"missing", input.TotalMissing,
		"imported", input.TotalImported,
		"matchedWithYahoo", input.TotalMatched,
		"trulyUnmatched", len(unmatchedReport.TrulyUnmatched))

	yahooPoolResult := input.YahooPoolResult
	if yahooPoolResult == nil {
		yahooPoolResult = &SaveYahooIDPoolResult{}
	}

	return &ProcessPlayersResult{
		TotalPlayers:          len(input.Players),
		ImportedPlayers:       input.TotalImported,
		MatchedWithYahoo:      input.TotalMatched,
		Downloaded:            input.TotalDownloaded,
		CacheHits:             input.TotalCacheHits,
		Missing:               input.TotalMissing,
		TotalYahooPlayers:     yahooPoolResult.TotalPlayers,
		SkippedNonNHL:         yahooPoolResult.SkippedNonNHL,
		VerifiedNonNHLThisRun: unmatchedReport.VerifiedNonNHLCount,
		TrulyUnmatched:        unmatchedReport.TrulyUnmatched,
		Errors:                input.AllErrors,
	}, nil
}

// runProcessBatches executes process batches with concurrency control.
func runProcessBatches(
	ctx workflow.Context,
	tracker *ProgressTracker,
	phaseID int,
	numBatches, concurrency, batchSize, totalItems int,
	startActivity func(workflow.Context, int) workflow.Future,
	results *[]ProcessPlayerBatchResult,
) error {
	logger := workflow.GetLogger(ctx)

	type activeWork struct {
		index  int
		future workflow.Future
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	// Start initial batches
	for i := 0; i < concurrency && nextIdx < numBatches; i++ {
		future := startActivity(ctx, nextIdx)
		active[nextIdx] = &activeWork{index: nextIdx, future: future}
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
				var result ProcessPlayerBatchResult
				if err := f.Get(ctx, &result); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					logger.Error("Process batch failed", "index", capturedIdx, "error", err)
					delete(active, capturedIdx)
					return
				}

				*results = append(*results, result)
				// Calculate actual batch size (last batch may be smaller)
				batchStart := capturedIdx * batchSize
				actualBatchSize := batchSize
				if batchStart+batchSize > totalItems {
					actualBatchSize = totalItems - batchStart
				}
				tracker.IncrementItemBy(phaseID, actualBatchSize)
				delete(active, capturedIdx)

				// Start next batch if available
				if nextIdx < numBatches {
					future := startActivity(ctx, nextIdx)
					active[nextIdx] = &activeWork{index: nextIdx, future: future}
					nextIdx++
				}
			})
		}

		selector.Select(ctx)

		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

// runYahooParseBatches parses Yahoo player files in concurrent batches.
func runYahooParseBatches(
	ctx workflow.Context,
	playerIDs []int,
	batchSize, concurrency int,
	results *[]cache.YahooPlayer,
) error {
	logger := workflow.GetLogger(ctx)
	numBatches := (len(playerIDs) + batchSize - 1) / batchSize

	type activeWork struct {
		index  int
		future workflow.Future
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	startActivity := func(batchIndex int) workflow.Future {
		batchStart := batchIndex * batchSize
		batchEnd := batchStart + batchSize
		if batchEnd > len(playerIDs) {
			batchEnd = len(playerIDs)
		}
		batch := playerIDs[batchStart:batchEnd]
		return workflow.ExecuteActivity(ctx, ParseYahooPlayerBatchActivity, batch)
	}

	// Start initial batches
	for i := 0; i < concurrency && nextIdx < numBatches; i++ {
		future := startActivity(nextIdx)
		active[nextIdx] = &activeWork{index: nextIdx, future: future}
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
				var batchResult []cache.YahooPlayer
				if err := f.Get(ctx, &batchResult); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					logger.Error("Yahoo parse batch failed", "index", capturedIdx, "error", err)
					delete(active, capturedIdx)
					return
				}

				*results = append(*results, batchResult...)
				delete(active, capturedIdx)

				// Start next batch if available
				if nextIdx < numBatches {
					future := startActivity(nextIdx)
					active[nextIdx] = &activeWork{index: nextIdx, future: future}
					nextIdx++
				}
			})
		}

		selector.Select(ctx)

		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

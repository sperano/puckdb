package worker

import (
	"time"

	"github.com/sperano/puckdb/cache"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDImportPlayers = "import-players"

const (
	// DefaultImportPlayersBatchSize is the number of players per activity.
	DefaultImportPlayersBatchSize = 50
	// DefaultImportPlayersConcurrency is the number of parallel activities.
	DefaultImportPlayersConcurrency = 10
)

// Phase IDs for import workflow
const (
	PhaseLoadYahooPool   = 1
	PhaseImportPlayers   = 2
	PhaseReportUnmatched = 3
)

// ImportPlayersInput contains configuration for the import workflow.
type ImportPlayersInput struct {
	BatchSize   *int // Players per batch activity (default: 50)
	Concurrency *int // Parallel activities (default: 10)
}

// ImportPlayersResult contains the final result of the import workflow.
type ImportPlayersResult struct {
	TotalPlayers     int
	ImportedPlayers  int
	MatchedWithYahoo int
	// Yahoo player stats
	TotalYahooPlayers     int              // Total Yahoo players parsed
	SkippedNonNHL         int              // Excluded at load (verified non-NHL from previous runs)
	VerifiedNonNHLThisRun int              // Verified as non-NHL during this run
	TrulyUnmatched        []VerifiedPlayer // Unmatched players with NHL games (need investigation)
	Errors                []string
}

// ImportPlayersWorkflow imports player data from cached PlayerLanding files into the database.
// It matches NHL players with Yahoo player IDs where possible.
func ImportPlayersWorkflow(ctx workflow.Context, input *ImportPlayersInput) (*ImportPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	// Parse configuration
	batchSize := DefaultImportPlayersBatchSize
	if input != nil && input.BatchSize != nil && *input.BatchSize > 0 {
		batchSize = *input.BatchSize
	}
	concurrency := DefaultImportPlayersConcurrency
	if input != nil && input.Concurrency != nil && *input.Concurrency > 0 {
		concurrency = *input.Concurrency
	}

	logger.Info("ImportPlayersWorkflow started",
		"batchSize", batchSize,
		"concurrency", concurrency)

	// Register progress query handler with phase-based tracking
	// Phase 1 (Load Yahoo pool) is fast and doesn't need progress tracking
	phases := []PhaseInfo{
		{ID: PhaseImportPlayers, Description: "Import players", Total: 0}, // Total set later
		{ID: PhaseReportUnmatched, Description: "Review unmatched players", Total: 0}, // Total set later
	}
	tracker := NewProgressTrackerWithPhases(phases)
	tracker.SetMessage("Starting import")
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumInterval:    time.Minute,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Phase 1: Load Yahoo ID pool into Redis (fan-out pattern)
	// This phase is fast and doesn't need progress tracking
	tracker.SetMessage("Loading Yahoo player pool")
	logger.Info("Phase 1: Loading Yahoo ID pool")

	// Step 1a: List all Yahoo player files
	var yahooPlayerIDs []int
	if err := workflow.ExecuteActivity(ctx, ListYahooPlayerFilesActivity).Get(ctx, &yahooPlayerIDs); err != nil {
		logger.Error("Failed to list Yahoo player files", "error", err)
		return nil, err
	}
	logger.Info("Listed Yahoo player files", "count", len(yahooPlayerIDs))

	// Step 1b: Parse Yahoo players in batches (fan-out)
	var allYahooPlayers []cache.YahooPlayer
	if err := runYahooParseBatches(ctx, yahooPlayerIDs, batchSize, concurrency, &allYahooPlayers); err != nil {
		logger.Error("Failed to parse Yahoo players", "error", err)
		return nil, err
	}
	logger.Info("Parsed Yahoo players", "count", len(allYahooPlayers))

	// Step 1c: Save all players to Redis
	var saveResult *SaveYahooIDPoolResult
	if err := workflow.ExecuteActivity(ctx, SaveYahooPlayersToRedisActivity, allYahooPlayers).Get(ctx, &saveResult); err != nil {
		logger.Error("Failed to save Yahoo players to Redis", "error", err)
		return nil, err
	}
	logger.Info("Saved Yahoo pool to Redis",
		"total", saveResult.TotalPlayers,
		"available", saveResult.AvailablePlayers,
		"skipped_non_nhl", saveResult.SkippedNonNHL)

	// List all PlayerLanding files
	logger.Info("Listing PlayerLanding files")

	var playerIDs []int64
	if err := workflow.ExecuteActivity(ctx, ListPlayerLandingIDsActivity).Get(ctx, &playerIDs); err != nil {
		logger.Error("Failed to list player IDs", "error", err)
		return nil, err
	}
	logger.Info("Found player files", "count", len(playerIDs))

	// Phase 2: Import players in batches
	tracker.MarkItemStarted(ctx, PhaseImportPlayers)
	tracker.SetMessage("Importing players")
	logger.Info("Phase 2: Importing players",
		"total", len(playerIDs),
		"batchSize", batchSize,
		"concurrency", concurrency)

	numBatches := (len(playerIDs) + batchSize - 1) / batchSize
	tracker.SetItemTotal(PhaseImportPlayers, len(playerIDs)) // Track by player count, not batches
	var totalImported, totalMatched int
	var allErrors []string

	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		batchStart := batchIndex * batchSize
		batchEnd := batchStart + batchSize
		if batchEnd > len(playerIDs) {
			batchEnd = len(playerIDs)
		}
		batch := playerIDs[batchStart:batchEnd]
		return workflow.ExecuteActivity(ctx, ImportPlayerBatchActivity, batch)
	}

	// Collect results through the selector
	results := make([]ImportBatchResult, 0, numBatches)
	resultChan := workflow.NewChannel(ctx)

	// Run batches with concurrency control - use main tracker for Phase 2
	err := runImportBatches(ctx, tracker, PhaseImportPlayers, numBatches, concurrency, batchSize, len(playerIDs), startActivity, resultChan, &results)
	if err != nil {
		return nil, err
	}

	// Aggregate results
	for _, r := range results {
		totalImported += r.Imported
		totalMatched += r.Matched
		allErrors = append(allErrors, r.Errors...)
	}

	logger.Info("Import batches complete",
		"imported", totalImported,
		"matched", totalMatched,
		"errors", len(allErrors))
	tracker.MarkItemCompleted(ctx, PhaseImportPlayers)

	// Phase 3: Review unmatched Yahoo players
	tracker.MarkItemStarted(ctx, PhaseReportUnmatched)
	tracker.SetMessage("Loading unmatched Yahoo players")
	logger.Info("Phase 3: Reviewing unmatched Yahoo players")

	// Step 3a: Load unmatched players to get the count
	var unmatchedPlayers []UnmatchedYahooPlayer
	if err := workflow.ExecuteActivity(ctx, LoadUnmatchedYahooPlayersActivity).Get(ctx, &unmatchedPlayers); err != nil {
		logger.Warn("Failed to load unmatched Yahoo players", "error", err)
		unmatchedPlayers = []UnmatchedYahooPlayer{}
	}

	// Set phase total to number of unmatched players
	tracker.SetItemTotal(PhaseReportUnmatched, len(unmatchedPlayers))
	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatchedPlayers))

	// Step 3b: Verify unmatched players in batches against NHL API
	unmatchedReport := &UnmatchedReport{
		TrulyUnmatched: make([]VerifiedPlayer, 0),
	}

	if len(unmatchedPlayers) > 0 {
		tracker.SetMessage("Verifying unmatched players against NHL API")

		// Process in batches for progress tracking
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
				// Continue with next batch
			} else {
				// Aggregate results
				unmatchedReport.TrulyUnmatched = append(unmatchedReport.TrulyUnmatched, batchResult.TrulyUnmatched...)
				unmatchedReport.VerifiedNonNHLCount += len(batchResult.VerifiedNonNHL)
				unmatchedReport.NotFoundCount += len(batchResult.NotFoundInNHL)
			}

			// Update progress after each batch
			tracker.IncrementItemBy(PhaseReportUnmatched, len(batch))
		}

		// Log truly unmatched players (those with NHL games)
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

	tracker.MarkItemCompleted(ctx, PhaseReportUnmatched)

	logger.Info("ImportPlayersWorkflow completed",
		"totalPlayers", len(playerIDs),
		"imported", totalImported,
		"matchedWithYahoo", totalMatched,
		"totalYahooPlayers", saveResult.TotalPlayers,
		"skippedNonNHL", saveResult.SkippedNonNHL,
		"trulyUnmatched", len(unmatchedReport.TrulyUnmatched),
		"verifiedNonNHLThisRun", unmatchedReport.VerifiedNonNHLCount,
		"errors", len(allErrors))

	return &ImportPlayersResult{
		TotalPlayers:          len(playerIDs),
		ImportedPlayers:       totalImported,
		MatchedWithYahoo:      totalMatched,
		TotalYahooPlayers:     saveResult.TotalPlayers,
		SkippedNonNHL:         saveResult.SkippedNonNHL,
		VerifiedNonNHLThisRun: unmatchedReport.VerifiedNonNHLCount,
		TrulyUnmatched:        unmatchedReport.TrulyUnmatched,
		Errors:                allErrors,
	}, nil
}

// runYahooParseBatches executes Yahoo player parsing in batches with concurrency control.
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

// runImportBatches executes import batches with concurrency control.
// Progress is tracked by player count (not batch count) using batchSize and totalItems.
func runImportBatches(
	ctx workflow.Context,
	tracker *ProgressTracker,
	phaseID int,
	numBatches, concurrency, batchSize, totalItems int,
	startActivity func(workflow.Context, int) workflow.Future,
	resultChan workflow.Channel,
	results *[]ImportBatchResult,
) error {
	logger := workflow.GetLogger(ctx)

	type activeWork struct {
		index  int
		future workflow.Future
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	// Start initial batch
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
				var result ImportBatchResult
				if err := f.Get(ctx, &result); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					logger.Error("Import batch failed", "index", capturedIdx, "error", err)
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

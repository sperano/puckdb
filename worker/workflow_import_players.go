package worker

import (
	"time"

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
	PhaseLoadYahooPool  = 1
	PhaseListFiles      = 2
	PhaseImportPlayers  = 3
	PhaseReportUnmatched = 4
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
	UnmatchedYahoo   []UnmatchedYahooPlayer
	Errors           []string
}

// ImportPlayersWorkflow imports player data from cached PlayerLanding files into the database.
// It matches NHL players with Yahoo player IDs where possible.
func ImportPlayersWorkflow(ctx workflow.Context, input *ImportPlayersInput) error {
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
	phases := []PhaseInfo{
		{ID: PhaseLoadYahooPool, Description: "Load Yahoo pool", Total: 1},
		{ID: PhaseListFiles, Description: "List player files", Total: 1},
		{ID: PhaseImportPlayers, Description: "Import players", Total: 0}, // Total set later
		{ID: PhaseReportUnmatched, Description: "Report unmatched", Total: 1},
	}
	tracker := NewProgressTrackerWithPhases(phases)
	tracker.SetMessage("Starting import")
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
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

	// Phase 1: Load Yahoo ID pool into Redis
	tracker.MarkItemStarted(PhaseLoadYahooPool)
	tracker.SetMessage("Loading Yahoo player pool")
	logger.Info("Phase 1: Loading Yahoo ID pool")

	var yahooPoolSize int
	if err := workflow.ExecuteActivity(ctx, LoadYahooIDPoolActivity).Get(ctx, &yahooPoolSize); err != nil {
		logger.Error("Failed to load Yahoo ID pool", "error", err)
		return err
	}
	logger.Info("Loaded Yahoo ID pool", "size", yahooPoolSize)
	tracker.IncrementItem(PhaseLoadYahooPool)
	tracker.MarkItemCompleted(PhaseLoadYahooPool)

	// Phase 2: List all PlayerLanding files
	tracker.MarkItemStarted(PhaseListFiles)
	tracker.SetMessage("Listing player files")
	logger.Info("Phase 2: Listing PlayerLanding files")

	var playerIDs []int64
	if err := workflow.ExecuteActivity(ctx, ListPlayerLandingIDsActivity).Get(ctx, &playerIDs); err != nil {
		logger.Error("Failed to list player IDs", "error", err)
		return err
	}
	logger.Info("Found player files", "count", len(playerIDs))
	tracker.IncrementItem(PhaseListFiles)
	tracker.MarkItemCompleted(PhaseListFiles)

	// Phase 3: Import players in batches
	tracker.MarkItemStarted(PhaseImportPlayers)
	tracker.SetMessage("Importing players")
	logger.Info("Phase 3: Importing players",
		"total", len(playerIDs),
		"batchSize", batchSize,
		"concurrency", concurrency)

	numBatches := (len(playerIDs) + batchSize - 1) / batchSize
	tracker.SetItemTotal(PhaseImportPlayers, numBatches) // Now we know the total
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

	// Run batches with concurrency control - use main tracker for Phase 3
	err := runImportBatches(ctx, tracker, PhaseImportPlayers, numBatches, concurrency, startActivity, resultChan, &results)
	if err != nil {
		return err
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
	tracker.MarkItemCompleted(PhaseImportPlayers)

	// Phase 4: Report unmatched Yahoo IDs
	tracker.MarkItemStarted(PhaseReportUnmatched)
	tracker.SetMessage("Reporting unmatched Yahoo players")
	logger.Info("Phase 4: Reporting unmatched Yahoo IDs")

	var unmatched []UnmatchedYahooPlayer
	if err := workflow.ExecuteActivity(ctx, ReportUnmatchedYahooIDsActivity).Get(ctx, &unmatched); err != nil {
		logger.Warn("Failed to report unmatched Yahoo IDs", "error", err)
		// Don't fail the workflow for this
	}
	tracker.IncrementItem(PhaseReportUnmatched)
	tracker.MarkItemCompleted(PhaseReportUnmatched)

	logger.Info("ImportPlayersWorkflow completed",
		"totalPlayers", len(playerIDs),
		"imported", totalImported,
		"matchedWithYahoo", totalMatched,
		"unmatchedYahoo", len(unmatched),
		"errors", len(allErrors))

	return nil
}

// runImportBatches executes import batches with concurrency control.
func runImportBatches(
	ctx workflow.Context,
	tracker *ProgressTracker,
	phaseID int,
	numBatches, concurrency int,
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
				tracker.IncrementItem(phaseID)
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

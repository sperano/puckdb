package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDProcessPlayers = "process-players"

// Group indices for ProcessPlayers progress tracking.
const (
	GroupLoadYahoo       = 0
	GroupProcessPlayers  = 1
	GroupVerifyUnmatched = 2
)

// Phase IDs for ContinueAsNew dispatch.
const (
	phaseLoadYahoo       = 1
	phaseProcessPlayers  = 2
	phaseVerifyUnmatched = 3
)

// ProcessPlayersInput contains parameters for the process players workflow.
type ProcessPlayersInput struct {
	BatchSize   *int // Players per batch activity
	Concurrency *int // Parallel activities
}

// processPlayersInternalInput supports ContinueAsNew between phases.
type processPlayersInternalInput struct {
	BatchSize   int
	Concurrency int

	// Player data (loaded from Redis at workflow start)
	Players []store.BoxscorePlayer

	// Phase 1 result (Yahoo pool metadata)
	YahooPoolResult *SaveYahooIDPoolResult

	// Phase 2 ContinueAsNew state
	StartIndex     int
	TotalCompleted int
	Phase          int

	// Aggregated results across ContinueAsNew
	TotalDownloaded int
	TotalMissing    int
	TotalImported   int
	TotalMatched    int
	AllErrors       []string
	Origins         core.OriginCounts
}

// ProcessPlayersResult contains the final result of the workflow.
type ProcessPlayersResult struct {
	// Player counts
	TotalPlayers     int
	ImportedPlayers  int
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

// NewProcessPlayersProgressReport creates the initial progress structure.
func NewProcessPlayersProgressReport(totalPlayers int) *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Loading Yahoo player pool...", Bars: []ProgressBar{{Label: "Yahoo", Total: 1}}},
			{Header: "Processing players...", Bars: []ProgressBar{{Label: "Players", Total: totalPlayers}}},
			{Header: "Verifying unmatched players...", Bars: []ProgressBar{{Label: "Unmatched", Total: 1}}},
		},
	}
}

// ProcessPlayersWorkflow loads boxscore players from Redis (extracted by ExtractBoxscorePlayersWorkflow),
// downloads their landing pages, imports to database, and matches with Yahoo players.
func ProcessPlayersWorkflow(ctx workflow.Context, input *ProcessPlayersInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	// Parse configuration
	var batchOverride, concurrencyOverride *int
	if input != nil {
		batchOverride = input.BatchSize
		concurrencyOverride = input.Concurrency
	}
	batchSize := resolveConfigInt(nil, processPlayersBatchSizeParam, batchOverride)
	concurrency := resolveConfigInt(logger, processPlayersConcurrencyParam, concurrencyOverride)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumInterval:    time.Minute,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Load players from Redis (previously extracted by ExtractBoxscorePlayersWorkflow)
	var playerAct *PlayerActivities
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(ctx, playerAct.LoadAllBoxscorePlayers).Get(ctx, &players); err != nil {
		return nil, fmt.Errorf("load boxscore players from redis: %w", err)
	}

	if len(players) == 0 {
		logger.Info("No players to process")
		return &ProcessPlayersResult{}, nil
	}

	logger.Info("ProcessPlayersWorkflow started",
		"totalPlayers", len(players),
		"batchSize", batchSize,
		"concurrency", concurrency)

	// Create and save progress tracker
	tracker := NewReportTracker(NewProcessPlayersProgressReport(len(players)))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	// Run Phase 1 inline (no ContinueAsNew yet — it's fast)
	return runPhaseLoadYahoo(ctx, tracker, &processPlayersInternalInput{
		BatchSize:   batchSize,
		Concurrency: concurrency,
		Players:     players,
		Phase:       phaseLoadYahoo,
		Origins:     core.OriginCounts{},
	})
}

// ProcessPlayersWorkflowContinue is the entry point for ContinueAsNew.
func ProcessPlayersWorkflowContinue(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	// Load tracker from Redis (persisted by previous execution)
	tracker, err := LoadReportTracker(ctx)
	if err != nil {
		return nil, err
	}
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	switch input.Phase {
	case phaseProcessPlayers:
		return runPhaseProcessPlayers(ctx, tracker, input)
	case phaseVerifyUnmatched:
		return runPhaseVerifyUnmatched(ctx, tracker, input)
	default:
		return nil, fmt.Errorf("unknown phase: %d", input.Phase)
	}
}

// runPhaseLoadYahoo loads Yahoo player pool into Redis.
func runPhaseLoadYahoo(ctx workflow.Context, tracker *ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 1: loading Yahoo player pool")
	tracker.StartGroup(ctx, GroupLoadYahoo)

	// Step 1a: List all Yahoo player files
	var yahooPlayerIDs []int
	if err := workflow.ExecuteActivity(ctx, ListYahooPlayerFilesActivity).Get(ctx, &yahooPlayerIDs); err != nil {
		return nil, fmt.Errorf("list yahoo player files: %w", err)
	}
	logger.Info("Listed Yahoo player files", "count", len(yahooPlayerIDs))

	// Step 1b: Parse Yahoo players in batches with concurrency
	var allYahooPlayers []store.YahooPlayer
	numBatches := batchCount(len(yahooPlayerIDs), input.BatchSize)

	if err := tracker.RunWorkerPool(ctx, GroupLoadYahoo, 0, numBatches, input.Concurrency,
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := batchSlice(yahooPlayerIDs, batchIndex, input.BatchSize)
			return workflow.ExecuteActivity(ctx, ParseYahooPlayerBatchActivity, batch)
		},
		func(ctx workflow.Context, _ int, f workflow.Future) error {
			var batchResult []store.YahooPlayer
			if err := f.Get(ctx, &batchResult); err != nil {
				return err
			}
			allYahooPlayers = append(allYahooPlayers, batchResult...)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("parse yahoo players: %w", err)
	}
	logger.Info("Parsed Yahoo players", "count", len(allYahooPlayers))

	// Step 1c: Save all players to Redis
	var saveResult *SaveYahooIDPoolResult
	if err := workflow.ExecuteActivity(ctx, SaveYahooPlayersToRedisActivity, allYahooPlayers).Get(ctx, &saveResult); err != nil {
		return nil, fmt.Errorf("save yahoo players to redis: %w", err)
	}
	logger.Info("Saved Yahoo pool to Redis",
		"total", saveResult.TotalPlayers,
		"available", saveResult.AvailablePlayers,
		"skipped_non_nhl", saveResult.SkippedNonNHL)

	tracker.CompleteGroup(ctx, GroupLoadYahoo,
		fmt.Sprintf("Loaded %d Yahoo players in %s.",
			saveResult.TotalPlayers, tracker.GetElapsed(ctx, GroupLoadYahoo)))

	// Start next group before ContinueAsNew so the spinner has an in-progress
	// line to attach to (avoids "⠋ ✓ completed msg" rendering).
	tracker.StartGroup(ctx, GroupProcessPlayers)

	// ContinueAsNew into Phase 2
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
		&processPlayersInternalInput{
			BatchSize:       input.BatchSize,
			Concurrency:     input.Concurrency,
			Players:         input.Players,
			YahooPoolResult: saveResult,
			Phase:           phaseProcessPlayers,
			Origins:         core.OriginCounts{},
		})
}

// runPhaseProcessPlayers downloads and imports players in batches.
func runPhaseProcessPlayers(ctx workflow.Context, tracker *ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
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

	playersThisExec := endIdx - startIdx
	numBatches := batchCount(playersThisExec, input.BatchSize)

	logger.Info("ProcessPlayersWorkflow Phase 2: processing players",
		"total_players", totalPlayers,
		"start_index", startIdx,
		"end_index", endIdx,
		"players_this_exec", playersThisExec,
		"num_batches", numBatches,
		"concurrency", input.Concurrency,
		"batch_size", input.BatchSize)

	// Activity options for player processing
	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(viper.GetInt(config.FlagTemporalRetryMaxInterval)) * time.Second,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
			MaximumAttempts:    int32(viper.GetInt(config.FlagTemporalRetryMaxAttempts)),
		},
	})

	// Aggregate results from previous ContinueAsNew executions
	totalDownloaded := input.TotalDownloaded
	totalMissing := input.TotalMissing
	totalImported := input.TotalImported
	totalMatched := input.TotalMatched
	allErrors := input.AllErrors
	if allErrors == nil {
		allErrors = []string{}
	}
	origins := input.Origins
	if origins == nil {
		origins = core.OriginCounts{}
	}

	// Run batches with concurrency control
	window := input.Players[startIdx:endIdx]
	if err := tracker.RunWorkerPoolBy(ctx, GroupProcessPlayers, 0, numBatches, input.Concurrency, input.BatchSize,
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := batchSlice(window, batchIndex, input.BatchSize)
			return workflow.ExecuteActivity(activityCtx, ProcessPlayerBatchActivity, batch)
		},
		func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
			var result ProcessPlayerBatchResult
			if err := f.Get(ctx, &result); err != nil {
				return err
			}
			totalDownloaded += result.Downloaded
			totalMissing += result.Missing
			totalImported += result.Imported
			totalMatched += result.Matched
			allErrors = append(allErrors, result.Errors...)
			origins.Add(result.Origins)

			// Correct the increment: RunWorkerPoolBy adds batchSize, but last batch may be smaller
			actualBatchSize := len(batchSlice(window, batchIndex, input.BatchSize))
			if actualBatchSize < input.BatchSize {
				tracker.IncrementBarBy(ctx, GroupProcessPlayers, 0, actualBatchSize-input.BatchSize)
			}
			return nil
		}); err != nil {
		return nil, err
	}

	// ContinueAsNew if more players remain
	if endIdx < totalPlayers {
		logger.Info("Continuing to next execution",
			"completed_so_far", input.TotalCompleted+playersThisExec,
			"remaining", totalPlayers-endIdx)

		return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
			&processPlayersInternalInput{
				BatchSize:       input.BatchSize,
				Concurrency:     input.Concurrency,
				Players:         input.Players,
				YahooPoolResult: input.YahooPoolResult,
				StartIndex:      endIdx,
				TotalCompleted:  input.TotalCompleted + playersThisExec,
				Phase:           phaseProcessPlayers,
				TotalDownloaded: totalDownloaded,
				TotalMissing:    totalMissing,
				TotalImported:   totalImported,
				TotalMatched:    totalMatched,
				AllErrors:       allErrors,
				Origins:         origins,
			})
	}

	// Phase complete
	cacheHits := origins[core.OriginRedis] + origins[core.OriginFileSystem]
	msg := fmt.Sprintf("Processed %d players (%d imported, %d matched) in %s.",
		totalPlayers, totalImported, totalMatched, tracker.GetElapsed(ctx, GroupProcessPlayers))
	msg = origins.AppendSummary(msg, "landing origins")
	tracker.CompleteGroup(ctx, GroupProcessPlayers, msg)

	// Start next group before ContinueAsNew so the spinner has an in-progress
	// line to attach to (avoids "⠋ ✓ completed msg" rendering).
	tracker.StartGroup(ctx, GroupVerifyUnmatched)

	logger.Info("Phase 2 complete",
		"downloaded", totalDownloaded,
		"cache_hits", cacheHits,
		"missing", totalMissing,
		"imported", totalImported,
		"matched", totalMatched)

	// ContinueAsNew into Phase 3
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinue,
		&processPlayersInternalInput{
			BatchSize:       input.BatchSize,
			Concurrency:     input.Concurrency,
			Players:         input.Players,
			YahooPoolResult: input.YahooPoolResult,
			Phase:           phaseVerifyUnmatched,
			TotalDownloaded: totalDownloaded,
			TotalMissing:    totalMissing,
			TotalImported:   totalImported,
			TotalMatched:    totalMatched,
			AllErrors:       allErrors,
			Origins:         origins,
		})
}

// runPhaseVerifyUnmatched verifies unmatched Yahoo players.
func runPhaseVerifyUnmatched(ctx workflow.Context, tracker *ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 3: verifying unmatched players")

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

	tracker.SetBarTotal(GroupVerifyUnmatched, 0, len(unmatchedPlayers))
	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatchedPlayers))

	// Verify unmatched players in batches
	unmatchedReport := &UnmatchedReport{
		TrulyUnmatched: make([]VerifiedPlayer, 0),
	}

	if len(unmatchedPlayers) > 0 {
		for i := 0; i < len(unmatchedPlayers); i += DefaultVerifyBatchSize {
			end := i + DefaultVerifyBatchSize
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

			tracker.IncrementBarBy(ctx, GroupVerifyUnmatched, 0, len(batch))
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

	origins := input.Origins
	if origins == nil {
		origins = core.OriginCounts{}
	}
	cacheHits := origins[core.OriginRedis] + origins[core.OriginFileSystem]

	// Mark Phase 3 complete with comprehensive summary
	tracker.CompleteGroup(ctx, GroupVerifyUnmatched,
		fmt.Sprintf("Imported %d players: %d Yahoo! matched, %d truly unmatched and %d errors in %s.",
			input.TotalImported, input.TotalMatched, len(unmatchedReport.TrulyUnmatched),
			len(input.AllErrors), tracker.GetElapsed(ctx, GroupVerifyUnmatched)))

	logger.Info("ProcessPlayersWorkflow completed",
		"totalPlayers", len(input.Players),
		"downloaded", input.TotalDownloaded,
		"cacheHits", cacheHits,
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
		CacheHits:             cacheHits,
		Missing:               input.TotalMissing,
		TotalYahooPlayers:     yahooPoolResult.TotalPlayers,
		SkippedNonNHL:         yahooPoolResult.SkippedNonNHL,
		VerifiedNonNHLThisRun: unmatchedReport.VerifiedNonNHLCount,
		TrulyUnmatched:        unmatchedReport.TrulyUnmatched,
		Errors:                input.AllErrors,
	}, nil
}

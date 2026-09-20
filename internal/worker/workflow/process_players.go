package workflow

import (
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDProcessPlayers = "process-players"

// Group indices for ProcessPlayers progress tracking.
const (
	GroupLoadYahoo       = 0
	GroupProcessPlayers  = 1
	GroupVerifyUnmatched = 2
)

// processPlayersPhase identifies which phase of ProcessPlayersWorkflow is running.
type processPlayersPhase int

// Phase IDs for ContinueAsNew dispatch. Numeric values are persisted in the
// ContinueAsNew input — DO NOT reorder existing entries or change their values.
// New phases may be appended with a fresh explicit value.
const (
	phaseLoadYahoo processPlayersPhase = iota + 1
	phaseProcessPlayers
	phaseVerifyUnmatched
)

// String returns the phase name for error messages and logging.
func (p processPlayersPhase) String() string {
	switch p {
	case phaseLoadYahoo:
		return "loadYahoo"
	case phaseProcessPlayers:
		return "processPlayers"
	case phaseVerifyUnmatched:
		return "verifyUnmatched"
	default:
		return fmt.Sprintf("unknown(%d)", int(p))
	}
}

// verifyConcurrency is the number of concurrent batches when verifying unmatched players.
// Each batch internally rate-limits NHL API calls, so parallel batches are safe.
const verifyConcurrency = 10

// ProcessPlayersInput contains parameters for the process players workflow.
type ProcessPlayersInput struct {
	BatchSize   *int // Players per batch activity
	Concurrency *int // Parallel activities
}

// processPlayersInternalInput supports ContinueAsNew between phases.
type processPlayersInternalInput struct {
	BatchSize   int
	Concurrency int

	// PlayersPerExec is the number of players Phase 2 processes per
	// ContinueAsNew execution. Snapshotted once by ProcessPlayersWorkflow and
	// carried through every phase transition; ProcessPlayersWorkflowContinue
	// resolves it itself only for inputs produced before this field existed
	// (identified by the zero value, which is otherwise unreachable since
	// shared.PlayerLandingPlayersPerExecParam always resolves to a positive
	// default).
	PlayersPerExec int

	// Player data (loaded from Redis at workflow start)
	Players []store.BoxscorePlayer

	// Phase 1 result (Yahoo pool metadata)
	YahooPoolResult *workplayer.SaveYahooIDPoolResult

	// Phase 2 ContinueAsNew state
	StartIndex     int
	TotalCompleted int
	Phase          processPlayersPhase

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
	TrulyUnmatched        []workplayer.VerifiedPlayer

	// Errors
	Errors []string
}

// NewProcessPlayersProgressReport creates the initial progress structure.
func NewProcessPlayersProgressReport(totalPlayers int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Loading Yahoo player pool...", Bars: []shared.ProgressBar{{Label: "Yahoo", Total: 1}}},
			{Header: "Processing players...", Bars: []shared.ProgressBar{{Label: "Players", Total: totalPlayers}}},
			{Header: "Verifying unmatched players...", Bars: []shared.ProgressBar{{Label: "Unmatched", Total: 1}}},
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
	cfg, err := snapshotProcessPlayersConfig(ctx, logger, batchOverride, concurrencyOverride)
	if err != nil {
		return nil, err
	}
	batchSize := cfg.BatchSize
	concurrency := cfg.Concurrency

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Load players from Redis (previously extracted by ExtractBoxscorePlayersWorkflow)
	var playerAct *workplayer.Activities
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
	tracker, err := shared.InitTracker(ctx, NewProcessPlayersProgressReport(len(players)))
	if err != nil {
		return nil, err
	}

	// Run Phase 1 inline (no ContinueAsNew yet — it's fast)
	return runPhaseLoadYahoo(ctx, tracker, &processPlayersInternalInput{
		BatchSize:      batchSize,
		Concurrency:    concurrency,
		PlayersPerExec: cfg.PlayersPerExec,
		Players:        players,
		Phase:          phaseLoadYahoo,
		Origins:        core.OriginCounts{},
	})
}

// ProcessPlayersWorkflowContinue is the entry point for ContinueAsNew.
func ProcessPlayersWorkflowContinue(ctx workflow.Context, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	// Load tracker from Redis (persisted by previous execution)
	tracker, err := shared.LoadReportTracker(ctx)
	if err != nil {
		return nil, err
	}
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	// PlayersPerExec is 0 only for inputs produced before this field existed;
	// ProcessPlayersWorkflow always snapshots a positive value for new runs.
	if input.PlayersPerExec == 0 {
		playersPerExec, err := shared.SnapshotConfigInt(ctx, nil, shared.PlayerLandingPlayersPerExecParam, nil)
		if err != nil {
			return nil, err
		}
		input.PlayersPerExec = playersPerExec
	}

	switch input.Phase {
	case phaseProcessPlayers:
		return runPhaseProcessPlayers(ctx, tracker, input)
	case phaseVerifyUnmatched:
		return runPhaseVerifyUnmatched(ctx, tracker, input)
	default:
		return nil, fmt.Errorf("unknown phase: %s", input.Phase)
	}
}

// runPhaseLoadYahoo loads Yahoo player pool into Redis.
func runPhaseLoadYahoo(ctx workflow.Context, tracker *shared.ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 1: loading Yahoo player pool")
	tracker.StartGroup(ctx, GroupLoadYahoo)

	// Step 1a: List all Yahoo player files
	var playerAct *workplayer.Activities
	var yahooPlayerIDs []int
	if err := workflow.ExecuteActivity(ctx, playerAct.ListYahooPlayerFiles).Get(ctx, &yahooPlayerIDs); err != nil {
		return nil, fmt.Errorf("list yahoo player files: %w", err)
	}
	logger.Info("Listed Yahoo player files", "count", len(yahooPlayerIDs))

	// Step 1b: Parse Yahoo players in batches with concurrency
	var allYahooPlayers []store.YahooPlayer
	numBatches := shared.BatchCount(len(yahooPlayerIDs), input.BatchSize)

	if err := tracker.RunWorkerPool(ctx, GroupLoadYahoo, 0, numBatches, input.Concurrency,
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := shared.BatchSlice(yahooPlayerIDs, batchIndex, input.BatchSize)
			return workflow.ExecuteActivity(ctx, playerAct.ParseYahooPlayerBatch, batch)
		},
		shared.CollectSlicesInto(&allYahooPlayers)); err != nil {
		return nil, fmt.Errorf("parse yahoo players: %w", err)
	}
	logger.Info("Parsed Yahoo players", "count", len(allYahooPlayers))

	// Step 1c: Save all players to Redis
	var saveResult *workplayer.SaveYahooIDPoolResult
	if err := workflow.ExecuteActivity(ctx, playerAct.SaveYahooPlayersToRedis, allYahooPlayers).Get(ctx, &saveResult); err != nil {
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
			PlayersPerExec:  input.PlayersPerExec,
			Players:         input.Players,
			YahooPoolResult: saveResult,
			Phase:           phaseProcessPlayers,
			Origins:         core.OriginCounts{},
		})
}

// runPhaseProcessPlayers downloads and imports players in batches.
func runPhaseProcessPlayers(ctx workflow.Context, tracker *shared.ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	var playerAct *workplayer.Activities

	playersPerExec := input.PlayersPerExec

	totalPlayers := len(input.Players)
	startIdx := input.StartIndex
	endIdx := startIdx + playersPerExec
	if endIdx > totalPlayers {
		endIdx = totalPlayers
	}

	playersThisExec := endIdx - startIdx
	numBatches := shared.BatchCount(playersThisExec, input.BatchSize)

	logger.Info("ProcessPlayersWorkflow Phase 2: processing players",
		"total_players", totalPlayers,
		"start_index", startIdx,
		"end_index", endIdx,
		"players_this_exec", playersThisExec,
		"num_batches", numBatches,
		"concurrency", input.Concurrency,
		"batch_size", input.BatchSize)

	activityCtx := workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

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
	if err := tracker.RunWorkerPoolWithIncrement(ctx, GroupProcessPlayers, 0, numBatches, input.Concurrency,
		func(i int) int { return len(shared.BatchSlice(window, i, input.BatchSize)) },
		func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := shared.BatchSlice(window, batchIndex, input.BatchSize)
			return workflow.ExecuteActivity(activityCtx, playerAct.ProcessPlayerBatch, batch)
		},
		func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
			var result workplayer.ProcessPlayerBatchResult
			if err := f.Get(ctx, &result); err != nil {
				return err
			}
			totalDownloaded += result.Downloaded
			totalMissing += result.Missing
			totalImported += result.Imported
			totalMatched += result.Matched
			allErrors = append(allErrors, result.Errors...)
			origins.Add(result.Origins)
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
				PlayersPerExec:  input.PlayersPerExec,
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
			PlayersPerExec:  input.PlayersPerExec,
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
func runPhaseVerifyUnmatched(ctx workflow.Context, tracker *shared.ReportTracker, input *processPlayersInternalInput) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ProcessPlayersWorkflow Phase 3: verifying unmatched players")

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Load unmatched players
	var playerAct *workplayer.Activities
	var unmatchedPlayers []workplayer.UnmatchedYahooPlayer
	if err := workflow.ExecuteActivity(ctx, playerAct.LoadUnmatchedYahooPlayers).Get(ctx, &unmatchedPlayers); err != nil {
		logger.Warn("Failed to load unmatched Yahoo players", "error", err)
		unmatchedPlayers = []workplayer.UnmatchedYahooPlayer{}
	}

	tracker.SetBarTotal(GroupVerifyUnmatched, 0, len(unmatchedPlayers))
	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatchedPlayers))

	// Verify unmatched players in batches (concurrent)
	unmatchedReport := &workplayer.UnmatchedReport{
		TrulyUnmatched: make([]workplayer.VerifiedPlayer, 0),
	}

	if len(unmatchedPlayers) > 0 {
		batchSize := workplayer.DefaultVerifyBatchSize
		numBatches := shared.BatchCount(len(unmatchedPlayers), batchSize)
		concurrency := verifyConcurrency

		err := tracker.RunWorkerPoolWithIncrement(ctx, GroupVerifyUnmatched, 0, numBatches, concurrency,
			func(i int) int { return len(shared.BatchSlice(unmatchedPlayers, i, batchSize)) },
			func(_ workflow.Context, batchIndex int) workflow.Future {
				batch := shared.BatchSlice(unmatchedPlayers, batchIndex, batchSize)
				return workflow.ExecuteActivity(ctx, playerAct.VerifyUnmatchedBatch, batch)
			},
			func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
				var batchResult *workplayer.VerifyUnmatchedResult
				if err := f.Get(ctx, &batchResult); err != nil {
					logger.Warn("Failed to verify batch", "error", err, "batch_index", batchIndex)
					return nil // Continue processing other batches
				}
				unmatchedReport.TrulyUnmatched = append(unmatchedReport.TrulyUnmatched, batchResult.TrulyUnmatched...)
				unmatchedReport.VerifiedNonNHLCount += len(batchResult.VerifiedNonNHL)
				unmatchedReport.NotFoundCount += len(batchResult.NotFoundInNHL)
				unmatchedReport.UnverifiedCount += len(batchResult.Unverified)
				return nil
			})
		if err != nil {
			logger.Warn("Error during verify pool execution", "error", err)
		}

		if unmatchedReport.UnverifiedCount > 0 {
			logger.Warn("Some unmatched players could not be verified (NHL API errors); they will be retried next run",
				"unverified", unmatchedReport.UnverifiedCount)
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
		if err := workflow.ExecuteActivity(ctx, playerAct.CleanupYahooIDPoolData).Get(ctx, nil); err != nil {
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
		fmt.Sprintf("Imported %d players: %d Yahoo! matched, %d truly unmatched, %d unverified and %d errors in %s.",
			input.TotalImported, input.TotalMatched, len(unmatchedReport.TrulyUnmatched),
			unmatchedReport.UnverifiedCount, len(input.AllErrors), tracker.GetElapsed(ctx, GroupVerifyUnmatched)))

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
		yahooPoolResult = &workplayer.SaveYahooIDPoolResult{}
	}

	result := &ProcessPlayersResult{
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
	}

	// Per-player errors are best-effort accumulated by ProcessPlayerBatch
	// (see worker/player/process_batch.go). When the workflow finishes
	// with any errors collected, log them all (so they appear in worker
	// logs even when payload truncation hides them in the workflow result)
	// and fail the workflow so the sync chain halts visibly. A 'success'
	// completion with silent errors is exactly how the FK-violation gap
	// (player_season_totals_season_team_id_fkey) went unnoticed.
	if len(input.AllErrors) > 0 {
		for _, e := range input.AllErrors {
			logger.Error("ProcessPlayers per-player error", "error", e)
		}
		return result, fmt.Errorf(
			"ProcessPlayersWorkflow finished with %d per-player error(s):\n  %s",
			len(input.AllErrors),
			strings.Join(input.AllErrors, "\n  "))
	}

	return result, nil
}

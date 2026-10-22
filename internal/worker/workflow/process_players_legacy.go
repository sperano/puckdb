package workflow

// The ProcessPlayers chain as it ran before the player manifest: every
// ContinueAsNew carries the whole player list and every error. Kept so that a
// chain started before the deploy finishes on the code it started with;
// ProcessPlayersWorkflow takes this path only when replaying a run 1 recorded
// without the processPlayersManifestChangeID marker. Remove this file,
// ProcessPlayersWorkflowContinue's registration and the ProcessPlayerBatch
// activity once no process-players chain started before the manifest is
// running (see the wiki page puckdb/plans/process-players-continuation).

import (
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

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

// runLegacyFirstExecution is run 1 of a legacy chain: load the whole player
// list from Redis, run phase 1 and continue with the list in the input.
func runLegacyFirstExecution(ctx workflow.Context, logger log.Logger, cfg processPlayersConfig) (*ProcessPlayersResult, error) {
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
	saveResult, err := loadYahooPool(ctx, tracker, input.BatchSize, input.Concurrency)
	if err != nil {
		return nil, err
	}

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
	opts := shared.PoolOptions{
		Total: numBatches, Concurrency: input.Concurrency,
		Progress: shared.SingleBar{
			Bar:       shared.BarRef{Group: GroupProcessPlayers},
			Increment: func(i int) int { return len(shared.BatchSlice(window, i, input.BatchSize)) },
		},
		Start: func(_ workflow.Context, batchIndex int) workflow.Future {
			batch := shared.BatchSlice(window, batchIndex, input.BatchSize)
			return workflow.ExecuteActivity(activityCtx, playerAct.ProcessPlayerBatch, batch)
		},
		Handle: func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
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
		},
	}
	if err := tracker.RunWorkerPool(ctx, opts); err != nil {
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
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	unmatchedPlayers := loadUnmatchedPlayers(ctx)
	// No save here, unlike runManifestVerify: recorded legacy histories
	// have none.
	tracker.SetBarTotal(shared.BarRef{Group: GroupVerifyUnmatched}, len(unmatchedPlayers))
	tracker.RecalcTotal()
	unmatchedReport := verifyUnmatchedPlayers(ctx, tracker, unmatchedPlayers)

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

package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/core"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// Bounds of the errors a ProcessPlayers chain carries from one execution to
// the next. Every error is in process_players_manifest_errors and the worker
// log; the state keeps a sample for the final failure message.
const (
	maxCarriedErrorSample = 20  // errors carried
	maxCarriedErrorRunes  = 500 // longest carried error message
)

// processPlayersState is everything one execution of a ProcessPlayers chain
// hands to the next. Its encoded size does not depend on the number of
// players or errors: the player list stays in the durable manifest and the
// errors in its error table.
type processPlayersState struct {
	Config   processPlayersConfig   // snapshotted once in run 1
	Manifest workplayer.ManifestRef // the chain's player list
	Phase    processPlayersPhase
	// Cursor is the next manifest ordinal phase 2 processes.
	Cursor      int
	Totals      processPlayersTotals
	ErrorSample []string
	YahooPool   workplayer.SaveYahooIDPoolResult
	// Progress is the chain's report. Carried rather than reloaded from
	// Redis, so a flushed or expired progress key cannot fail a phase
	// boundary; Redis only serves it to the API.
	Progress *shared.ProgressReport
}

// processPlayersTotals aggregates batch results across executions.
type processPlayersTotals struct {
	Downloaded int
	Missing    int
	Imported   int
	Matched    int
	Errors     int
	Origins    core.OriginCounts
}

// validate rejects a state run 1 could not have produced: a zero batch size
// or window would loop forever, and a cursor outside the manifest would
// skip or repeat players.
func (s *processPlayersState) validate() error {
	if s == nil || s.Progress == nil {
		return errors.New("process players continuation without state")
	}
	cfg := s.Config
	if cfg.BatchSize <= 0 || cfg.Concurrency <= 0 || cfg.PlayersPerExec <= 0 {
		return fmt.Errorf("process players continuation with invalid config %+v", cfg)
	}
	if s.Manifest.Count <= 0 || s.Cursor < 0 || s.Cursor > s.Manifest.Count ||
		(s.Phase == phaseProcessPlayers && s.Cursor == s.Manifest.Count) {
		return fmt.Errorf("process players continuation with cursor %d in phase %s of a %d-player manifest",
			s.Cursor, s.Phase, s.Manifest.Count)
	}
	return nil
}

// add counts one completed batch.
func (t *processPlayersTotals) add(result workplayer.ProcessPlayerBatchResult) {
	t.Downloaded += result.Downloaded
	t.Missing += result.Missing
	t.Imported += result.Imported
	t.Matched += result.Matched
	if t.Origins == nil {
		t.Origins = core.OriginCounts{}
	}
	t.Origins.Add(result.Origins)
}

// countErrors counts errs and keeps them in the sample while it has room.
func (s *processPlayersState) countErrors(errs []workplayer.PlayerError) {
	s.Totals.Errors += len(errs)
	for _, e := range errs {
		if len(s.ErrorSample) >= maxCarriedErrorSample {
			return
		}
		s.ErrorSample = append(s.ErrorSample, truncateRunes(e.Message, maxCarriedErrorRunes))
	}
}

// truncateRunes returns s cut to at most maxRunes runes.
func truncateRunes(s string, maxRunes int) string {
	if len(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// ordinalRange is the manifest ordinals [From, To).
type ordinalRange struct {
	From int
	To   int
}

// manifestBatches splits the ordinals [from, to) into batches of at most
// batchSize.
func manifestBatches(from, to, batchSize int) []ordinalRange {
	batches := make([]ordinalRange, 0, shared.BatchCount(to-from, batchSize))
	for start := from; start < to; start += batchSize {
		batches = append(batches, ordinalRange{From: start, To: min(start+batchSize, to)})
	}
	return batches
}

// runManifestFirstExecution is run 1 of a chain: copy the player list into a
// durable manifest named by the chain's FirstRunID, run phase 1, and continue
// with a bounded state. Each phase-2 execution then processes
// Config.PlayersPerExec manifest ordinals from Cursor, and the last one moves
// to phase 3.
func runManifestFirstExecution(ctx workflow.Context, cfg processPlayersConfig) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	info := workflow.GetInfo(ctx)
	var playerAct *workplayer.Activities
	var manifest workplayer.ManifestRef
	if err := workflow.ExecuteActivity(ctx, playerAct.CreateProcessPlayersManifest, workplayer.CreateManifestInput{
		ManifestID: shared.ChainRunID(info),
		WorkflowID: info.WorkflowExecution.ID,
	}).Get(ctx, &manifest); err != nil {
		return nil, fmt.Errorf("create process players manifest: %w", err)
	}
	if manifest.Count == 0 {
		logger.Info("No players to process")
		return &ProcessPlayersResult{}, nil
	}

	logger.Info("ProcessPlayersWorkflow started",
		"totalPlayers", manifest.Count,
		"manifest", manifest.ID,
		"batchSize", cfg.BatchSize,
		"concurrency", cfg.Concurrency)

	state := &processPlayersState{
		Config:   cfg,
		Manifest: manifest,
		Phase:    phaseProcessPlayers,
		Totals:   processPlayersTotals{Origins: core.OriginCounts{}},
		Progress: NewProcessPlayersProgressReport(manifest.Count),
	}
	tracker, err := shared.InitTracker(ctx, state.Progress)
	if err != nil {
		return nil, err
	}
	pool, err := loadYahooPool(ctx, tracker, cfg.BatchSize, cfg.Concurrency)
	if err != nil {
		return nil, err
	}
	state.YahooPool = *pool

	// Start next group before ContinueAsNew so the spinner has an in-progress
	// line to attach to (avoids "⠋ ✓ completed msg" rendering).
	tracker.StartGroup(ctx, GroupProcessPlayers)
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinueV2, state)
}

// ProcessPlayersWorkflowContinueV2 is every execution of a manifest chain
// after run 1.
func ProcessPlayersWorkflowContinueV2(ctx workflow.Context, state *processPlayersState) (*ProcessPlayersResult, error) {
	if err := state.validate(); err != nil {
		return nil, err
	}
	tracker, err := shared.InitTracker(ctx, state.Progress)
	if err != nil {
		return nil, err
	}
	// Publish the carried report again: Redis may have lost it.
	tracker.Save(ctx)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	if err := ensureYahooPool(ctx, state); err != nil {
		return nil, err
	}

	switch state.Phase {
	case phaseProcessPlayers:
		return runManifestWindow(ctx, tracker, state)
	case phaseVerifyUnmatched:
		return runManifestVerify(ctx, tracker, state)
	default:
		return nil, fmt.Errorf("unknown phase: %s", state.Phase)
	}
}

// ensureYahooPool refreshes the Yahoo ID pool's TTL, or rebuilds the pool
// when Redis lost it, and records the size the next check compares against.
func ensureYahooPool(ctx workflow.Context, state *processPlayersState) error {
	var playerAct *workplayer.Activities
	var result workplayer.EnsureYahooIDPoolResult
	if err := workflow.ExecuteActivity(ctx, playerAct.EnsureYahooIDPool,
		workplayer.EnsureYahooIDPoolInput{PoolSize: state.YahooPool.PoolSize}).Get(ctx, &result); err != nil {
		return fmt.Errorf("ensure yahoo id pool: %w", err)
	}
	if result.Rebuilt {
		workflow.GetLogger(ctx).Warn("Rebuilt the lost Yahoo ID pool",
			"expected", state.YahooPool.PoolSize, "found", result.FoundSize,
			"rebuilt", result.PoolSize, "available", result.Available,
			"readErrors", result.ReadErrors, "parseErrors", result.ParseErrors)
	}
	state.YahooPool.PoolSize = result.PoolSize
	return nil
}

// runManifestWindow is one phase-2 execution: process the manifest ordinals
// [Cursor, Cursor+PlayersPerExec), then continue with the next window or
// with phase 3.
func runManifestWindow(ctx workflow.Context, tracker *shared.ReportTracker, state *processPlayersState) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)
	cfg := state.Config
	from, to := state.Cursor, min(state.Cursor+cfg.PlayersPerExec, state.Manifest.Count)
	batches := manifestBatches(from, to, cfg.BatchSize)

	logger.Info("ProcessPlayersWorkflow Phase 2: processing players",
		"total_players", state.Manifest.Count,
		"start_index", from,
		"end_index", to,
		"num_batches", len(batches),
		"concurrency", cfg.Concurrency,
		"batch_size", cfg.BatchSize)

	next := *state
	var playerAct *workplayer.Activities
	opts := shared.PoolOptions{
		Total: len(batches), Concurrency: cfg.Concurrency,
		Progress: shared.SingleBar{
			Bar:       shared.BarRef{Group: GroupProcessPlayers},
			Increment: func(i int) int { return batches[i].To - batches[i].From },
		},
		Start: func(_ workflow.Context, i int) workflow.Future {
			return workflow.ExecuteActivity(ctx, playerAct.ProcessPlayerManifestBatch, workplayer.ManifestBatchInput{
				Manifest: state.Manifest, From: batches[i].From, To: batches[i].To,
				YahooPoolSize: state.YahooPool.PoolSize,
			})
		},
		Handle: func(ctx workflow.Context, _ int, f workflow.Future) error {
			var result workplayer.ManifestBatchResult
			if err := f.Get(ctx, &result); err != nil {
				return err
			}
			next.Totals.add(result.ProcessPlayerBatchResult)
			return recordBatchErrors(ctx, &next, result.PlayerErrors)
		},
	}
	if err := tracker.RunWorkerPool(ctx, opts); err != nil {
		return nil, err
	}

	next.Cursor = to
	if to < state.Manifest.Count {
		logger.Info("Continuing to next execution", "completed_so_far", to, "remaining", state.Manifest.Count-to)
		return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinueV2, &next)
	}
	completePhaseProcessPlayers(ctx, tracker, &next)
	return nil, workflow.NewContinueAsNewError(ctx, ProcessPlayersWorkflowContinueV2, &next)
}

// completePhaseProcessPlayers closes phase 2 after its last window and moves
// state to phase 3.
func completePhaseProcessPlayers(ctx workflow.Context, tracker *shared.ReportTracker, state *processPlayersState) {
	totals := state.Totals
	msg := fmt.Sprintf("Processed %d players (%d imported, %d matched) in %s.",
		state.Manifest.Count, totals.Imported, totals.Matched, tracker.GetElapsed(ctx, GroupProcessPlayers))
	tracker.CompleteGroup(ctx, GroupProcessPlayers, totals.Origins.AppendSummary(msg, "landing origins"))
	// Start next group before ContinueAsNew so the spinner has an in-progress
	// line to attach to (avoids "⠋ ✓ completed msg" rendering).
	tracker.StartGroup(ctx, GroupVerifyUnmatched)
	workflow.GetLogger(ctx).Info("Phase 2 complete",
		"downloaded", totals.Downloaded,
		"cache_hits", totals.Origins[core.OriginRedis]+totals.Origins[core.OriginFileSystem],
		"missing", totals.Missing,
		"imported", totals.Imported,
		"matched", totals.Matched,
		"errors", totals.Errors)
	state.Phase = phaseVerifyUnmatched
}

// recordBatchErrors stores a completed batch's errors in the manifest's
// error table, then counts them in state. Only errors of recorded results
// reach the table, so it always matches the carried count.
func recordBatchErrors(ctx workflow.Context, state *processPlayersState, errs []workplayer.PlayerError) error {
	if len(errs) == 0 {
		return nil
	}
	var playerAct *workplayer.Activities
	if err := workflow.ExecuteActivity(ctx, playerAct.RecordProcessPlayersErrors, workplayer.RecordErrorsInput{
		ManifestID: state.Manifest.ID, Errors: errs,
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("record per-player errors: %w", err)
	}
	state.countErrors(errs)
	return nil
}

// runManifestVerify is phase 3, the chain's last execution: verify the
// unmatched Yahoo players, close the manifest and report the result.
func runManifestVerify(ctx workflow.Context, tracker *shared.ReportTracker, state *processPlayersState) (*ProcessPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	unmatchedPlayers := loadUnmatchedPlayers(ctx)
	tracker.SetBarTotal(shared.BarRef{Group: GroupVerifyUnmatched}, len(unmatchedPlayers))
	tracker.RecalcTotal()
	tracker.Save(ctx)
	unmatchedReport := verifyUnmatchedPlayers(ctx, tracker, unmatchedPlayers)

	finishManifest(ctx, state)

	totals := state.Totals
	tracker.CompleteGroup(ctx, GroupVerifyUnmatched,
		fmt.Sprintf("Imported %d players: %d Yahoo! matched, %d truly unmatched, %d unverified and %d errors in %s.",
			totals.Imported, totals.Matched, len(unmatchedReport.TrulyUnmatched),
			unmatchedReport.UnverifiedCount, totals.Errors, tracker.GetElapsed(ctx, GroupVerifyUnmatched)))

	result := manifestResult(state, unmatchedReport)
	logger.Info("ProcessPlayersWorkflow completed",
		"totalPlayers", result.TotalPlayers,
		"downloaded", result.Downloaded,
		"cacheHits", result.CacheHits,
		"missing", result.Missing,
		"imported", result.ImportedPlayers,
		"matchedWithYahoo", result.MatchedWithYahoo,
		"trulyUnmatched", len(result.TrulyUnmatched))

	// Fail the chain when any player failed, so the sync halts visibly: a
	// 'success' with silent errors is how the FK-violation gap
	// (player_season_totals_season_team_id_fkey) went unnoticed.
	if totals.Errors > 0 {
		return result, fmt.Errorf(
			"ProcessPlayersWorkflow finished with %d per-player error(s); all are in process_players_manifest_errors "+
				"(manifest_id %s) and the worker log; the first %d:\n  %s",
			totals.Errors, state.Manifest.ID, len(state.ErrorSample), strings.Join(state.ErrorSample, "\n  "))
	}
	return result, nil
}

// finishManifest marks the manifest finished. Its failure only loses that
// bookkeeping, so it is logged and the chain still reports its result.
func finishManifest(ctx workflow.Context, state *processPlayersState) {
	logger := workflow.GetLogger(ctx)
	var playerAct *workplayer.Activities
	var storedErrors int
	if err := workflow.ExecuteActivity(ctx, playerAct.FinishProcessPlayersManifest, state.Manifest.ID).Get(ctx, &storedErrors); err != nil {
		logger.Warn("Failed to finish process players manifest", "manifest", state.Manifest.ID, "error", err)
		return
	}
	if storedErrors != state.Totals.Errors {
		logger.Warn("Manifest error table does not match the carried error count",
			"manifest", state.Manifest.ID, "stored", storedErrors, "carried", state.Totals.Errors)
	}
}

// manifestResult builds the workflow result of a finished manifest chain.
func manifestResult(state *processPlayersState, unmatched *workplayer.UnmatchedReport) *ProcessPlayersResult {
	totals := state.Totals
	return &ProcessPlayersResult{
		TotalPlayers:          state.Manifest.Count,
		ImportedPlayers:       totals.Imported,
		MatchedWithYahoo:      totals.Matched,
		Downloaded:            totals.Downloaded,
		CacheHits:             totals.Origins[core.OriginRedis] + totals.Origins[core.OriginFileSystem],
		Missing:               totals.Missing,
		TotalYahooPlayers:     state.YahooPool.TotalPlayers,
		SkippedNonNHL:         state.YahooPool.SkippedNonNHL,
		VerifiedNonNHLThisRun: unmatched.VerifiedNonNHLCount,
		TrulyUnmatched:        unmatched.TrulyUnmatched,
		Errors:                state.ErrorSample,
	}
}

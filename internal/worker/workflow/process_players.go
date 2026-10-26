package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDProcessPlayers = "process-players"

// Group indices for ProcessPlayers progress tracking.
const (
	GroupLoadYahoo       = 0
	GroupProcessPlayers  = 1
	GroupVerifyUnmatched = 2
)

const (
	// processPlayersYahooEstimate is the Yahoo bar's Total until Phase 1
	// resizes it to the number of Yahoo player batches.
	processPlayersYahooEstimate = 1
	// processPlayersUnmatchedEstimate is the Unmatched bar's Total until
	// Phase 3 resizes it to the number of unmatched players.
	processPlayersUnmatchedEstimate = 1
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

// processPlayersManifestChangeID is the GetVersion change ID of run 1 of
// ProcessPlayersWorkflow. workflow.DefaultVersion (a run 1 recorded before
// the change) keeps the legacy path.
const processPlayersManifestChangeID = "process-players-manifest"

// processPlayersManifestVersion copies the player list into a durable
// manifest and continues as ProcessPlayersWorkflowContinueV2.
const processPlayersManifestVersion workflow.Version = 1

// ProcessPlayersInput contains parameters for the process players workflow.
type ProcessPlayersInput struct {
	BatchSize   *int // Players per batch activity
	Concurrency *int // Parallel activities
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

	// Errors: every error for a legacy chain; for a manifest chain the first
	// maxCarriedErrorSample (all of them are in process_players_manifest_errors).
	Errors []string
}

// NewProcessPlayersProgressReport creates the initial progress structure.
// Total is the sum of the bar totals, like every report AddBar builds.
func NewProcessPlayersProgressReport(totalPlayers int) *shared.ProgressReport {
	report := &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Loading Yahoo player pool...", Bars: []shared.ProgressBar{{Label: "Yahoo", Total: processPlayersYahooEstimate}}},
			{Header: "Processing players...", Bars: []shared.ProgressBar{{Label: "Players", Total: totalPlayers}}},
			{Header: "Verifying unmatched players...", Bars: []shared.ProgressBar{{Label: "Unmatched", Total: processPlayersUnmatchedEstimate}}},
		},
	}
	report.Total = progressBarTotal(report)
	return report
}

// progressBarTotal is the sum of every bar's Total in report.
func progressBarTotal(report *shared.ProgressReport) int {
	total := 0
	for _, g := range report.Groups {
		for _, bar := range g.Bars {
			total += bar.Total
		}
	}
	return total
}

// ProcessPlayersWorkflow processes the boxscore players extracted by
// ExtractBoxscorePlayersWorkflow: it downloads their landing pages, imports
// them to the database and matches them with Yahoo players, as a chain of
// ContinueAsNew executions (see runManifestFirstExecution).
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

	if workflow.GetVersion(ctx, processPlayersManifestChangeID, workflow.DefaultVersion, processPlayersManifestVersion) ==
		workflow.DefaultVersion {
		return runLegacyFirstExecution(ctx, logger, cfg)
	}
	return runManifestFirstExecution(ctx, cfg)
}

// loadYahooPool is phase 1: parse every stored Yahoo player file and save the
// Yahoo ID pool to Redis. ctx must carry activity options.
func loadYahooPool(ctx workflow.Context, tracker *shared.ReportTracker, batchSize, concurrency int) (*workplayer.SaveYahooIDPoolResult, error) {
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
	batches := shared.NewBatches(yahooPlayerIDs, batchSize)
	// Resized without a save: legacy chain histories record no save here
	// (ResizeBar would add a local activity they lack); the next save (the
	// first batch's IncrementBarBy in RunWorkerPool, or CompleteGroup with
	// no batches) writes the new size.
	tracker.SetBarTotal(shared.BarRef{Group: GroupLoadYahoo}, batches.Count())
	tracker.RecalcTotal()

	opts := shared.PoolOptions{
		Total: batches.Count(), Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupLoadYahoo}},
		Start: func(_ workflow.Context, batchIndex int) workflow.Future {
			return workflow.ExecuteActivity(ctx, playerAct.ParseYahooPlayerBatch, batches.Batch(batchIndex))
		},
		Handle: shared.CollectSlicesInto(&allYahooPlayers),
	}
	if err := tracker.RunWorkerPool(ctx, opts); err != nil {
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
	return saveResult, nil
}

// loadUnmatchedPlayers returns the Yahoo players phase 2 left unmatched, or
// none when they cannot be loaded. ctx must carry activity options.
func loadUnmatchedPlayers(ctx workflow.Context) []workplayer.UnmatchedYahooPlayer {
	logger := workflow.GetLogger(ctx)
	logger.Info("ProcessPlayersWorkflow Phase 3: verifying unmatched players")

	var playerAct *workplayer.Activities
	var unmatchedPlayers []workplayer.UnmatchedYahooPlayer
	if err := workflow.ExecuteActivity(ctx, playerAct.LoadUnmatchedYahooPlayers).Get(ctx, &unmatchedPlayers); err != nil {
		logger.Warn("Failed to load unmatched Yahoo players", "error", err)
		unmatchedPlayers = []workplayer.UnmatchedYahooPlayer{}
	}
	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatchedPlayers))
	return unmatchedPlayers
}

// verifyUnmatchedPlayers is phase 3: check each unmatched Yahoo player
// against the NHL API, then delete the pool from Redis. ctx must carry
// activity options.
func verifyUnmatchedPlayers(ctx workflow.Context, tracker *shared.ReportTracker, unmatchedPlayers []workplayer.UnmatchedYahooPlayer) *workplayer.UnmatchedReport {
	logger := workflow.GetLogger(ctx)
	var playerAct *workplayer.Activities

	// Verify unmatched players in batches (concurrent)
	unmatchedReport := &workplayer.UnmatchedReport{
		TrulyUnmatched: make([]workplayer.VerifiedPlayer, 0),
	}
	if len(unmatchedPlayers) == 0 {
		return unmatchedReport
	}

	batches := shared.NewBatches(unmatchedPlayers, workplayer.DefaultVerifyBatchSize)
	err := tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: batches.Count(), Concurrency: verifyConcurrency,
		Progress: batches.ItemProgress(shared.BarRef{Group: GroupVerifyUnmatched}),
		Start: func(_ workflow.Context, batchIndex int) workflow.Future {
			return workflow.ExecuteActivity(ctx, playerAct.VerifyUnmatchedBatch, batches.Batch(batchIndex))
		},
		Handle: func(ctx workflow.Context, batchIndex int, f workflow.Future) error {
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
		},
	})
	if err != nil {
		logger.Warn("Error during verify pool execution", "error", err)
	}
	logUnmatchedReport(logger, unmatchedReport)

	// Cleanup Redis keys
	if err := workflow.ExecuteActivity(ctx, playerAct.CleanupYahooIDPoolData).Get(ctx, nil); err != nil {
		logger.Warn("Failed to cleanup Yahoo ID pool", "error", err)
	}
	return unmatchedReport
}

// logUnmatchedReport logs the players phase 3 could not verify or found
// with NHL games but no match.
func logUnmatchedReport(logger log.Logger, unmatchedReport *workplayer.UnmatchedReport) {
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
}

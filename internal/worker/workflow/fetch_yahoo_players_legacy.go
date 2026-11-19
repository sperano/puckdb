package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// FetchYahooPlayersScanConfig is the configuration the retired player ID
// scan snapshotted once per logical run and carried across ContinueAsNew in
// FetchYahooPlayersInput.Config; its JSON names are those the recorded
// histories hold.
type FetchYahooPlayersScanConfig struct {
	MaxPlayerID         int `json:"maxPlayerID"`
	Concurrency         int `json:"concurrency"`
	ActivityBatchSize   int `json:"activityBatchSize"`
	PlayersPerExecution int `json:"playersPerExecution"`
	// MissingTTLDays is passed to every FetchYahooPlayerBatch: it decides
	// which ids are downloaded again, so it is frozen for the whole run.
	MissingTTLDays int `json:"missingTTLDays"`
}

// The scan settings' last defaults. The scan's flags are gone, so a legacy
// execution that has to snapshot its config takes these; replaying one reuses
// the recorded snapshot.
const (
	legacyScanMaxPlayerID         = 35000
	legacyScanConcurrency         = 5
	legacyScanActivityBatchSize   = 100
	legacyScanPlayersPerExecution = 5000
	legacyScanMissingTTLDays      = 30
)

// loadFetchYahooPlayersScanConfig returns the scan's last defaults.
func loadFetchYahooPlayersScanConfig() FetchYahooPlayersScanConfig {
	return FetchYahooPlayersScanConfig{
		MaxPlayerID:         legacyScanMaxPlayerID,
		Concurrency:         legacyScanConcurrency,
		ActivityBatchSize:   legacyScanActivityBatchSize,
		PlayersPerExecution: legacyScanPlayersPerExecution,
		MissingTTLDays:      legacyScanMissingTTLDays,
	}
}

// resolveFetchYahooPlayersScanConfig returns carried when the caller supplied
// one (a ContinueAsNew resumption within the same logical run), or snapshots
// the config for the first execution of a run.
func resolveFetchYahooPlayersScanConfig(ctx workflow.Context, carried *FetchYahooPlayersScanConfig) (*FetchYahooPlayersScanConfig, error) {
	if carried != nil {
		return carried, nil
	}
	snapshot, err := shared.SnapshotConfig(ctx, loadFetchYahooPlayersScanConfig)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// fetchYahooPlayersState is the continuation state carried across
// ContinueAsNew executions of one logical run. Distinct from
// FetchYahooPlayersScanConfig, which is the snapshotted settings for the run.
type fetchYahooPlayersState struct {
	StartID         int
	TotalCompleted  int
	TotalFound      int
	TotalDownloaded int
	TotalMissing    int
	StartedAt       time.Time
}

// parseFetchYahooPlayersState extracts the continuation state from input,
// defaulting to a fresh run's starting point when input is nil (the first
// execution).
func parseFetchYahooPlayersState(ctx workflow.Context, input *FetchYahooPlayersInput) fetchYahooPlayersState {
	state := fetchYahooPlayersState{StartID: 1, StartedAt: workflow.Now(ctx)}
	if input == nil {
		return state
	}
	if input.StartPlayerID > 0 {
		state.StartID = input.StartPlayerID
	}
	state.TotalCompleted = input.TotalCompleted
	state.TotalFound = input.TotalFound
	state.TotalDownloaded = input.TotalDownloaded
	state.TotalMissing = input.TotalMissing
	if !input.StartedAt.IsZero() {
		state.StartedAt = input.StartedAt
	}
	return state
}

// newFetchYahooPlayersScanProgressReport creates the scan's progress
// structure. completed is the cumulative count from previous ContinueAsNew
// executions.
func newFetchYahooPlayersScanProgressReport(total, completed int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Total:     total,
		Completed: completed,
		Groups: []shared.ProgressGroup{
			{Header: "Fetching Yahoo! players...", Bars: []shared.ProgressBar{{Total: total, Current: completed}}},
		},
	}
}

// fetchYahooPlayersScan is FetchYahooPlayersWorkflow as executions recorded
// before the game player lists ran it: Yahoo player pages from ID 1 to the
// snapshotted MaxPlayerID, with ContinueAsNew every PlayersPerExecution IDs.
// Kept unchanged for those executions to replay; their FetchYahooPlayerBatch
// now downloads nothing, and their ContinueAsNew starts an execution of the
// game player list download.
func fetchYahooPlayersScan(ctx workflow.Context, input *FetchYahooPlayersInput) error {
	logger := workflow.GetLogger(ctx)

	state := parseFetchYahooPlayersState(ctx, input)
	startID := state.StartID
	totalCompleted := state.TotalCompleted
	totalFound := state.TotalFound
	totalDownloaded := state.TotalDownloaded
	totalMissing := state.TotalMissing
	startedAt := state.StartedAt

	var carriedConfig *FetchYahooPlayersScanConfig
	if input != nil {
		carriedConfig = input.Config
	}
	cfg, err := resolveFetchYahooPlayersScanConfig(ctx, carriedConfig)
	if err != nil {
		return err
	}

	maxPlayerID := cfg.MaxPlayerID
	concurrency := cfg.Concurrency
	activityBatchSize := cfg.ActivityBatchSize
	playersPerExecution := cfg.PlayersPerExecution
	// A config carried from before MissingTTLDays existed holds 0; fall back
	// to the default so markers still expire.
	missingTTLDays := cfg.MissingTTLDays
	if missingTTLDays <= 0 {
		missingTTLDays = legacyScanMissingTTLDays
	}
	missingTTL := time.Duration(missingTTLDays) * hoursPerDay * time.Hour

	// Calculate this execution's range
	endID := startID + playersPerExecution - 1
	if endID > maxPlayerID {
		endID = maxPlayerID
	}
	totalPlayers := endID - startID + 1

	// Activity batches over this execution's IDs, as offsets from startID.
	batches := shared.NewBatchRanges(totalPlayers, activityBatchSize)

	logger.Info("FetchYahooPlayersWorkflow started",
		"start_id", startID,
		"end_id", endID,
		"max_player_id", maxPlayerID,
		"concurrency", concurrency,
		"activity_batch_size", activityBatchSize,
		"num_activity_batches", batches.Count(),
		"total_completed", totalCompleted)

	// Set up progress tracking with the new ReportTracker
	tracker, err := shared.InitTracker(ctx, newFetchYahooPlayersScanProgressReport(maxPlayerID, totalCompleted))
	if err != nil {
		return err
	}
	tracker.StartGroup(ctx, GroupFetchYahooPlayers)

	var yahooAct *yahoo.FetchActivities
	// Each batch downloads up to activityBatchSize pages behind the rate limiter.
	activityCtx := workflow.WithActivityOptions(ctx, shared.YahooDownloadActivityOptions())
	startActivity := func(ctx workflow.Context, batchIndex int) workflow.Future {
		first, end := batches.Bounds(batchIndex)
		return workflow.ExecuteActivity(activityCtx, yahooAct.FetchYahooPlayerBatch, startID+first, startID+end-1, missingTTL)
	}

	var executionStats shared.FetchStats
	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var result shared.FetchStats
		if err := future.Get(ctx, &result); err != nil {
			return err
		}
		executionStats.Add(result)
		return nil
	}

	// Each completed batch advances the bar by the IDs it covered.
	opts := shared.PoolOptions{
		Total: batches.Count(), Concurrency: concurrency,
		Progress: batches.ItemProgress(shared.BarRef{Group: GroupFetchYahooPlayers}),
		Start:    startActivity,
		Handle:   handler,
	}
	if err := tracker.RunWorkerPool(ctx, opts); err != nil {
		return err
	}

	// Continue with next batch if more players remain
	if endID < maxPlayerID {
		return workflow.NewContinueAsNewError(ctx, FetchYahooPlayersWorkflow,
			&FetchYahooPlayersInput{
				StartPlayerID:   endID + 1,
				TotalCompleted:  totalCompleted + totalPlayers,
				TotalFound:      totalFound + executionStats.Downloaded + executionStats.CacheHits,
				TotalDownloaded: totalDownloaded + executionStats.Downloaded,
				TotalMissing:    totalMissing + executionStats.Missing,
				StartedAt:       startedAt,
				Config:          cfg,
			})
	}

	// Mark group complete with final count and total elapsed time
	finalFound := totalFound + executionStats.Downloaded + executionStats.CacheHits
	finalDownloaded := totalDownloaded + executionStats.Downloaded
	finalMissing := totalMissing + executionStats.Missing
	finalCount := totalCompleted + totalPlayers
	elapsed := shared.FormatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.CompleteGroup(ctx, GroupFetchYahooPlayers,
		fmt.Sprintf("Found %d/%d Yahoo! players in %s.", finalFound, finalCount, elapsed))

	logFetchYahooPlayersTotals(logger, maxPlayerID, finalCount, finalFound, finalDownloaded, finalMissing)
	return nil
}

// mostMissingPercent is the share of swept ids above which a run that
// downloaded nothing is suspicious: a healthy first sweep downloads thousands
// of pages, and a healthy later one finds them cached, not missing.
const mostMissingPercent = 50

// hoursPerDay converts the missing-marker TTL, configured in days.
const hoursPerDay = 24

// logFetchYahooPlayersTotals logs the run's final counts and warns when the
// run downloaded no page while most ids were missing, the signature of stale
// or wrongly written missing markers.
func logFetchYahooPlayersTotals(logger log.Logger, maxPlayerID, swept, found, downloaded, missing int) {
	logger.Info("FetchYahooPlayersWorkflow completed",
		"max_player_id", maxPlayerID, "swept", swept,
		"downloaded", downloaded, "cached", found-downloaded, "missing", missing)
	if downloaded == 0 && swept > 0 && missing*100 > swept*mostMissingPercent {
		logger.Warn("FetchYahooPlayersWorkflow downloaded no pages while most ids were missing; "+
			"missing markers may be stale or wrong",
			"swept", swept, "missing", missing, "found", found)
	}
}

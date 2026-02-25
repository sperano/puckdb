package worker

import (
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// FetchPlayerLogsWorkflow fetches player game logs for all seasons.
// It processes each season in a child workflow, downloading game logs for players
// extracted from boxscores. For the current season, files are only overwritten
// if the --refresh-current-player-logs flag is set.
//
// The workflow has two phases:
// 1. Extract player counts for all seasons (parallel) - caches results in Redis
// 2. Download player logs for each season (child workflows use cached extraction)
func FetchPlayerLogsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	// Register query handler immediately so progress queries work from workflow start
	tracker := NewProgressTracker(0)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = 10
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

	refreshCurrent := viper.GetBool(config.FlagRefreshCurrentPlayerLogs)

	logger.Info("FetchPlayerLogsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency,
		"refreshCurrent", refreshCurrent)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Fetch season data
	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return err
	}

	if len(seasons) == 0 {
		logger.Warn("No seasons to process")
		return nil
	}

	// Phase 1: Extract player counts for all seasons (parallel)
	// This caches extraction results in Redis for child workflows to use
	playerCounts, err := countPlayersForAllSeasons(ctx, logger, tracker, seasons, concurrency)
	if err != nil {
		return err
	}

	// Create counter function using pre-computed counts
	counter := func(season SeasonInfo) int {
		return playerCounts[season.StartYear()]
	}

	// Initialize tracker with actual player counts
	tracker.InitializeWithSeasons(seasons, counter, "Fetching player logs...", "Fetched player logs for %d seasons")

	// Phase 2: Download player logs for each season
	return processPlayerLogsWithChildWorkflows(ctx, logger, tracker, seasons, concurrency, refreshCurrent)
}

// countPlayersForAllSeasons extracts player counts for all seasons in parallel.
// Results are cached in Redis for child workflows to use.
func countPlayersForAllSeasons(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, seasons []SeasonInfo, concurrency int) (map[int]int, error) {
	logger.Info("Counting players for all seasons", "seasons", len(seasons), "concurrency", concurrency)

	// Initialize tracker for counting phase (1 task per season)
	countOne := func(_ SeasonInfo) int { return 1 }
	tracker.InitializeWithSeasons(seasons, countOne, "Counting players...", "Counted players for %d seasons")

	counts := make(map[int]int)
	pending := make([]SeasonInfo, len(seasons))
	copy(pending, seasons)

	// Track active futures
	type countWork struct {
		season SeasonInfo
		future workflow.Future
	}
	active := make(map[int]*countWork)

	// Start initial batch
	for i := 0; i < concurrency && len(pending) > 0; i++ {
		season := pending[0]
		pending = pending[1:]
		tracker.MarkItemStarted(ctx, season.StartYear())
		future := workflow.ExecuteActivity(ctx, CountPlayersForSeasonActivity, season)
		active[season.StartYear()] = &countWork{season: season, future: future}
	}

	// Process until all done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		for startYear, work := range active {
			year := startYear
			w := work
			selector.AddFuture(w.future, func(f workflow.Future) {
				var count int
				if err := f.Get(ctx, &count); err != nil {
					logger.Error("Failed to count players for season", "startYear", year, "error", err)
					counts[year] = 0
				} else {
					counts[year] = count
					logger.Info("Counted players for season", "startYear", year, "players", count)
				}

				// Mark season complete and update progress
				tracker.IncrementItem(year)
				delete(active, year)

				// Start next pending season
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					tracker.MarkItemStarted(ctx, nextSeason.StartYear())
					future := workflow.ExecuteActivity(ctx, CountPlayersForSeasonActivity, nextSeason)
					active[nextSeason.StartYear()] = &countWork{season: nextSeason, future: future}
				}
			})
		}

		selector.Select(ctx)
	}

	logger.Info("Player counting complete", "seasons", len(counts))
	return counts, nil
}

// processPlayerLogsWithChildWorkflows spawns child workflows for each season's player logs.
// Each season runs in its own child workflow, isolating workflow history.
//
// TODO: Refactor to share code with processWithChildWorkflows in workflow.go.
// Both functions follow the same pattern but call different child workflows.
// Could be unified with a workflow spawner callback function.
func processPlayerLogsWithChildWorkflows(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, seasons []SeasonInfo, concurrency int, refreshCurrent bool) error {
	if len(seasons) == 0 {
		return nil
	}

	// Track active child workflows (startYear -> work)
	active := make(map[int]*childWorkflowWork)
	// Queue of pending seasons
	pending := make([]SeasonInfo, len(seasons))
	copy(pending, seasons)

	// Start initial batch of seasons (up to concurrency)
	for i := 0; i < concurrency && len(pending) > 0; i++ {
		season := pending[0]
		pending = pending[1:]
		startPlayerLogsChildWorkflow(ctx, logger, tracker, active, season, refreshCurrent)
	}

	var firstErr error

	// Process until all work is done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		// Add all active child workflow futures to selector
		for startYear, work := range active {
			year := startYear
			sw := work
			selector.AddFuture(sw.future, func(f workflow.Future) {
				if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				logger.Info("Season player logs completed", "startYear", year)
				markSeasonComplete(tracker, year)
				delete(active, year)

				// Start next pending season immediately
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					startPlayerLogsChildWorkflow(ctx, logger, tracker, active, nextSeason, refreshCurrent)
				}
			})
		}

		// Wait for any child workflow to complete
		selector.Select(ctx)

		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

// startPlayerLogsChildWorkflow spawns a child workflow for a season's player logs.
func startPlayerLogsChildWorkflow(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, active map[int]*childWorkflowWork, season SeasonInfo, refreshCurrent bool) {
	logger.Info("Starting player logs child workflow", "startYear", season.StartYear())
	tracker.MarkItemStarted(ctx, season.StartYear())
	ctxo := withChildOptions(ctx, WorkflowIDFetchSeasonPlayerLogs(season.StartYear()))
	input := FetchSeasonPlayerLogsInput{
		Season:         season,
		RefreshCurrent: refreshCurrent,
	}
	future := workflow.ExecuteChildWorkflow(ctxo, FetchSeasonPlayerLogsWorkflow, input)
	active[season.StartYear()] = &childWorkflowWork{
		season: season,
		future: future,
	}
}

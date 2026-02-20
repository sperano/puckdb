package worker

import (
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonsWorkflow imports season data from cached boxscores into the database.
// It reads boxscore files from SimpleFS (previously downloaded) and upserts them
// into the nhl_games, nhl_game_skater_stats, and nhl_game_goalie_stats tables.
//
// Each season runs in its own child workflow to isolate workflow history.
// This matches the FetchSeasonsWorkflow pattern for consistency.
func ImportSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	// Register query handler immediately so progress queries work from workflow start
	tracker := NewProgressTracker(0)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = config.DefaultMaxSeasonConcurrency
	}

	concurrency := config.DefaultSeasonConcurrency
	if input != nil && input.SeasonConcurrency != nil && *input.SeasonConcurrency > 0 {
		concurrency = *input.SeasonConcurrency
	}
	if concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	logger.Info("ImportSeasonsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return err
	}

	// Teams must already exist in the database before importing boxscores.
	// This is ensured by running ImportNHLTeamsAndPlayersWorkflow first.

	// Initialize progress with headers for GROUPED_ITEMS display (matching FetchSeasons pattern)
	tracker.initializeImportSeasons(seasons, "Importing seasons...", "Imported %d seasons")

	return processImportWithChildWorkflows(ctx, logger, tracker, seasons, concurrency)
}

// initializeImportSeasons sets up per-season progress for import.
// Each season tracks days as its total (no Yahoo league/team tasks for imports).
func (p *ProgressTracker) initializeImportSeasons(seasons []SeasonInfo, header, completedHeader string) {
	total := 0
	items := make([]ItemProgress, len(seasons))

	for i, season := range seasons {
		days := countDaysInSeason(season)
		items[i] = ItemProgress{
			ID:          season.StartYear(),
			Description: season.Label(),
			Total:       days,
			Completed:   0,
		}
		p.itemIndex[season.StartYear()] = i
		total += days
	}

	p.progress = WorkflowProgress{
		Total:           total,
		Completed:       0,
		Header:          header,
		CompletedHeader: completedHeader,
		Items:           items,
		DisplayStyle:    DisplayStyleGroupedItems,
	}
}

// importChildWorkflowWork tracks a child workflow for a single season import
type importChildWorkflowWork struct {
	season SeasonInfo
	future workflow.ChildWorkflowFuture
}

// processImportWithChildWorkflows spawns child workflows for each season.
// Each season runs in its own child workflow, isolating workflow history.
// - Maintains exactly `concurrency` seasons in flight at any time
// - Starts a new season immediately when one completes
func processImportWithChildWorkflows(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, seasons []SeasonInfo, concurrency int) error {
	if len(seasons) == 0 {
		return nil
	}

	// Track active child workflows (startYear -> work)
	active := make(map[int]*importChildWorkflowWork)
	// Queue of pending seasons
	pending := make([]SeasonInfo, len(seasons))
	copy(pending, seasons)

	// Start initial batch of seasons (up to concurrency)
	for i := 0; i < concurrency && len(pending) > 0; i++ {
		season := pending[0]
		pending = pending[1:]
		startImportSeasonChildWorkflow(ctx, logger, tracker, active, season)
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
				logger.Info("Season import completed", "startYear", year)
				// Mark all tasks for this season as complete
				markSeasonComplete(tracker, year)
				delete(active, year)

				// Start next pending season immediately
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					startImportSeasonChildWorkflow(ctx, logger, tracker, active, nextSeason)
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

// startImportSeasonChildWorkflow spawns a child workflow for a season import
func startImportSeasonChildWorkflow(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, active map[int]*importChildWorkflowWork, season SeasonInfo) {
	logger.Info("Starting season import child workflow", "startYear", season.StartYear())
	tracker.MarkItemStarted(ctx, season.StartYear())
	ctxo := withChildOptions(ctx, WorkflowIDImportSeason(season.StartYear()))
	future := workflow.ExecuteChildWorkflow(ctxo, ImportSeasonWorkflow, season)
	active[season.StartYear()] = &importChildWorkflowWork{
		season: season,
		future: future,
	}
}

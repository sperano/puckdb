package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	TaskQueueName                = "puckdb-tasks"
	WorkflowIDImportEverything   = "import-everything"
	WorkflowIDDownloadEverything = "download-everything"
	WorkflowIDDownloadSeasons    = "download-all"
)

func WorkflowIDImportLeague(season int, leagueID int) string {
	return fmt.Sprintf("league-%d-%d", season, leagueID)
}

func WorkflowIDImportTeam(season int, leagueID int, teamID int) string {
	return fmt.Sprintf("team-%d-%d", season, leagueID)
}

func WorkflowIDDownloadGamesForSeason(season int) string {
	return fmt.Sprintf("download-games-for-season-%d", season)
}

func WorkflowIDDownloadEverythingForSeason(season int) string {
	return fmt.Sprintf("download-everything-for-season-%d", season)
}

func WorkflowIDImportEverythingForSeason(season int) string {
	return fmt.Sprintf("import-everything-for-season-%d", season)
}

func withChildOptions(ctx workflow.Context, id string) workflow.Context {
	return workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: config.DefaultWorkflowExecutionTimeout,
		WorkflowTaskTimeout:      config.DefaultWorkflowExecutionTimeout,
		WorkflowID:               id,
	})
}

func defaultActivityOptions() workflow.ActivityOptions {
	initialInterval := viper.GetInt(config.FlagTemporalRetryInitialInterval)
	maxInterval := viper.GetInt(config.FlagTemporalRetryMaxInterval)
	maxAttempts := viper.GetInt32(config.FlagTemporalRetryMaxAttempts)
	return workflow.ActivityOptions{
		StartToCloseTimeout: config.DefaultActivityStartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(initialInterval) * time.Second,
			MaximumInterval:    time.Duration(maxInterval) * time.Second,
			MaximumAttempts:    maxAttempts,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
		},
	}
}

func DownloadSeasonsWorkflow(ctx workflow.Context, input *model.DownloadSeasonsInput) error {
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

	logger.Info("DownloadAllWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return err
	}

	// Update tracker with actual season data now that we know the seasons
	tracker.InitializeWithSeasons(seasons)

	return processWithChildWorkflows(ctx, logger, tracker, seasons, concurrency)
}

// childWorkflowWork tracks a child workflow for a single season
type childWorkflowWork struct {
	season SeasonInfo
	future workflow.ChildWorkflowFuture
}

// processWithChildWorkflows spawns child workflows for each season.
// Each season runs in its own child workflow, isolating workflow history.
// - Maintains exactly `concurrency` seasons in flight at any time
// - Starts a new season immediately when one completes
func processWithChildWorkflows(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, seasons []SeasonInfo, concurrency int) error {
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
		startSeasonChildWorkflow(ctx, logger, tracker, active, season)
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
				logger.Info("Season completed", "startYear", year)
				// Mark all tasks for this season as complete
				markSeasonComplete(tracker, year)
				delete(active, year)

				// Start next pending season immediately
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					startSeasonChildWorkflow(ctx, logger, tracker, active, nextSeason)
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

// startSeasonChildWorkflow spawns a child workflow for a season
func startSeasonChildWorkflow(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, active map[int]*childWorkflowWork, season SeasonInfo) {
	logger.Info("Starting season child workflow", "startYear", season.StartYear)
	tracker.MarkItemStarted(season.StartYear)
	ctxo := withChildOptions(ctx, WorkflowIDDownloadSeason(season.StartYear))
	future := workflow.ExecuteChildWorkflow(ctxo, DownloadSeasonWorkflow, &DownloadSeasonInput{Season: season})
	active[season.StartYear] = &childWorkflowWork{
		season: season,
		future: future,
	}
}

// markSeasonComplete sets all tasks for a season as completed in the tracker
func markSeasonComplete(tracker *ProgressTracker, startYear int) {
	if idx, ok := tracker.itemIndex[startYear]; ok {
		remaining := tracker.progress.Items[idx].Total - tracker.progress.Items[idx].Completed
		tracker.progress.Items[idx].Completed = tracker.progress.Items[idx].Total
		tracker.progress.Completed += remaining
	}
}

// countDaysInSeason returns the number of days from season start to min(season end, today).
func countDaysInSeason(season SeasonInfo) int {
	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}
	days := int(end.Sub(season.StartDate).Hours()/config.HoursPerDay) + 1
	if days < 0 {
		return 0
	}
	return days
}

// countDownloadTasksForSeason counts the total number of download tasks for a single season.
// Each day is a child workflow that counts as 1 task, plus one-time league/team downloads.
func countDownloadTasksForSeason(season SeasonInfo) int {
	days := countDaysInSeason(season)
	// Check if the season is in the Yahoo config
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err != nil {
		return days // Just daily child workflows
	}
	yahooCfg, inYahoo := yahooConfig[season.StartYear]
	if !inYahoo {
		return days // Just daily child workflows
	}
	count := days // One child workflow per day
	// Add league and team downloads (one-time per season)
	for _, league := range yahooCfg.Leagues {
		count++                      // DownloadLeague
		count += len(league.TeamIDs) // DownloadTeam per team
	}
	return count
}

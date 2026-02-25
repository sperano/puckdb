package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	TaskQueueName            = "puckdb-tasks"
	WorkflowIDImportSeasons  = "import-seasons"
	WorkflowIDFetchSeasons   = "fetch-seasons"
	WorkflowIDFetchPlayerLogs = "fetch-player-logs"
)

func withChildOptions(ctx workflow.Context, id string) workflow.Context {
	return workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: config.DefaultWorkflowExecutionTimeout,
		WorkflowTaskTimeout:      config.DefaultWorkflowTaskTimeout,
		WorkflowID:               id,
		// Allow terminating orphaned child workflows from previous failed parent runs
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
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

// fetchDayActivityOptions returns activity options with longer timeout for FetchDayActivity.
// This activity downloads multiple files with rate limiting, requiring more time.
func fetchDayActivityOptions() workflow.ActivityOptions {
	opts := defaultActivityOptions()
	opts.StartToCloseTimeout = config.DefaultFetchDayActivityTimeout
	return opts
}

// Group index for FetchSeasonsWorkflow progress (separate from Initialize's GroupFetchSeasons)
const GroupFetchSeasonsData = 0

// NewFetchSeasonsProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons are known.
func NewFetchSeasonsProgressReport() *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Fetching seasons...", Bars: []ProgressBar{}},
		},
	}
}

func FetchSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	// Register query handler immediately so progress queries work from workflow start
	tracker := NewReportTracker(NewFetchSeasonsProgressReport())
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

	logger.Info("FetchSeasonsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return err
	}

	// Clear stale progress from previous runs
	workflowIDs := make([]string, len(seasons))
	for i, s := range seasons {
		workflowIDs[i] = WorkflowIDFetchSeason(s.StartYear())
	}
	if err := workflow.ExecuteActivity(ctx, ClearProgressActivity, workflowIDs).Get(ctx, nil); err != nil {
		logger.Warn("Failed to clear progress", "error", err)
	}

	// Add bars for each season now that we know them (use countDaysInSeason for consistency)
	barIndex := tracker.AddBarsForSeasons(GroupFetchSeasonsData, seasons, countDaysInSeason, WorkflowIDFetchSeason)
	tracker.StartGroup(ctx, GroupFetchSeasonsData)

	return processWithChildWorkflows(ctx, logger, tracker, barIndex, seasons, concurrency)
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
func processWithChildWorkflows(ctx workflow.Context, logger log.Logger, tracker *ReportTracker, barIndex map[int]int, seasons []SeasonInfo, concurrency int) error {
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
		startSeasonChildWorkflow(ctx, logger, tracker, barIndex, active, season)
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
				// Mark this season's bar as complete
				if barIdx, ok := barIndex[year]; ok {
					tracker.CompleteBar(GroupFetchSeasonsData, barIdx)
				}
				delete(active, year)

				// Start next pending season immediately
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					startSeasonChildWorkflow(ctx, logger, tracker, barIndex, active, nextSeason)
				}
			})
		}

		// Wait for any child workflow to complete
		selector.Select(ctx)

		if firstErr != nil {
			return firstErr
		}
	}

	// Mark group complete
	tracker.CompleteGroup(ctx, GroupFetchSeasonsData, fmt.Sprintf("Fetched %d seasons in %s.", len(seasons), tracker.GetElapsed(ctx, GroupFetchSeasonsData)))

	return nil
}

// startSeasonChildWorkflow spawns a child workflow for a season and marks the bar as started.
func startSeasonChildWorkflow(ctx workflow.Context, logger log.Logger, tracker *ReportTracker, barIndex map[int]int, active map[int]*childWorkflowWork, season SeasonInfo) {
	logger.Info("Starting season child workflow", "startYear", season.StartYear())
	// Mark bar as started when spawning (before child registers query handler)
	if barIdx, ok := barIndex[season.StartYear()]; ok {
		tracker.StartBar(GroupFetchSeasonsData, barIdx)
	}
	ctxo := withChildOptions(ctx, WorkflowIDFetchSeason(season.StartYear()))
	future := workflow.ExecuteChildWorkflow(ctxo, FetchSeasonWorkflow, season)
	active[season.StartYear()] = &childWorkflowWork{
		season: season,
		future: future,
	}
}

// markSeasonComplete sets all tasks for a season as completed in the legacy ProgressTracker.
// Used by workflows that still use ProgressTracker (fetch_player_logs, import_seasons).
func markSeasonComplete(tracker *ProgressTracker, startYear int) {
	if idx, ok := tracker.itemIndex[startYear]; ok {
		remaining := tracker.progress.Items[idx].Total - tracker.progress.Items[idx].Completed
		tracker.progress.Items[idx].Completed = tracker.progress.Items[idx].Total
		tracker.progress.Completed += remaining
	}
}

// effectiveEndDate returns the end date or today, whichever is earlier.
// Used to avoid processing future dates.
func effectiveEndDate(end time.Time) time.Time {
	if end.After(time.Now()) {
		return time.Now()
	}
	return end
}

// countDays returns the number of days between start and end (inclusive).
func countDays(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/config.HoursPerDay) + 1
	if days < 0 {
		return 0
	}
	return days
}

// countDaysInSeason returns the number of days from season start to min(season end, today).
func countDaysInSeason(season SeasonInfo) int {
	return countDays(season.StartDate, effectiveEndDate(season.EndDate))
}

// getDayConcurrency returns the configured day concurrency for parallel processing.
func getDayConcurrency() int {
	concurrency := viper.GetInt(config.FlagDayConcurrency)
	if concurrency <= 0 {
		return config.DefaultDayConcurrency
	}
	return concurrency
}

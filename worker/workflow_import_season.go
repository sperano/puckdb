package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonInput contains parameters for importing a single season.
type ImportSeasonInput struct {
	Season SeasonInfo
}

// ImportSeasonWorkflow imports all boxscores for a single season.
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func ImportSeasonWorkflow(ctx workflow.Context, input *ImportSeasonInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("ImportSeasonWorkflow started",
		"startYear", season.StartYear,
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat))

	// Set up progress tracking (days only for imports)
	total := countDaysInSeason(season)
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Determine end date (don't import future days)
	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	// Calculate number of days to process
	numDays := int(end.Sub(season.StartDate).Hours()/config.HoursPerDay) + 1
	if numDays < 0 {
		numDays = 0
	}

	// Get day concurrency from config
	dayConcurrency := viper.GetInt(config.FlagDayConcurrency)
	if dayConcurrency <= 0 {
		dayConcurrency = config.DefaultDayConcurrency
	}

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", dayConcurrency)

	// Process days in parallel using RunWorkerPool
	startDate := season.StartDate
	startYear := season.StartYear
	err := tracker.RunWorkerPool(ctx, numDays, dayConcurrency, func(ctx workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		dayInput := ImportBoxscoresForDateInput{
			Date:   day,
			Season: startYear,
		}
		return workflow.ExecuteActivity(ctx, ImportBoxscoresForDateActivity, dayInput)
	})
	if err != nil {
		return err
	}

	logger.Info("ImportSeasonWorkflow completed",
		"startYear", season.StartYear,
		"completed", tracker.progress.Completed)
	return nil
}

// WorkflowIDImportSeason returns the workflow ID for a single season import.
func WorkflowIDImportSeason(startYear int) string {
	return fmt.Sprintf("import-season-%d", startYear)
}

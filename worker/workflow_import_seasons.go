package worker

import (
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

const (
	// Mock import processes one "day" per tick, with a short sleep to simulate work
	mockImportTickDuration = 50 * time.Millisecond
)

// ImportSeasonsWorkflow imports season data into the database.
// Currently a mock implementation that simulates importing one day at a time.
func ImportSeasonsWorkflow(ctx workflow.Context, input *model.DownloadSeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	tracker := NewProgressTracker(0)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = 10
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

	initializeImportProgress(tracker, seasons)

	return processImportSeasons(ctx, logger, tracker, seasons, concurrency)
}

// initializeImportProgress sets up per-season progress for import.
// Each season has one step per day, matching the download workflow.
func initializeImportProgress(tracker *ProgressTracker, seasons []SeasonInfo) {
	total := 0
	items := make([]ItemProgress, len(seasons))

	for i, season := range seasons {
		days := countDaysInSeason(season)
		items[i] = ItemProgress{
			ID:          season.StartYear,
			Description: season.Label(),
			Total:       days,
			Completed:   0,
		}
		tracker.itemIndex[season.StartYear] = i
		total += days
	}

	tracker.progress = WorkflowProgress{
		Total:     total,
		Completed: 0,
		Items:     items,
	}
}

// importSeasonWork tracks a mock import for a single season.
type importSeasonWork struct {
	season    SeasonInfo
	day       int // current day being processed
	totalDays int // total days in this season
}

// processImportSeasons processes seasons with concurrency control.
// Each tick advances all active seasons by one day.
func processImportSeasons(ctx workflow.Context, logger log.Logger, tracker *ProgressTracker, seasons []SeasonInfo, concurrency int) error {
	if len(seasons) == 0 {
		return nil
	}

	active := make(map[int]*importSeasonWork)
	pending := make([]SeasonInfo, len(seasons))
	copy(pending, seasons)

	// Start initial batch
	for i := 0; i < concurrency && len(pending) > 0; i++ {
		season := pending[0]
		pending = pending[1:]
		startImportSeason(logger, tracker, active, season)
	}

	// Process until all complete
	for len(active) > 0 {
		// Sleep for one tick
		if err := workflow.Sleep(ctx, mockImportTickDuration); err != nil {
			return err
		}

		// Advance all active seasons by one day
		for year, work := range active {
			work.day++
			tracker.IncrementItem(year)

			if work.day >= work.totalDays {
				tracker.MarkItemCompleted(year)
				logger.Info("Season import completed", "startYear", year, "days", work.totalDays)
				delete(active, year)

				// Start next pending season
				if len(pending) > 0 {
					nextSeason := pending[0]
					pending = pending[1:]
					startImportSeason(logger, tracker, active, nextSeason)
				}
			}
		}
	}

	return nil
}

func startImportSeason(logger log.Logger, tracker *ProgressTracker, active map[int]*importSeasonWork, season SeasonInfo) {
	days := countDaysInSeason(season)
	logger.Info("Starting season import", "startYear", season.StartYear, "days", days)
	tracker.MarkItemStarted(season.StartYear)
	active[season.StartYear] = &importSeasonWork{
		season:    season,
		day:       0,
		totalDays: days,
	}
}

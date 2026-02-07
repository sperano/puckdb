package worker

import (
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonsWorkflow imports season data from cached boxscores into the database.
// It reads boxscore files from SimpleFS (previously downloaded) and upserts them
// into the nhl_games, nhl_game_skater_stats, and nhl_game_goalie_stats tables.
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

	// Extract and upsert any missing teams before importing boxscores
	if err := extractAndUpsertMissingTeams(ctx, logger, seasons); err != nil {
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

// processImportSeasons processes seasons sequentially, with each season's days processed in parallel.
// This matches the download workflow pattern where seasons are processed one at a time,
// but days within a season are processed with configurable concurrency.
func processImportSeasons(ctx workflow.Context, logger interface{ Info(string, ...interface{}) }, tracker *ProgressTracker, seasons []SeasonInfo, seasonConcurrency int) error {
	if len(seasons) == 0 {
		return nil
	}

	// Get day concurrency from config (same as download workflow)
	dayConcurrency := viper.GetInt(config.FlagDayConcurrency)
	if dayConcurrency <= 0 {
		dayConcurrency = 20
	}

	// Process each season
	for _, season := range seasons {
		tracker.MarkItemStarted(season.StartYear)

		if err := importSeasonBoxscores(ctx, logger, tracker, season, dayConcurrency); err != nil {
			return err
		}

		tracker.MarkItemCompleted(season.StartYear)
		logger.Info("Season import completed", "startYear", season.StartYear)
	}

	return nil
}

// importSeasonBoxscores imports all boxscores for a single season.
// Days are processed in parallel using the same concurrency pattern as the download workflow.
func importSeasonBoxscores(ctx workflow.Context, logger interface{ Info(string, ...interface{}) }, tracker *ProgressTracker, season SeasonInfo, dayConcurrency int) error {
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

	logger.Info("Processing days in parallel",
		"season", season.StartYear,
		"numDays", numDays,
		"concurrency", dayConcurrency)

	// Process days in parallel using RunWorkerPool (same pattern as download workflow)
	startDate := season.StartDate
	startYear := season.StartYear
	err := tracker.RunWorkerPool(ctx, numDays, dayConcurrency, func(ctx workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		input := ImportBoxscoresForDateInput{
			Date:   day,
			Season: startYear,
		}
		return workflow.ExecuteActivity(ctx, ImportBoxscoresForDateActivity, input)
	})

	return err
}

// extractAndUpsertMissingTeams scans all boxscores for teams and inserts any missing ones.
// This ensures historical teams (like Toronto Arenas) exist before importing games.
func extractAndUpsertMissingTeams(ctx workflow.Context, logger interface{ Info(string, ...interface{}) }, seasons []SeasonInfo) error {
	logger.Info("Extracting teams from boxscores", "seasons", len(seasons))

	// Extract all unique teams from boxscores across all seasons
	var extractedTeams []ExtractedTeam
	extractInput := ExtractTeamsForSeasonsInput{Seasons: seasons}
	if err := workflow.ExecuteActivity(ctx, ExtractTeamsForSeasonsActivity, extractInput).Get(ctx, &extractedTeams); err != nil {
		return err
	}

	logger.Info("Extracted teams from boxscores", "unique_teams", len(extractedTeams))

	// Upsert any missing teams (existing teams are skipped)
	var result UpsertMissingTeamsResult
	if err := workflow.ExecuteActivity(ctx, UpsertMissingTeamsActivity, extractedTeams).Get(ctx, &result); err != nil {
		return err
	}

	logger.Info("Team upsert complete",
		"inserted", result.TeamsInserted,
		"skipped", result.TeamsSkipped)

	return nil
}

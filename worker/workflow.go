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
	defaultTimeout               = 30 * time.Minute
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

/*
func WorkflowIDImportRostersForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("import-rosters-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
}

func WorkflowIDImportTeamSummariesForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("import-team-summary-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
}
*/

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
		WorkflowExecutionTimeout: defaultTimeout,
		WorkflowTaskTimeout:      defaultTimeout,
		WorkflowID:               id,
	})
}

func defaultActivityOptions() workflow.ActivityOptions {
	initialInterval := viper.GetInt(config.FlagTemporalRetryInitialInterval)
	maxInterval := viper.GetInt(config.FlagTemporalRetryMaxInterval)
	maxAttempts := viper.GetInt32(config.FlagTemporalRetryMaxAttempts)
	return workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(initialInterval) * time.Second,
			MaximumInterval:    time.Duration(maxInterval) * time.Second,
			MaximumAttempts:    maxAttempts,
			BackoffCoefficient: 2.0,
		},
	}
}

const defaultSeasonConcurrency = 3

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

	concurrency := defaultSeasonConcurrency
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
	tracker.MarkSeasonStarted(season.StartYear)
	ctxo := withChildOptions(ctx, WorkflowIDDownloadSeason(season.StartYear))
	future := workflow.ExecuteChildWorkflow(ctxo, DownloadSeasonWorkflow, &DownloadSeasonInput{Season: season})
	active[season.StartYear] = &childWorkflowWork{
		season: season,
		future: future,
	}
}

// markSeasonComplete sets all tasks for a season as completed in the tracker
func markSeasonComplete(tracker *ProgressTracker, startYear int) {
	if idx, ok := tracker.seasonIndex[startYear]; ok {
		remaining := tracker.progress.Seasons[idx].Total - tracker.progress.Seasons[idx].Completed
		tracker.progress.Seasons[idx].Completed = tracker.progress.Seasons[idx].Total
		tracker.progress.Completed += remaining
	}
}

/*
func DownloadEverythingWorkflow(ctx workflow.Context) error {
	log.Info().Msg("DownloadFromYahoo everything!")
	seasons, err := config.GetYahooSeasonsConfig()
	if err != nil {
		return err
	}

	total := countDownloadTasks(seasons)
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	futures := collectDownloadFutures(ctx, seasons)

	return tracker.WaitAll(ctx, futures)
}
*/

/*
func DownloadEverythingForSeasonWorkflow(ctx workflow.Context, season config.Season) error {
	log.Info().Int("season", season.StartYear()).Msg("DownloadFromYahoo everything for season!")

	total := countDownloadTasksForSeason(season)
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	futures := collectDownloadFuturesForSeason(ctx, season)

	return tracker.WaitAll(ctx, futures)
}
*/

// countDaysInSeason returns the number of days from season start to min(season end, today).
func countDaysInSeason(season SeasonInfo) int {
	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}
	days := int(end.Sub(season.StartDate).Hours()/24) + 1
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

/*
// countDownloadTasks counts the total number of download tasks across all seasons.
func countDownloadTasks(seasons config.Seasons) int {
	total := 0
	for _, season := range seasons {
		total += countDownloadTasksForSeason(season)
	}
	return total
}
*/

/*
// collectDownloadFutures collects all download futures across all seasons.
func collectDownloadFutures(ctx workflow.Context, seasons config.Seasons) []workflow.Future {
	futures := make([]workflow.Future, 0, countDownloadTasks(seasons))
	for _, season := range seasons {
		futures = append(futures, collectDownloadFuturesForSeason(ctx, season)...)
	}
	return futures
}
*/

/*
func ImportEverythingWorkflow(ctx workflow.Context) error {
	log.Info().Msg("Import everything!")
	seasons, err := config.GetYahooSeasonsConfig()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	futures := make([]workflow.Future, 0)
	for _, season := range seasons {
		ctxo := withChildOptions(ctx, WorkflowIDImportGamesForSeason(season.StartYear()))
		future := workflow.ExecuteChildWorkflow(ctxo, ImportGamesForSeasonWorkflow, season)
		futures = append(futures, future)
		for _, league := range season.Leagues {
			ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
			future := workflow.ExecuteActivity(ctxa, ImportLeague, season.StartYear(), season.GameKey, league.LeagueID)
			futures = append(futures, future)
			for _, teamid := range league.TeamIDs {
				future := workflow.ExecuteActivity(ctxa, ImportTeam, season.StartYear(), season.GameKey, league.LeagueID, teamid)
				futures = append(futures, future)
				ctxo := withChildOptions(ctx, WorkflowIDImportRostersForTeam(season, league, teamid))
				future = workflow.ExecuteChildWorkflow(ctxo, ImportRosterForTeamWorkflow, season, league, teamid)
				futures = append(futures, future)
				ctxo = withChildOptions(ctx, WorkflowIDImportTeamSummariesForTeam(season, league, teamid))
				future = workflow.ExecuteChildWorkflow(ctxo, ImportTeamSummariesForTeamWorkflow, season, league, teamid)
				futures = append(futures, future)
			}
		}
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
*/

/*
func ImportEverythingForSeasonWorkflow(ctx workflow.Context, season config.Season) error {
	log.Info().Int("season", season.StartYear()).Msg("Import everything for season!")
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	futures := make([]workflow.Future, 0)

	ctxo := withChildOptions(ctx, WorkflowIDImportGamesForSeason(season.StartYear()))
	future := workflow.ExecuteChildWorkflow(ctxo, ImportGamesForSeasonWorkflow, season)
	futures = append(futures, future)

	for _, league := range season.Leagues {
		ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctxa, ImportLeague, season.StartYear(), season.GameKey, league.LeagueID)
		futures = append(futures, future)
		for _, teamid := range league.TeamIDs {
			future := workflow.ExecuteActivity(ctxa, ImportTeam, season.StartYear(), season.GameKey, league.LeagueID, teamid)
			futures = append(futures, future)
			ctxo := withChildOptions(ctx, WorkflowIDImportRostersForTeam(season, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, ImportRosterForTeamWorkflow, season, league, teamid)
			futures = append(futures, future)
			ctxo = withChildOptions(ctx, WorkflowIDImportTeamSummariesForTeam(season, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, ImportTeamSummariesForTeamWorkflow, season, league, teamid)
			futures = append(futures, future)
		}
	}

	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
*/

//func ImportTeams(ctx workflow.Context, force bool) (string, error) {
//	return "", config.ErrNotImplementedYet
//	//ctx = workflow.WithActivityOptions(ctx, defaultOptions())
//	//var futures []workflow.Future
//	//teamIDs := core.GetTeamIDs()
//	//for _, id := range teamIDs {
//	//	f := workflow.ExecuteActivity(ctx, ImportTeamActivity, id, force)
//	//	futures = append(futures, f)
//	//}
//	//// wait until all teams are imported
//	//for _, future := range futures {
//	//	if err := future.Get(ctx, nil); err != nil {
//	//		return "", err
//	//	}
//	//}
//	//return fmt.Sprintf("imported %s", english.Plural(len(teamIDs), "team", "")), nil
//}

//func ImportGamesOnDay(ctx workflow.Context, day time.Time, force bool) (string, error) {
//	log.Info().Str("date", date.ToYearString(day)).Bool("force", force).Msg("ImportGamesOnDay")
//	ctx = workflow.WithActivityOptions(ctx, defaultOptions())
//	var result []string
//	if err := workflow.ExecuteActivity(ctx, EnsureGamesListOnDayActivity, day, force).Get(ctx, &result); err != nil {
//		return "", err
//	}
//	var futures []workflow.Future
//	for _, gameLink := range result {
//		f := workflow.ExecuteActivity(ctx, ImportGameActivity, day, gameLink, force)
//		futures = append(futures, f)
//	}
//	for _, future := range futures {
//		if err := future.Get(ctx, nil); err != nil {
//			return "", err
//		}
//	}
//	return fmt.Sprintf("imported games for day %s", day), nil
//}

//func startEndRange(season *config.Season, start time.Time, end time.Time) (date.DateRange, error) {
//	if start.IsZero() {
//		start = season.Start.ToStdTime()
//	}
//	if end.IsZero() {
//		start = season.End.ToStdTime()
//	}
//	return date.DateRangeToToday(start, end, time.Now())
//}

//func ImportGamesDateRange(ctx workflow.Context, season *config.Season, start time.Time, end time.Time, force bool) (string, error) {
//	log.Info().Str("start", date.ToYearString(start)).Str("end", date.ToYearString(end)).
//		Bool("force", force).Msg("ImportGames")
//	dateRange, err := startEndRange(season, start, end)
//	if err != nil {
//		return "", err
//	}
//	ctx = workflow.WithActivityOptions(ctx, defaultOptions())
//	var futures []workflow.Future
//	for _, date := range dateRange {
//		f := workflow.ExecuteChildWorkflow(ctx, ImportGamesOnDay, date, force)
//		futures = append(futures, f)
//	}
//	for _, future := range futures {
//		if err := future.Get(ctx, nil); err != nil {
//			return "", err
//		}
//	}
//	return fmt.Sprintf("imported games"), nil
//}

//func commonAllWithTeamID(ctx workflow.Context, season *config.Season, workflowFunc interface{}, start time.Time, end time.Time, teamID uint, force bool) (string, error) {
//	log.Info().Str("start", date.ToYearString(start)).Str("end", date.ToYearString(end)).
//		Uint("teamID", teamID).Bool("force", force).Msgf("%v", workflowFunc)
//	dateRange, err := startEndRange(season, start, end)
//	if err != nil {
//		return "", err
//	}
//	ctx = workflow.WithActivityOptions(ctx, defaultOptions())
//	var futures []workflow.Future
//	for _, date := range dateRange {
//		f := workflow.ExecuteChildWorkflow(ctx, workflowFunc, date, teamID, force)
//		futures = append(futures, f)
//	}
//	for _, future := range futures {
//		if err := future.Get(ctx, nil); err != nil {
//			return "", err
//		}
//	}
//	return fmt.Sprintf("imported %v", workflowFunc), nil
//}

//func ImportRosterPlayersOnDay(ctx workflow.Context, day time.Time, teamID uint, force bool) (string, error) {
//	log.Info().Str("date", date.ToYearString(day)).Uint("teamID", teamID).Bool("force", force).
//		Msg("ImportRosterPlayersOnDay")
//	if teamID == 0 {
//		results, err := commonWorkflowForAllTeams(ctx, ImportRosterPlayersForTeamOnDayActivity, day, force)
//		if err != nil {
//			return "", err
//		}
//		return strings.Join(results, ", "), nil
//	}
//	return commonWorkflowSingle(ctx, ImportRosterPlayersForTeamOnDayActivity, teamID, day, force)
//}

//func executeChildWorkflowTeamIDsAndRange(ctx workflow.Context, childWorkflow any, objectName string, start time.Time, end time.Time, force bool) (string, error) {
//	return "nil", fmt.Errorf("NOOT implemented yetttt")
//	//dateRange, err := getStartEndRange(core.GlobalYFH(), start, end)
//	//if err != nil {
//	//	return "", err
//	//}
//	//ctx = workflow.WithActivityOptions(ctx, defaultOptions())
//	//var futures []workflow.Future
//	//for _, date := range *dateRange {
//	//	for _, teamID := range core.GetTeamIDs() {
//	//		f := workflow.ExecuteChildWorkflow(ctx, childWorkflow, date, teamID, force)
//	//		futures = append(futures, f)
//	//	}
//	//}
//	//for _, future := range futures {
//	//	if err := future.Get(ctx, nil); err != nil {
//	//		return "", err
//	//	}
//	//}
//	//return fmt.Sprintf("imported %s for all teams start=%s end=%s",
//	//	objectName,
//	//	model.NewReadableTSFromShortTime((*dateRange)[0]).ShortString(),
//	//	model.NewReadableTSFromShortTime((*dateRange)[len(*dateRange)-1]).ShortString()), nil
//}

//func ImportRosterPlayersDateRange(ctx workflow.Context, season *config.Season, start time.Time, end time.Time, teamID uint, force bool) (string, error) {
//	log.Info().Str("start", date.ToYearString(start)).Str("end", date.ToYearString(end)).
//		Uint("teamID", teamID).Bool("force", force).
//		Msg("ImportRosterPlayersDateRange")
//	if teamID == 0 {
//		return executeChildWorkflowTeamIDsAndRange(ctx, ImportRosterPlayersOnDay, "Roster Player(s)", start, end, force)
//	}
//	return commonAllWithTeamID(ctx, season, ImportRosterPlayersOnDay, start, end, teamID, force)
//}

//func ImportTeamSummariesOnDay(ctx workflow.Context, day time.Time, teamID uint, force bool) (string, error) {
//	log.Info().Str("date", date.ToYearString(day)).Uint("teamID", teamID).Bool("force", force).
//		Msg("ImportTeamSummariesOnDay")
//	if teamID == 0 {
//		results, err := commonWorkflowForAllTeams(ctx, ImportTeamSummaryForTeamOnDayActivity, day, force)
//		if err != nil {
//			return "", err
//		}
//		return strings.Join(results, ", "), nil
//	}
//	return commonWorkflowSingle(ctx, ImportTeamSummaryForTeamOnDayActivity, teamID, day, force)
//}

//func ImportTeamSummariesDateRange(ctx workflow.Context, season *config.Season, start time.Time, end time.Time, teamID uint, force bool) (string, error) {
//	log.Info().Str("start", date.ToYearString(start)).Str("end", date.ToYearString(end)).
//		Uint("teamID", teamID).Bool("force", force).Msg("ImportTeamSummariesDateRange")
//	if teamID == 0 {
//		return executeChildWorkflowTeamIDsAndRange(ctx, ImportTeamSummariesOnDay, "Team Summaries", start, end, force)
//	}
//	return commonAllWithTeamID(ctx, season, ImportTeamSummariesOnDay, start, end, teamID, force)
//}

//func ImportAllOnDay(ctx workflow.Context, day time.Time, force bool) (string, error) {
//	log.Info().Str("date", date.ToYearString(day)).Bool("force", force).Msg("ImportAllOnDay")
//	var results []string
//	s, err := ImportGamesOnDay(ctx, day, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	//
//	s, err = ImportRosterPlayersOnDay(ctx, day, 0, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	//
//	s, err = ImportTeamSummariesOnDay(ctx, day, 0, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	return strings.Join(results, ", "), nil
//}

//func ImportAllDateRange(ctx workflow.Context, season *config.Season, start time.Time, end time.Time, force bool) (string, error) {
//	log.Info().Str("start", date.ToYearString(start)).Str("end", date.ToYearString(end)).
//		Bool("force", force).Msg("ImportAllDateRange")
//	var results []string
//	s, err := ImportGamesDateRange(ctx, season, start, end, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	//
//	s, err = ImportRosterPlayersDateRange(ctx, season, start, end, 0, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	//
//	s, err = ImportTeamSummariesDateRange(ctx, season, start, end, 0, force)
//	if err != nil {
//		return "", err
//	}
//	results = append(results, s)
//	return strings.Join(results, ", "), nil
//}

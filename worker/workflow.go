package worker

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/date"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	TaskQueueName                = "yfh-tasks"
	defaultTimeout               = 30 * time.Minute
	WorkflowIDImportEverything   = "import-everything"
	WorkflowIDDownloadEverything = "download-everything"
)

func WorkflowIDImportLeague(season int, leagueID int) string {
	return fmt.Sprintf("league-%d-%d", season, leagueID)
}

func WorkflowIDImportTeam(season int, leagueID int, teamID int) string {
	return fmt.Sprintf("team-%d-%d", season, leagueID)
}

func WorkflowIDImportGamesForDay(year int, month int, day int) string {
	return fmt.Sprintf("import-games-for-day-%d-%d-%d", year, month, day)
}

func WorkflowIDImportGamesForSeason(season int) string {
	return fmt.Sprintf("import-games-for-season-%d", season)
}

func WorkflowIDImportRostersForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("import-rosters-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
}

func WorkflowIDImportTeamSummariesForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("import-team-summary-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
}

func WorkflowIDDownloadGamesForSeason(season int) string {
	return fmt.Sprintf("download-games-for-season-%d", season)
}

func WorkflowIDDownloadRostersForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("download-rosters-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
}

func WorkflowIDDownloadTeamSummariesForTeam(season config.Season, league config.League, teamid int) string {
	return fmt.Sprintf("download-team-summary-%d-%d-%d", season.StartYear(), league.LeagueID, teamid)
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
	maxAttempts := viper.GetInt32(config.FlagTemporalRetryMaxAttempts)
	return workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Duration(initialInterval) * time.Second,
			MaximumAttempts: maxAttempts,
		},
	}
}

func getGameKey(season int) (int, error) {
	seasons, err := config.GetSeasonsConfig()
	if err != nil {
		return 0, err
	}
	seasonObj, err := seasons.Get(season)
	if err != nil {
		return 0, err
	}
	return seasonObj.GameKey, nil
}

func DownloadEverythingWorkflow(ctx workflow.Context) error {
	log.Info().Msg("DownloadFromYahoo everything!")
	seasons, err := config.GetSeasonsConfig()
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

// countDaysInSeason returns the number of days from season start to min(season end, today).
func countDaysInSeason(season config.Season) int {
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}
	days := int(end.Sub(season.Start).Hours()/24) + 1
	if days < 0 {
		return 0
	}
	return days
}

// countDownloadTasksForSeason counts the total number of download tasks for a single season.
func countDownloadTasksForSeason(season config.Season) int {
	activitiesPerDay := 2 // DownloadGameDay + DownloadDailySchedule
	count := countDaysInSeason(season) * activitiesPerDay
	for _, league := range season.Leagues {
		count++ // DownloadLeague
		count += len(league.TeamIDs) * 3 // DownloadTeam + 2 child workflows per team
	}
	return count
}

// countDownloadTasks counts the total number of download tasks across all seasons.
func countDownloadTasks(seasons config.Seasons) int {
	total := 0
	for _, season := range seasons {
		total += countDownloadTasksForSeason(season)
	}
	return total
}

// collectDownloadFuturesForSeason collects all download futures for a single season.
func collectDownloadFuturesForSeason(ctx workflow.Context, season config.Season) []workflow.Future {
	futures := make([]workflow.Future, 0, countDownloadTasksForSeason(season))

	// Spawn per-day activities for granular progress tracking
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err == nil {
		for _, day := range dateRange {
			ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
			futures = append(futures, workflow.ExecuteActivity(ctxa, DownloadGameDay, day))
			futures = append(futures, workflow.ExecuteActivity(ctxa, DownloadDailySchedule, day))
		}
	}

	for _, league := range season.Leagues {
		ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctxa, DownloadLeague, season.StartYear(), season.GameKey, league.LeagueID)
		futures = append(futures, future)
		for _, teamid := range league.TeamIDs {
			future := workflow.ExecuteActivity(ctxa, DownloadTeam, season.StartYear(), season.GameKey, league.LeagueID, teamid)
			futures = append(futures, future)
			ctxo := withChildOptions(ctx, WorkflowIDDownloadRostersForTeam(season, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, DownloadRosterForTeamWorkflow, season, league, teamid)
			futures = append(futures, future)
			ctxo = withChildOptions(ctx, WorkflowIDDownloadTeamSummariesForTeam(season, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, DownloadTeamSummariesForTeamWorkflow, season, league, teamid)
			futures = append(futures, future)
		}
	}

	return futures
}

// collectDownloadFutures collects all download futures across all seasons.
func collectDownloadFutures(ctx workflow.Context, seasons config.Seasons) []workflow.Future {
	futures := make([]workflow.Future, 0, countDownloadTasks(seasons))
	for _, season := range seasons {
		futures = append(futures, collectDownloadFuturesForSeason(ctx, season)...)
	}
	return futures
}

func ImportEverythingWorkflow(ctx workflow.Context) error {
	log.Info().Msg("Import everything!")
	seasons, err := config.GetSeasonsConfig()
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

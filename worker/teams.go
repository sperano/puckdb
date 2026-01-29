package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
)

func DownloadTeam(ctx context.Context, season int, leagueID int, teamID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadTeam", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := cache.NewSimpleCache()
	return downloadTeamImpl(ctx, fs, season, gameKey, leagueID, teamID)
}

// downloadTeamImpl is the testable implementation.
func downloadTeamImpl(ctx context.Context, fs cache.FileSystem, season int, gameKey int, leagueID int, teamID int) error {
	log.Info().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo Team")
	file := fs.New(cache.TeamFileType, season, leagueID, teamID)
	url := http.YahooTeamURL(gameKey, leagueID, teamID)
	return doDownloadImpl(ctx, fs, file, url)
}

func ImportTeam(ctx context.Context, season int, leagueID int, teamID int) (database.Team, error) {
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return database.Team{}, err
	}
	fs := cache.NewSimpleCache()
	return importTeamImpl(ctx, fs, season, gameKey, leagueID, teamID)
}

// importTeamImpl is the testable implementation.
func importTeamImpl(ctx context.Context, fs cache.FileSystem, season int, gameKey int, leagueID int, teamID int) (database.Team, error) {
	log.Info().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Import Team")
	file := fs.New(cache.TeamFileType, season, leagueID, teamID)
	url := http.YahooTeamURL(gameKey, leagueID, teamID)
	t, err := doImportImpl(ctx, fs, file, url, cache.GetTeamXMLModel)
	if err != nil {
		return database.Team{}, err
	}
	if t == nil {
		return database.Team{}, nil
	}
	return *t, err
}

/*
func DownloadRosterForTeamWorkflow(ctx workflow.Context, startDate, endDate time.Time, season, leagueID, teamID int) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Downloading rosters", "season", season, "leagueID", leagueID, "teamID", teamID)

	end := endDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	futures := make([]workflow.Future, 0)
	for day := startDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, DownloadRosterForTeamOnDay, season, leagueID, teamID, day)
		futures = append(futures, future)
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
func ImportRosterForTeamWorkflow(ctx workflow.Context, startDate, endDate time.Time, gameKey, leagueID, teamID int) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Importing rosters", "gameKey", gameKey, "leagueID", leagueID, "teamID", teamID)

	end := endDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	futures := make([]workflow.Future, 0)
	for day := startDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, ImportRosterForTeamOnDay, gameKey, leagueID, teamID, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
*/

func DownloadRosterForTeamOnDay(ctx context.Context, season int, leagueID int, teamID int, day time.Time) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadRosterForTeamOnDay", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := cache.NewSimpleCache()
	return downloadRosterForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// downloadRosterForTeamOnDayImpl is the testable implementation.
func downloadRosterForTeamOnDayImpl(ctx context.Context, fs cache.FileSystem, gameKey int, leagueID int, teamID int, day time.Time) error {
	log.Info().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo roster")
	file := fs.New(cache.RosterFileType, day, leagueID, teamID)
	url := http.YahooRosterURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

func ImportRosterForTeamOnDay(ctx context.Context, season int, leagueID int, teamID int, day time.Time) (database.RosterPlayers, error) {
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return nil, err
	}
	fs := cache.NewSimpleCache()
	return importRosterForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// importRosterForTeamOnDayImpl is the testable implementation.
func importRosterForTeamOnDayImpl(ctx context.Context, fs cache.FileSystem, gameKey int, leagueID int, teamID int, day time.Time) (database.RosterPlayers, error) {
	log.Info().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Import roster")
	file := fs.New(cache.RosterFileType, day, leagueID, teamID)
	url := http.YahooRosterURL(gameKey, leagueID, teamID, day)
	r, err := doImportImpl(ctx, fs, file, url, cache.GetRosterXMLModel)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r, err
}

/*
func DownloadTeamSummariesForTeamWorkflow(ctx workflow.Context, startDate, endDate time.Time, season, leagueID, teamID int) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Downloading team summaries", "season", season, "leagueID", leagueID, "teamID", teamID)

	end := endDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	futures := make([]workflow.Future, 0)
	for day := startDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, DownloadTeamSummaryForTeamOnDay, season, leagueID, teamID, day)
		futures = append(futures, future)
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
func ImportTeamSummariesForTeamWorkflow(ctx workflow.Context, startDate, endDate time.Time, gameKey, leagueID, teamID int) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Importing team summaries", "gameKey", gameKey, "leagueID", leagueID, "teamID", teamID)

	end := endDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	futures := make([]workflow.Future, 0)
	for day := startDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, ImportTeamSummaryForTeamOnDay, gameKey, leagueID, teamID, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
*/

func DownloadTeamSummaryForTeamOnDay(ctx context.Context, season int, leagueID int, teamID int, day time.Time) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadTeamSummaryForTeamOnDay", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := cache.NewSimpleCache()
	return downloadTeamSummaryForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// downloadTeamSummaryForTeamOnDayImpl is the testable implementation.
func downloadTeamSummaryForTeamOnDayImpl(ctx context.Context, fs cache.FileSystem, gameKey int, leagueID int, teamID int, day time.Time) error {
	log.Info().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo team summary")
	file := fs.New(cache.TeamSummaryFileType, day, leagueID, teamID)
	url := http.YahooTeamSummaryURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

func ImportTeamSummaryForTeamOnDay(ctx context.Context, season int, leagueID int, teamID int, day time.Time) (database.TeamSummary, error) {
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return database.TeamSummary{}, err
	}
	fs := cache.NewSimpleCache()
	return importTeamSummaryForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// importTeamSummaryForTeamOnDayImpl is the testable implementation.
func importTeamSummaryForTeamOnDayImpl(ctx context.Context, fs cache.FileSystem, gameKey int, leagueID int, teamID int, day time.Time) (database.TeamSummary, error) {
	log.Info().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Import team summary")
	file := fs.New(cache.TeamSummaryFileType, day, leagueID, teamID)
	url := http.YahooTeamSummaryURL(gameKey, leagueID, teamID, day)
	ts, err := doImportImpl(ctx, fs, file, url, cache.GetTeamSummaryXMLModel)
	if err != nil {
		return database.TeamSummary{}, err
	}
	if ts == nil {
		return database.TeamSummary{}, nil
	}
	return *ts, err
}

/*
func ImportTeamWorkflow(ctx workflow.Context, season int, leagueID int, teamID int) (database.Team, error) {
	var team database.Team
	gkey, err := getGameKey(season)
	if err != nil {
		return team, err
	}
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	err = workflow.ExecuteActivity(ctx, ImportTeam, season, gkey, leagueID, teamID).Get(ctx, &team)
	return team, err
}
*/

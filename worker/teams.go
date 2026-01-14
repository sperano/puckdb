package worker

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/cache"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/database"
	"github.com/sperano/yfh/date"
	"github.com/sperano/yfh/http"
	"go.temporal.io/sdk/workflow"
	"time"
)

func DownloadTeam(ctx context.Context, season int, gameKey int, leagueID int, teamID int) error {
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

func ImportTeam(ctx context.Context, season int, gameKey int, leagueID int, teamID int) (database.Team, error) {
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

func DownloadRosterForTeamWorkflow(ctx workflow.Context, season config.Season, league config.League, teamid int) error {
	log.Info().Int("season", season.StartYear()).Int("leagueID", league.LeagueID).Int("teamID", teamid).Msg("Downloading rosters")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, DownloadRosterForTeamOnDay, season.GameKey, league.LeagueID, teamid, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func ImportRosterForTeamWorkflow(ctx workflow.Context, season config.Season, league config.League, teamid int) error {
	log.Info().Int("season", season.StartYear()).Int("leagueID", league.LeagueID).Int("teamID", teamid).Msg("Importing rosters")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, ImportRosterForTeamOnDay, season.GameKey, league.LeagueID, teamid, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func DownloadRosterForTeamOnDay(ctx context.Context, gameKey int, leagueID int, teamID int, day time.Time) error {
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

func ImportRosterForTeamOnDay(ctx context.Context, gameKey int, leagueID int, teamID int, day time.Time) (database.RosterPlayers, error) {
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

func DownloadTeamSummariesForTeamWorkflow(ctx workflow.Context, season config.Season, league config.League, teamid int) error {
	log.Info().Int("season", season.StartYear()).Int("leagueID", league.LeagueID).Int("teamID", teamid).Msg("Downloading team summaries")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, DownloadTeamSummaryForTeamOnDay, season.GameKey, league.LeagueID, teamid, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func ImportTeamSummariesForTeamWorkflow(ctx workflow.Context, season config.Season, league config.League, teamid int) error {
	log.Info().Int("season", season.StartYear()).Int("leagueID", league.LeagueID).Int("teamID", teamid).Msg("Importing team summaries")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, ImportTeamSummaryForTeamOnDay, season.GameKey, league.LeagueID, teamid, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func DownloadTeamSummaryForTeamOnDay(ctx context.Context, gameKey int, leagueID int, teamID int, day time.Time) error {
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

func ImportTeamSummaryForTeamOnDay(ctx context.Context, gameKey int, leagueID int, teamID int, day time.Time) (database.TeamSummary, error) {
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

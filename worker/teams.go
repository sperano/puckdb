package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
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
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo Team")
	file := cache.TeamFile{Season: season, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamURL(gameKey, leagueID, teamID)
	return doDownloadImpl(ctx, fs, file, url)
}

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
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo roster")
	file := cache.RosterFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooRosterURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

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
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("DownloadFromYahoo team summary")
	file := cache.TeamSummaryFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamSummaryURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

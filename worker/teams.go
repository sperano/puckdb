package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
)

func FetchTeamActivity(ctx context.Context, season int, leagueID int, teamID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchTeamActivity", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := store.NewStore()
	return fetchTeamImpl(ctx, fs, season, gameKey, leagueID, teamID)
}

// fetchTeamImpl is the testable implementation.
func fetchTeamImpl(ctx context.Context, fs store.Store, season int, gameKey int, leagueID int, teamID int) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo Team")
	file := store.TeamFile{Season: season, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamURL(gameKey, leagueID, teamID)
	return doDownloadImpl(ctx, fs, file, url)
}

func FetchRosterForTeamOnDayActivity(ctx context.Context, season int, leagueID int, teamID int, day time.Time) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchRosterForTeamOnDayActivity", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := store.NewStore()
	return fetchRosterForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// fetchRosterForTeamOnDayImpl is the testable implementation.
func fetchRosterForTeamOnDayImpl(ctx context.Context, fs store.Store, gameKey int, leagueID int, teamID int, day time.Time) error {
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo roster")
	file := store.RosterFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooRosterURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

func FetchTeamSummaryForTeamOnDayActivity(ctx context.Context, season int, leagueID int, teamID int, day time.Time) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchTeamSummaryForTeamOnDayActivity", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := store.NewStore()
	return fetchTeamSummaryForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day)
}

// fetchTeamSummaryForTeamOnDayImpl is the testable implementation.
func fetchTeamSummaryForTeamOnDayImpl(ctx context.Context, fs store.Store, gameKey int, leagueID int, teamID int, day time.Time) error {
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo team summary")
	file := store.TeamSummaryFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamSummaryURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

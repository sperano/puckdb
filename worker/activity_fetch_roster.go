package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// FetchRosterForTeamOnDayActivity downloads a Yahoo fantasy roster for a team on a specific day.
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

func fetchRosterForTeamOnDayImpl(ctx context.Context, fs store.Store, gameKey int, leagueID int, teamID int, day time.Time) error {
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo roster")
	file := store.RosterFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooRosterURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url)
}

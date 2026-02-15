package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// FetchTeamSummaryForTeamOnDayActivity downloads a Yahoo fantasy team summary for a specific day.
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
	return fetchTeamSummaryForTeamOnDayImpl(ctx, fs, gameKey, leagueID, teamID, day, DownloadFromYahoo)
}

func fetchTeamSummaryForTeamOnDayImpl(ctx context.Context, fs store.Store, gameKey int, leagueID int, teamID int, day time.Time, download Downloader) error {
	log.Trace().Time("day", day).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo team summary")
	file := store.TeamSummaryFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamSummaryURL(gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, fs, file, url, download)
}

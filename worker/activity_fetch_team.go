package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// FetchTeamActivity downloads a Yahoo fantasy team page.
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
	return fetchTeamImpl(ctx, fs, season, gameKey, leagueID, teamID, DownloadFromYahoo)
}

func fetchTeamImpl(ctx context.Context, fs store.Store, season int, gameKey int, leagueID int, teamID int, download Downloader) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo Team")
	file := store.TeamFile{Season: season, LeagueID: leagueID, TeamID: teamID}
	url := http.YahooTeamURL(gameKey, leagueID, teamID)
	return doDownloadImpl(ctx, fs, file, url, download)
}

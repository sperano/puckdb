package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/urls"
)

func FetchLeagueActivity(ctx context.Context, season int, leagueID int) error {
	defer metrics.TrackActivityDuration("FetchLeagueActivity")()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := store.NewStore()
	return fetchLeagueImpl(ctx, fs, season, gameKey, leagueID, DownloadFromYahoo)
}

// fetchLeagueImpl is the testable implementation.
func fetchLeagueImpl(ctx context.Context, fs store.Store, season int, gameKey int, leagueID int, download Downloader) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Msg("Fetching Yahoo League")
	file := store.LeagueFile{Season: season, LeagueID: leagueID}
	url := urls.YahooLeagueURL(gameKey, leagueID)
	return doDownloadImpl(ctx, fs, file, url, download)
}

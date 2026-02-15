package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

func FetchLeagueActivity(ctx context.Context, season int, leagueID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchLeagueActivity", time.Since(start))
	}()
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
	url := http.YahooLeagueURL(gameKey, leagueID)
	return doDownloadImpl(ctx, fs, file, url, download)
}

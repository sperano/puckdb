package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
)

func DownloadLeague(ctx context.Context, season int, leagueID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadLeague", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := cache.NewSimpleCache()
	return downloadLeagueImpl(ctx, fs, season, gameKey, leagueID)
}

// downloadLeagueImpl is the testable implementation.
func downloadLeagueImpl(ctx context.Context, fs cache.FileSystem, season int, gameKey int, leagueID int) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Msg("DownloadFromYahoo League")
	file := fs.New(cache.LeagueFileType, season, leagueID)
	url := http.YahooLeagueURL(gameKey, leagueID)
	return doDownloadImpl(ctx, fs, file, url)
}

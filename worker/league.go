package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
)

func FetchLeague(ctx context.Context, season int, leagueID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchLeague", time.Since(start))
	}()
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}
	fs := cache.NewSimpleCache()
	return fetchLeagueImpl(ctx, fs, season, gameKey, leagueID)
}

// fetchLeagueImpl is the testable implementation.
func fetchLeagueImpl(ctx context.Context, fs cache.FileSystem, season int, gameKey int, leagueID int) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Msg("Fetching Yahoo League")
	file := cache.LeagueFile{Season: season, LeagueID: leagueID}
	url := http.YahooLeagueURL(gameKey, leagueID)
	return doDownloadImpl(ctx, fs, file, url)
}

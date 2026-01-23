package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
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
	log.Info().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Msg("DownloadFromYahoo League")
	file := fs.New(cache.LeagueFileType, season, leagueID)
	url := http.YahooLeagueURL(gameKey, leagueID)
	return doDownloadImpl(ctx, fs, file, url)
}

func ImportLeague(ctx context.Context, season int, leagueID int) (database.League, error) {
	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return database.League{}, err
	}
	fs := cache.NewSimpleCache()
	return importLeagueImpl(ctx, fs, season, gameKey, leagueID)
}

// importLeagueImpl is the testable implementation.
func importLeagueImpl(ctx context.Context, fs cache.FileSystem, season int, gameKey int, leagueID int) (database.League, error) {
	log.Info().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Msg("Import League")
	file := fs.New(cache.LeagueFileType, season, leagueID)
	url := http.YahooLeagueURL(gameKey, leagueID)
	l, err := doImportImpl(ctx, fs, file, url, cache.GetLeagueXMLModel)
	if err != nil {
		return database.League{}, err
	}
	if l == nil {
		return database.League{}, nil
	}
	return *l, err
}

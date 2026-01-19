package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/http"
)

/*
func ImportLeagueWorkflow(ctx workflow.Context, season int, leagueID int) (database.League, error) {
	var league database.League
	gkey, err := getGameKey(season)
	if err != nil {
		return league, err
	}
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	err = workflow.ExecuteActivity(ctx, ImportLeague, season, gkey, leagueID).Get(ctx, &league)
	return league, err
}
*/

func DownloadLeague(ctx context.Context, season int, gameKey int, leagueID int) error {
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

func ImportLeague(ctx context.Context, season int, gameKey int, leagueID int) (database.League, error) {
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

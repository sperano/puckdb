package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
)

// LoadYahooIDPoolActivity loads all Yahoo players from cached HTML files into Redis.
// This is Phase 1 of the import workflow - it builds the pool of available Yahoo IDs.
func LoadYahooIDPoolActivity(ctx context.Context) (int, error) {
	fs := cache.NewSimpleCache().(*cache.InstrumentedFS).Inner()
	return loadYahooIDPoolImpl(ctx, fs, redis.NewClient())
}

func loadYahooIDPoolImpl(ctx context.Context, fs *cache.SimpleFS, redisClient redis.Client) (int, error) {
	// Parse all Yahoo player HTML files using ListAll
	players, err := cache.ListAll(fs, cache.YahooPlayerFile{}, cache.ParseYahooPlayerFilename, func(file cache.File, content []byte) (cache.YahooPlayer, error) {
		yf := file.(cache.YahooPlayerFile)
		player, err := cache.ParseYahooPlayerHTML(yf.PlayerID, content)
		if err != nil {
			return cache.YahooPlayer{}, err
		}
		return *player, nil
	})
	if err != nil {
		return 0, err
	}

	log.Info().
		Int("yahoo_players", len(players)).
		Msg("Parsed Yahoo player files")

	// Save to Redis
	if err := SaveYahooIDPool(ctx, redisClient, players); err != nil {
		return 0, err
	}

	return len(players), nil
}

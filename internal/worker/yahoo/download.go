package yahoo

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
)

var (
	gameKeyCache   = make(map[int]int) // season year -> game key
	gameKeyCacheMu sync.RWMutex
)

// GetGameKeyForSeason fetches the Yahoo Fantasy game key for a given NHL season.
// Results are cached in memory and on disk since game keys never change.
func GetGameKeyForSeason(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, fetcher shared.Downloader, season int) (int, error) {
	// Check memory cache first
	gameKeyCacheMu.RLock()
	if key, ok := gameKeyCache[season]; ok {
		gameKeyCacheMu.RUnlock()
		return key, nil
	}
	gameKeyCacheMu.RUnlock()

	gameKey, err := getGameKeyImpl(ctx, storage, gobCache, season, fetcher, SleepAfterYahooDownload)
	if err != nil {
		return 0, err
	}

	// Update memory cache
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()

	return gameKey, nil
}

// getGameKeyImpl fetches the game key, using cache or downloading if needed.
func getGameKeyImpl(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int, fetcher shared.Downloader, postDownload func()) (int, error) {
	f := Fetcher{Storage: storage, GobCache: gobCache, Download: fetcher, Throttle: postDownload}
	fantasy, origin, err := f.Fetch(ctx, resource.GameKey{Season: season})
	if err != nil {
		return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
	}
	log.Debug().Int("season", season).Stringer("origin", origin).Msg("Game key fetched")
	return extractGameKey(fantasy, season)
}

// extractGameKey extracts the game key from parsed fantasy content.
func extractGameKey(fantasy *store.FantasyContent, season int) (int, error) {
	var gameKey int
	if len(fantasy.Games) > 0 {
		gameKey = fantasy.Games[0].Key
	} else if fantasy.Game.Key != 0 {
		gameKey = fantasy.Game.Key
	} else {
		return 0, fmt.Errorf("no game key found for season %d", season)
	}

	log.Info().Int("season", season).Int("game_key", gameKey).Msg("Loaded Yahoo game key")
	return gameKey, nil
}

// SetGameKeyCache seeds the in-memory game key cache for a given season.
// This is intended for use in tests that need to bypass the download path.
func SetGameKeyCache(season, gameKey int) {
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()
}

// SleepAfterYahooDownload sleeps for a random duration between min and max after a Yahoo API download.
func SleepAfterYahooDownload() {
	minSeconds := viper.GetInt(config.FlagYahooDownloadSleepMin)
	maxSeconds := viper.GetInt(config.FlagYahooDownloadSleepMax)
	if maxSeconds <= 0 {
		return
	}
	if minSeconds < 0 {
		minSeconds = 0
	}
	if minSeconds >= maxSeconds {
		minSeconds = maxSeconds
	}
	sleepSeconds := minSeconds + rand.Intn(maxSeconds-minSeconds+1)
	if sleepSeconds > 0 {
		log.Debug().Int("seconds", sleepSeconds).Msg("Sleeping after Yahoo download")
		time.Sleep(time.Duration(sleepSeconds) * time.Second)
	}
}

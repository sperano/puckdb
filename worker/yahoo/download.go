package yahoo

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
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
	gameKeyRes := resource.GameKey{Season: season}

	if storage.Exists(gameKeyRes.Path()) {
		log.Debug().Int("season", season).Msg("Game key file found")
		fantasy, _, err := cache.ReadParsedCached(ctx, storage, gobCache, gameKeyRes)
		if err != nil {
			return 0, fmt.Errorf("read stored game key for season %d: %w", season, err)
		}
		return extractGameKey(fantasy, season)
	}

	log.Info().Int("season", season).Msg("Downloading game key from Yahoo")
	content, err := fetcher(ctx, gameKeyRes.URL())
	if err != nil {
		return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
	}

	if err := storage.Write(gameKeyRes.Path(), content); err != nil {
		return 0, fmt.Errorf("save game key for season %d: %w", season, err)
	}
	log.Info().Int("season", season).Str("path", gameKeyRes.Path()).Msg("Saved game key")
	if postDownload != nil {
		postDownload()
	}

	fantasy, err := gameKeyRes.Parse(content)
	if err != nil {
		return 0, fmt.Errorf("parse game key response for season %d: %w", season, err)
	}

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

package yahoo

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
)

var (
	gameKeyCache   = make(map[int]int) // season year -> game key
	gameKeyCacheMu sync.RWMutex
	// gameKeyLoadLocks holds one single-slot semaphore (chan struct{}) per
	// season year, so concurrent misses for a season load its game key one at
	// a time and the callers that waited read it from memory instead of each
	// downloading it again.
	gameKeyLoadLocks sync.Map
)

const yahooNHLGameCode = "nhl"

// GetGameKeyForSeason fetches the Yahoo Fantasy game key for a given NHL season.
// Results are cached in memory and on disk since game keys never change.
//
// Misses for one season are serialized within the process: activities that
// start together on a cold cache (the Yahoo and NHL fetch groups both need
// the key) download it once. Each caller loads with its own context, so a
// canceled or failed load only hands the lock to the next waiter, which then
// tries itself.
func GetGameKeyForSeason(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, fetcher shared.Downloader, season int) (int, error) {
	if key, ok := memoryGameKey(season); ok {
		return key, nil
	}
	unlock, err := lockGameKeyLoad(ctx, season)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if key, ok := memoryGameKey(season); ok {
		return key, nil
	}

	gameKey, err := getGameKeyImpl(ctx, storage, gobCache, season, fetcher)
	if err != nil {
		return 0, err
	}

	// Update memory cache
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()

	return gameKey, nil
}

// memoryGameKey returns the season's game key from the in-memory cache.
func memoryGameKey(season int) (int, bool) {
	gameKeyCacheMu.RLock()
	defer gameKeyCacheMu.RUnlock()
	key, ok := gameKeyCache[season]
	return key, ok
}

// lockGameKeyLoad takes the season's load lock, or returns ctx's error if
// ctx ends first. The returned func releases the lock.
func lockGameKeyLoad(ctx context.Context, season int) (func(), error) {
	slot, _ := gameKeyLoadLocks.LoadOrStore(season, make(chan struct{}, 1))
	sem := slot.(chan struct{})
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for game key load for season %d: %w", season, ctx.Err())
	}
}

// getGameKeyImpl fetches the game key, using cache or downloading if needed.
func getGameKeyImpl(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int, fetcher shared.Downloader) (int, error) {
	f := Fetcher{Storage: storage, GobCache: gobCache, Download: fetcher}
	fantasy, origin, err := f.Fetch(ctx, resource.GameKey{Season: season})
	if err != nil {
		return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
	}
	log.Debug().Int("season", season).Stringer("origin", origin).Msg("Game key fetched")
	return extractGameKey(fantasy, season)
}

// extractGameKey extracts the game key from parsed fantasy content.
func extractGameKey(fantasy *store.FantasyContent, season int) (int, error) {
	var game store.FantasyGame
	if len(fantasy.Games) > 0 {
		game = fantasy.Games[0]
	} else if fantasy.Game.Key != 0 {
		game = fantasy.Game
	} else {
		return 0, fmt.Errorf("no game key found for season %d", season)
	}
	if game.Key <= 0 || game.Season != season || !strings.EqualFold(game.Code, yahooNHLGameCode) {
		return 0, fmt.Errorf("yahoo game key %d is for code %q season %d, requested NHL season %d",
			game.Key, game.Code, game.Season, season)
	}

	log.Info().Int("season", season).Int("game_key", game.Key).Msg("Loaded Yahoo game key")
	return game.Key, nil
}

// SetGameKeyCache seeds the in-memory game key cache for a given season.
// This is intended for use in tests that need to bypass the download path.
func SetGameKeyCache(season, gameKey int) {
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()
}

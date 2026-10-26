package yahoo

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

const yahooNHLGameCode = "nhl"

// GameKeys keeps the Yahoo Fantasy game key of each NHL season in memory, since
// game keys never change. The worker shares one between the activities that
// need a key; each test builds its own, so no key outlives the test that set it.
// A nil *GameKeys keeps nothing: every call fetches the key through the
// Fetcher's own caches.
type GameKeys struct {
	mu   sync.RWMutex
	keys map[int]int // season year -> game key
	// loadLocks holds one single-slot semaphore (chan struct{}) per season
	// year, so concurrent misses for a season load its game key one at a time
	// and the callers that waited read it from memory instead of each
	// downloading it again.
	loadLocks sync.Map
}

// NewGameKeys returns a GameKeys holding a copy of known (season year -> game
// key); known may be nil.
func NewGameKeys(known map[int]int) *GameKeys {
	keys := make(map[int]int, len(known))
	maps.Copy(keys, known)
	return &GameKeys{keys: keys}
}

// ForSeason returns the Yahoo Fantasy game key for a given NHL season, from
// memory or else through f, which reads the disk and gob caches before
// downloading.
//
// Misses for one season are serialized: activities that start together on a
// cold cache (the Yahoo and NHL fetch groups both need the key) download it
// once. Each caller loads with its own context, so a canceled or failed load
// only hands the lock to the next waiter, which then tries itself.
func (g *GameKeys) ForSeason(ctx context.Context, f Fetcher, season int) (int, error) {
	if g == nil {
		return fetchGameKey(ctx, f, season)
	}
	if key, ok := g.cached(season); ok {
		return key, nil
	}
	unlock, err := g.lockLoad(ctx, season)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if key, ok := g.cached(season); ok {
		return key, nil
	}

	gameKey, err := fetchGameKey(ctx, f, season)
	if err != nil {
		return 0, err
	}
	g.mu.Lock()
	g.keys[season] = gameKey
	g.mu.Unlock()
	return gameKey, nil
}

// cached returns the season's game key from memory.
func (g *GameKeys) cached(season int) (int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	key, ok := g.keys[season]
	return key, ok
}

// lockLoad takes the season's load lock, or returns ctx's error if ctx ends
// first. The returned func releases the lock.
func (g *GameKeys) lockLoad(ctx context.Context, season int) (func(), error) {
	slot, _ := g.loadLocks.LoadOrStore(season, make(chan struct{}, 1))
	sem := slot.(chan struct{})
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for game key load for season %d: %w", season, ctx.Err())
	}
}

// fetchGameKey fetches the game key, using cache or downloading if needed.
func fetchGameKey(ctx context.Context, f Fetcher, season int) (int, error) {
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

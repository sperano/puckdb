package yahoo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDownloader returns a shared.Downloader that returns the given content and error.
func mockDownloader(content []byte, err error) shared.Downloader {
	return func(_ context.Context, _ string) ([]byte, error) {
		return content, err
	}
}

const (
	gameKeyTestSeason = 2018
	gameKeyTestKey    = 398
	gameKeyTestXML    = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><games><game><game_key>398</game_key><game_id>398</game_id><code>nhl</code><season>2018</season></game></games></fantasy_content>`
	knownGameKeySeason     = 2020
	knownGameKey           = 390
	coalescedGameKeyCalls  = 5
	gameKeyNetworkErrorMsg = "network timeout"
)

// gameKeyFetcher returns a Fetcher over empty caches that downloads with dl.
func gameKeyFetcher(dl shared.Downloader) Fetcher {
	return Fetcher{Storage: store.NewMemStorage(), GobCache: cache.NewGobCache(nil), Download: dl}
}

// countingGameKeyDownloader downloads gameKeyTestXML and counts its calls.
func countingGameKeyDownloader(calls *atomic.Int32) shared.Downloader {
	return func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return []byte(gameKeyTestXML), nil
	}
}

func TestGameKeys_KnownKeyNeedsNoFetch(t *testing.T) {
	t.Parallel()
	keys := NewGameKeys(map[int]int{knownGameKeySeason: knownGameKey})

	// A zero Fetcher has no storage, cache or downloader to consult.
	got, err := keys.ForSeason(context.Background(), Fetcher{}, knownGameKeySeason)

	require.NoError(t, err)
	assert.Equal(t, knownGameKey, got)
}

func TestNewGameKeys_CopiesKnownKeys(t *testing.T) {
	t.Parallel()
	known := map[int]int{knownGameKeySeason: knownGameKey}
	keys := NewGameKeys(known)
	known[knownGameKeySeason] = gameKeyTestKey

	got, err := keys.ForSeason(context.Background(), Fetcher{}, knownGameKeySeason)

	require.NoError(t, err)
	assert.Equal(t, knownGameKey, got, "changing the caller's map must not change the cache")
}

func TestGameKeys_MissFetchesOnceThenRemembers(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	keys := NewGameKeys(nil)

	// Each call gets fresh storage, so only GameKeys can spare the second download.
	for range 2 {
		got, err := keys.ForSeason(context.Background(), gameKeyFetcher(countingGameKeyDownloader(&calls)), gameKeyTestSeason)
		require.NoError(t, err)
		assert.Equal(t, gameKeyTestKey, got)
	}
	assert.Equal(t, int32(1), calls.Load(), "the second call must read the key from memory")
}

func TestGameKeys_InstancesShareNoKeys(t *testing.T) {
	t.Parallel()
	seeded := NewGameKeys(map[int]int{gameKeyTestSeason: knownGameKey})
	var calls atomic.Int32

	got, err := NewGameKeys(nil).ForSeason(context.Background(), gameKeyFetcher(countingGameKeyDownloader(&calls)), gameKeyTestSeason)

	require.NoError(t, err)
	assert.Equal(t, gameKeyTestKey, got, "another instance's key must not be visible")
	assert.Equal(t, int32(1), calls.Load())
	seededKey, err := seeded.ForSeason(context.Background(), Fetcher{}, gameKeyTestSeason)
	require.NoError(t, err)
	assert.Equal(t, knownGameKey, seededKey)
}

func TestGameKeys_NilFetchesEveryCall(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var keys *GameKeys

	for range 2 {
		got, err := keys.ForSeason(context.Background(), gameKeyFetcher(countingGameKeyDownloader(&calls)), gameKeyTestSeason)
		require.NoError(t, err)
		assert.Equal(t, gameKeyTestKey, got)
	}
	assert.Equal(t, int32(2), calls.Load(), "a nil cache keeps nothing")
}

func TestGameKeys_FetcherError(t *testing.T) {
	t.Parallel()
	f := gameKeyFetcher(mockDownloader(nil, errors.New(gameKeyNetworkErrorMsg)))

	_, err := NewGameKeys(nil).ForSeason(context.Background(), f, gameKeyTestSeason)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch game key")
	assert.Contains(t, err.Error(), gameKeyNetworkErrorMsg)
}

// Callers that miss the same season at the same time download its game key
// once; the others wait and read it from memory.
func TestGameKeys_CoalescesConcurrentMisses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var downloads atomic.Int32
		fetcher := func(context.Context, string) ([]byte, error) {
			downloads.Add(1)
			<-release
			return []byte(gameKeyTestXML), nil
		}
		f := gameKeyFetcher(fetcher)
		gameKeys := NewGameKeys(nil)

		keys := make([]int, coalescedGameKeyCalls)
		errs := make([]error, coalescedGameKeyCalls)
		var wg sync.WaitGroup
		for i := range coalescedGameKeyCalls {
			wg.Go(func() {
				keys[i], errs[i] = gameKeys.ForSeason(context.Background(), f, gameKeyTestSeason)
			})
		}
		// Every caller is now blocked: one in the download, the rest on the
		// season's load lock.
		synctest.Wait()
		close(release)
		wg.Wait()

		for i := range coalescedGameKeyCalls {
			require.NoError(t, errs[i])
			assert.Equal(t, gameKeyTestKey, keys[i])
		}
		assert.Equal(t, int32(1), downloads.Load(), "one download for all concurrent misses")
	})
}

// A caller blocked on the season's load lock returns its own context error
// when its context ends, instead of waiting for the load that holds the lock.
func TestGameKeys_CanceledWaiterReturns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		gameKeys := NewGameKeys(nil)
		unlock, err := gameKeys.lockLoad(context.Background(), gameKeyTestSeason)
		require.NoError(t, err)
		defer unlock()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := gameKeys.ForSeason(ctx, Fetcher{}, gameKeyTestSeason)
			done <- err
		}()
		// The caller is now blocked on the load lock.
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned while the load lock was held and its context live: %v", err)
		default:
		}

		cancel()
		// If cancellation did not wake the caller, every goroutine in the
		// bubble is blocked and synctest fails the test as a deadlock.
		require.ErrorIs(t, <-done, context.Canceled)
	})
}

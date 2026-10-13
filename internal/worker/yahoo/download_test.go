package yahoo

import (
	"context"
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
	coalescedGameKeySeason = 2016
	coalescedGameKey       = 375
	coalescedGameKeyCalls  = 5
	coalescedGameKeyXML    = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><games><game><game_key>375</game_key><game_id>375</game_id><code>nhl</code><season>2016</season></game></games></fantasy_content>`
	canceledWaiterSeason = 2015
)

func forgetGameKey(season int) {
	gameKeyCacheMu.Lock()
	delete(gameKeyCache, season)
	gameKeyCacheMu.Unlock()
	gameKeyLoadLocks.Delete(season)
}

// Callers that miss the same season at the same time download its game key
// once; the others wait and read it from memory.
func TestGetGameKeyForSeason_CoalescesConcurrentMisses(t *testing.T) {
	forgetGameKey(coalescedGameKeySeason)
	t.Cleanup(func() { forgetGameKey(coalescedGameKeySeason) })

	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var downloads atomic.Int32
		fetcher := func(context.Context, string) ([]byte, error) {
			downloads.Add(1)
			<-release
			return []byte(coalescedGameKeyXML), nil
		}
		mem := store.NewMemStorage()
		gobCache := cache.NewGobCache(nil)

		keys := make([]int, coalescedGameKeyCalls)
		errs := make([]error, coalescedGameKeyCalls)
		var wg sync.WaitGroup
		for i := range coalescedGameKeyCalls {
			wg.Go(func() {
				keys[i], errs[i] = GetGameKeyForSeason(context.Background(), mem, gobCache, fetcher, coalescedGameKeySeason)
			})
		}
		// Every caller is now blocked: one in the download, the rest on the
		// season's load lock.
		synctest.Wait()
		close(release)
		wg.Wait()

		for i := range coalescedGameKeyCalls {
			require.NoError(t, errs[i])
			assert.Equal(t, coalescedGameKey, keys[i])
		}
		assert.Equal(t, int32(1), downloads.Load(), "one download for all concurrent misses")
	})
}

// A caller whose context ends while another load holds the season's lock
// returns its own context error instead of waiting for that load.
func TestGetGameKeyForSeason_CanceledWaiterReturns(t *testing.T) {
	forgetGameKey(canceledWaiterSeason)
	t.Cleanup(func() { forgetGameKey(canceledWaiterSeason) })

	unlock, err := lockGameKeyLoad(context.Background(), canceledWaiterSeason)
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = GetGameKeyForSeason(ctx, nil, nil, nil, canceledWaiterSeason)
	require.ErrorIs(t, err, context.Canceled)
}

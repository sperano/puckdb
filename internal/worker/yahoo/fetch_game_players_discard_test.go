package yahoo

import (
	"testing"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func runDiscardGameList(a *FetchActivities, downloadID int64, starts ...int) error {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.DiscardYahooGamePlayerList)
	_, err := env.ExecuteActivity(a.DiscardYahooGamePlayerList, DiscardYahooGamePlayerListInput{
		Season: yahoofixtures.CurrentGameSeason, DownloadID: downloadID, Starts: starts,
	})
	return err
}

// A failed download's pages go; the committed list's pages stay.
func TestDiscardYahooGamePlayerList_RemovesUncommittedDownloadPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0)
	require.NoError(t, runCommitGameList(a, commitGameListInput(gameListDownloadID, []int{0}, nil)))
	storeGamePages(mem, gameListNewDownloadID, 0, gameListSecondPage)

	require.NoError(t, runDiscardGameList(a, gameListNewDownloadID, 0, gameListSecondPage))

	assert.False(t, mem.Has(gamePage(gameListNewDownloadID, 0).Path()))
	assert.False(t, mem.Has(gamePage(gameListNewDownloadID, gameListSecondPage).Path()))
	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()), "the committed list keeps its page")
}

// With no list committed, the download's pages go; a page never stored is
// no error.
func TestDiscardYahooGamePlayerList_NoManifestRemovesPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListNewDownloadID, 0)

	require.NoError(t, runDiscardGameList(a, gameListNewDownloadID, 0, gameListSecondPage))

	assert.Zero(t, mem.Count())
}

// A download whose commit wrote the manifest is the season's list: its
// pages stay.
func TestDiscardYahooGamePlayerList_KeepsCommittedDownload(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0)
	require.NoError(t, runCommitGameList(a, commitGameListInput(gameListDownloadID, []int{0}, nil)))

	require.NoError(t, runDiscardGameList(a, gameListDownloadID, 0))

	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()))
	assert.Equal(t, gameListDownloadID, readGameListManifest(t, mem).DownloadID)
}

// A manifest that cannot be read may name the download, so nothing is
// deleted and the activity fails (to be retried).
func TestDiscardYahooGamePlayerList_ManifestReadErrorKeepsPages(t *testing.T) {
	mem := store.NewMemStorage()
	storeGamePages(mem, gameListNewDownloadID, 0)
	a := &FetchActivities{
		Storage:  failingManifestStorage{MemStorage: mem, season: yahoofixtures.CurrentGameSeason},
		GobCache: cache.NewGobCache(nil),
	}

	err := runDiscardGameList(a, gameListNewDownloadID, 0)

	require.ErrorContains(t, err, "read game player list manifest 2026: "+errManifestStorage.Error())
	assert.True(t, mem.Has(gamePage(gameListNewDownloadID, 0).Path()))
}

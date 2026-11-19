package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func commitGameListInput(downloadID int64, starts, discarded []int) CommitYahooGamePlayerListInput {
	return CommitYahooGamePlayerListInput{
		Season: yahoofixtures.CurrentGameSeason, GameKey: yahoofixtures.CurrentGameKey, DownloadID: downloadID,
		FetchedAt: time.UnixMilli(downloadID).UTC(), Starts: starts, Players: yahoofixtures.PlayersInGamePage,
		Discarded: discarded,
	}
}

func runCommitGameList(a *FetchActivities, input CommitYahooGamePlayerListInput) error {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.CommitYahooGamePlayerList)
	_, err := env.ExecuteActivity(a.CommitYahooGamePlayerList, input)
	return err
}

func storeGamePages(mem *store.MemStorage, downloadID int64, starts ...int) {
	for _, start := range starts {
		mem.SetFile(gamePage(downloadID, start).Path(), yahoofixtures.Read(yahoofixtures.GamePlayersPage))
	}
}

func readGameListManifest(t *testing.T, mem *store.MemStorage) resource.GamePlayerListManifest {
	t.Helper()
	manifest, err := resource.ReadParsed(context.Background(), mem,
		resource.GamePlayerList{Season: yahoofixtures.CurrentGameSeason})
	require.NoError(t, err)
	return manifest
}

func TestCommitYahooGamePlayerList_WritesManifest(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0, gameListSecondPage)

	require.NoError(t, runCommitGameList(a, commitGameListInput(gameListDownloadID, []int{0, gameListSecondPage}, nil)))

	assert.Equal(t, resource.GamePlayerListManifest{
		DownloadID: gameListDownloadID, GameKey: yahoofixtures.CurrentGameKey,
		FetchedAt: time.UnixMilli(gameListDownloadID).UTC(), Starts: []int{0, gameListSecondPage},
		Players: yahoofixtures.PlayersInGamePage,
	}, readGameListManifest(t, mem))
}

// Committing a new download removes the replaced list's pages and the
// download's discarded pages, and keeps its own.
func TestCommitYahooGamePlayerList_RemovesReplacedAndDiscardedPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0, gameListSecondPage)
	require.NoError(t, runCommitGameList(a, commitGameListInput(gameListDownloadID, []int{0, gameListSecondPage}, nil)))
	storeGamePages(mem, gameListNewDownloadID, 0, gameListSecondPage)

	require.NoError(t, runCommitGameList(a,
		commitGameListInput(gameListNewDownloadID, []int{0}, []int{gameListSecondPage})))

	assert.Equal(t, gameListNewDownloadID, readGameListManifest(t, mem).DownloadID)
	assert.True(t, mem.Has(gamePage(gameListNewDownloadID, 0).Path()))
	assert.False(t, mem.Has(gamePage(gameListNewDownloadID, gameListSecondPage).Path()), "discarded page")
	assert.False(t, mem.Has(gamePage(gameListDownloadID, 0).Path()), "replaced list page")
	assert.False(t, mem.Has(gamePage(gameListDownloadID, gameListSecondPage).Path()), "replaced list page")
}

// A retried commit of the committed download keeps its pages.
func TestCommitYahooGamePlayerList_RetryKeepsOwnPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0)
	input := commitGameListInput(gameListDownloadID, []int{0}, nil)
	require.NoError(t, runCommitGameList(a, input))

	require.NoError(t, runCommitGameList(a, input))

	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()))
	assert.Equal(t, gameListDownloadID, readGameListManifest(t, mem).DownloadID)
}

// A download with a missing page never replaces the committed list.
func TestCommitYahooGamePlayerList_MissingPageKeepsCommittedList(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0)
	require.NoError(t, runCommitGameList(a, commitGameListInput(gameListDownloadID, []int{0}, nil)))
	storeGamePages(mem, gameListNewDownloadID, 0)

	err := runCommitGameList(a, commitGameListInput(gameListNewDownloadID, []int{0, gameListSecondPage}, nil))

	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"page seasons/2026/yahoo-game/players/1763550000000/players-0025.xml is missing")
	assert.Equal(t, gameListDownloadID, readGameListManifest(t, mem).DownloadID)
	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()), "the committed list keeps its pages")
}

// A replaced manifest that cannot be read fails the commit before it
// writes, so the pages that manifest names are not orphaned.
func TestCommitYahooGamePlayerList_ReplacedManifestReadErrorKeepsList(t *testing.T) {
	mem := store.NewMemStorage()
	storeGamePages(mem, gameListDownloadID, 0)
	require.NoError(t, runCommitGameList(&FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)},
		commitGameListInput(gameListDownloadID, []int{0}, nil)))
	storeGamePages(mem, gameListNewDownloadID, 0)
	a := &FetchActivities{
		Storage:  failingManifestStorage{MemStorage: mem, season: yahoofixtures.CurrentGameSeason},
		GobCache: cache.NewGobCache(nil),
	}

	err := runCommitGameList(a, commitGameListInput(gameListNewDownloadID, []int{0}, nil))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read replaced game player list manifest 2026")
	assert.Contains(t, err.Error(), errManifestStorage.Error())
	assert.Equal(t, gameListDownloadID, readGameListManifest(t, mem).DownloadID)
	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()), "the committed list keeps its pages")
}

// A replaced manifest that no longer parses names no pages: the commit
// writes the new manifest and deletes nothing but its discarded pages.
func TestCommitYahooGamePlayerList_UnparsableReplacedManifest(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	storeGamePages(mem, gameListDownloadID, 0)
	mem.SetFile(resource.GamePlayerList{Season: yahoofixtures.CurrentGameSeason}.Path(), []byte("not json"))
	storeGamePages(mem, gameListNewDownloadID, 0, gameListSecondPage)

	require.NoError(t, runCommitGameList(a,
		commitGameListInput(gameListNewDownloadID, []int{0}, []int{gameListSecondPage})))

	assert.Equal(t, gameListNewDownloadID, readGameListManifest(t, mem).DownloadID)
	assert.True(t, mem.Has(gamePage(gameListDownloadID, 0).Path()), "no manifest names the old page")
	assert.False(t, mem.Has(gamePage(gameListNewDownloadID, gameListSecondPage).Path()), "discarded page")
}

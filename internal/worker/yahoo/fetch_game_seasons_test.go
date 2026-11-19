package yahoo

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// unorderedNHLGamesXML lists two NHL games newest first.
const unorderedNHLGamesXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <games count="2">
    <game><game_key>477</game_key><code>nhl</code><season>2026</season></game>
    <game><game_key>61</game_key><code>nhl</code><season>2000</season></game>
  </games>
</fantasy_content>`

func runListGameSeasons(t *testing.T, a *FetchActivities) ([]int, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ListYahooNHLGameSeasons)
	val, err := env.ExecuteActivity(a.ListYahooNHLGameSeasons)
	var seasons []int
	if err == nil {
		require.NoError(t, val.Get(&seasons))
	}
	return seasons, err
}

func TestListYahooNHLGameSeasons_DownloadsListAndSortsSeasons(t *testing.T) {
	mem := store.NewMemStorage()
	mem.SetFile(resource.YahooGames{}.Path(), yahoofixtures.Read(yahoofixtures.NHLGames))
	dl, calls := countingDownloader([]byte(unorderedNHLGamesXML), nil)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}

	seasons, err := runListGameSeasons(t, a)

	require.NoError(t, err)
	assert.Equal(t, []int{2000, 2026}, seasons, "the downloaded list, not the cached one, oldest first")
	assert.Equal(t, 1, *calls, "the games list is downloaded even when cached")
	assert.Equal(t, unorderedNHLGamesXML, string(mem.Get(resource.YahooGames{}.Path())),
		"the download replaces the cached list the plans read")
}

func TestListYahooNHLGameSeasons_DownloadErrorFails(t *testing.T) {
	errYahoo := errors.New("yahoo unavailable")
	dl, _ := countingDownloader(nil, errYahoo)
	a := &FetchActivities{Storage: store.NewMemStorage(), Download: dl, GobCache: cache.NewGobCache(nil)}

	_, err := a.ListYahooNHLGameSeasons(context.Background())

	require.ErrorIs(t, err, errYahoo)
	assert.Contains(t, err.Error(), "refresh Yahoo NHL games")
}

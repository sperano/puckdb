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

const (
	replacedDownloadID = int64(1758369600000)
	newDownloadID      = int64(1758412800000)
	secondPageStart    = resource.LeaguePlayersPageSize
)

func poolPage(downloadID int64, start int) resource.LeaguePlayers {
	return resource.LeaguePlayers{
		Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, DownloadID: downloadID, Start: start,
	}
}

func commitPool(t *testing.T, a *FetchActivities, downloadID int64, starts []int) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.CommitYahooLeaguePlayerPool)
	_, err := env.ExecuteActivity(a.CommitYahooLeaguePlayerPool, CommitYahooLeaguePlayerPoolInput{
		Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, DownloadID: downloadID,
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: time.UnixMilli(downloadID).UTC(),
		Starts: starts, Players: yahoofixtures.PlayersInPage,
	})
	require.NoError(t, err)
}

// Committing a new download removes the replaced snapshot's pages and keeps
// its own.
func TestCommitYahooLeaguePlayerPool_RemovesReplacedSnapshotPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	page := yahoofixtures.Read(yahoofixtures.PlayersPage)
	for _, start := range []int{0, secondPageStart} {
		mem.SetFile(poolPage(replacedDownloadID, start).Path(), page)
	}
	commitPool(t, a, replacedDownloadID, []int{0, secondPageStart})
	mem.SetFile(poolPage(newDownloadID, 0).Path(), page)

	commitPool(t, a, newDownloadID, []int{0})

	assert.False(t, mem.Has(poolPage(replacedDownloadID, 0).Path()))
	assert.False(t, mem.Has(poolPage(replacedDownloadID, secondPageStart).Path()))
	assert.True(t, mem.Has(poolPage(newDownloadID, 0).Path()))
	manifest, err := resource.ReadParsed(context.Background(), mem,
		resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})
	require.NoError(t, err)
	assert.Equal(t, newDownloadID, manifest.DownloadID)
}

// A retried commit of the same download must not delete its own pages.
func TestCommitYahooLeaguePlayerPool_RetryKeepsOwnPages(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	mem.SetFile(poolPage(newDownloadID, 0).Path(), yahoofixtures.Read(yahoofixtures.PlayersPage))

	commitPool(t, a, newDownloadID, []int{0})
	commitPool(t, a, newDownloadID, []int{0})

	assert.True(t, mem.Has(poolPage(newDownloadID, 0).Path()))
}

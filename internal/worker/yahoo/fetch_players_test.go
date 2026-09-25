package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// fetchPlayersTestNow is a fixed "current time" used where the plan tests
// need one that is not tied to a fixture's end date.
var fetchPlayersTestNow = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

func writeLeagueXML(t *testing.T, mem *store.MemStorage, season, leagueID int, xml []byte) {
	t.Helper()
	res := resource.League{Season: season, LeagueID: leagueID}
	require.NoError(t, mem.Write(context.Background(), res.Path(), xml))
}

func runPlanActivity(t *testing.T, a *FetchActivities, input PlanYahooLeaguePlayerPoolInput) (YahooLeaguePlayerPoolPlan, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.PlanYahooLeaguePlayerPool)
	val, err := env.ExecuteActivity(a.PlanYahooLeaguePlayerPool, input)
	var plan YahooLeaguePlayerPoolPlan
	if err == nil {
		require.NoError(t, val.Get(&plan))
	}
	return plan, err
}

// ────────────────────────────────────────────────────────────────────────────
// PlanYahooLeaguePlayerPool
// ────────────────────────────────────────────────────────────────────────────

func TestPlanYahooLeaguePlayerPool_LeagueSeasonEnded(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.Read(yahoofixtures.RotoLeague))
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.RotoSeason, LeagueID: yahoofixtures.RotoLeagueID})

	require.NoError(t, err)
	assert.False(t, plan.Refresh)
	assert.Contains(t, plan.Reason, "league season ended")
}

func TestPlanYahooLeaguePlayerPool_NoManifest_RefreshesPool(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.Read(yahoofixtures.PointsLeague))
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})

	require.NoError(t, err)
	assert.True(t, plan.Refresh)
	assert.Contains(t, plan.Reason, "never downloaded")
}

func TestPlanYahooLeaguePlayerPool_FreshManifest_NoRefresh(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.Read(yahoofixtures.PointsLeague))
	manifest := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, manifest, resource.LeaguePlayerPoolManifest{
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: time.Now(), Starts: []int{0}, Players: 7,
	}))
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})

	require.NoError(t, err)
	assert.False(t, plan.Refresh)
	assert.Contains(t, plan.Reason, "ago")
}

func TestPlanYahooLeaguePlayerPool_OldManifest_Refreshes(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.Read(yahoofixtures.PointsLeague))
	manifest := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	old := time.Now().Add(-2 * time.Duration(config.DefaultYahooPlayerPoolMaxAge) * time.Hour)
	require.NoError(t, resource.WriteParsed(context.Background(), mem, manifest, resource.LeaguePlayerPoolManifest{
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: old, Starts: []int{0}, Players: 7,
	}))
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})

	require.NoError(t, err)
	assert.True(t, plan.Refresh)
	assert.Contains(t, plan.Reason, "older than")
}

func TestPlanYahooLeaguePlayerPool_UnreadableManifest_Refreshes(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.Read(yahoofixtures.PointsLeague))
	manifestRes := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	require.NoError(t, mem.Write(context.Background(), manifestRes.Path(), []byte("not json")))
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})

	require.NoError(t, err)
	assert.True(t, plan.Refresh)
	assert.Contains(t, plan.Reason, "unreadable")
}

func TestPlanYahooLeaguePlayerPool_MissingLeagueFile_Error(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	_, err := runPlanActivity(t, a, PlanYahooLeaguePlayerPoolInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})

	require.Error(t, err)
}

// ────────────────────────────────────────────────────────────────────────────
// FetchYahooLeaguePlayersPage
// ────────────────────────────────────────────────────────────────────────────

// countingDownloader records how many times it was invoked so a test can
// assert a page is downloaded even when a cached copy already exists.
func countingDownloader(content []byte, err error) (shared.Downloader, *int) {
	calls := 0
	return func(_ context.Context, _ string) ([]byte, error) {
		calls++
		return content, err
	}, &calls
}

func runPageActivity(t *testing.T, a *FetchActivities, input FetchYahooLeaguePlayersPageInput) (FetchYahooLeaguePlayersPageResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.FetchYahooLeaguePlayersPage)
	val, err := env.ExecuteActivity(a.FetchYahooLeaguePlayersPage, input)
	var result FetchYahooLeaguePlayersPageResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

func TestFetchYahooLeaguePlayersPage_DownloadsEvenWhenCached(t *testing.T) {
	SetGameKeyCache(yahoofixtures.PointsSeason, yahoofixtures.PointsGameKey)
	mem := store.NewMemStorage()
	page := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, Start: 0}
	// Pre-populate a stale cached copy that must be overwritten.
	require.NoError(t, mem.Write(context.Background(), page.Path(),
		[]byte(`<fantasy_content><league><league_key>465.l.77777</league_key><league_id>77777</league_id></league></fantasy_content>`)))
	fresh := yahoofixtures.Read(yahoofixtures.PlayersPage)
	dl, calls := countingDownloader(fresh, nil)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}

	result, err := runPageActivity(t, a, FetchYahooLeaguePlayersPageInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, Start: 0})

	require.NoError(t, err)
	assert.Equal(t, 1, *calls, "a page must always be downloaded, even when cached")
	assert.Equal(t, yahoofixtures.PlayersInPage, result.Players)
	assert.Equal(t, "465.l.77777", result.LeagueKey)
	assert.Equal(t, yahoofixtures.PointsGameKey, result.GameKey)
	stored, err := mem.Read(context.Background(), page.Path())
	require.NoError(t, err)
	assert.Equal(t, fresh, stored, "the fresh download must replace the stale cached copy")
}

func TestFetchYahooLeaguePlayersPage_DownloadError(t *testing.T) {
	SetGameKeyCache(yahoofixtures.PointsSeason, yahoofixtures.PointsGameKey)
	mem := store.NewMemStorage()
	dl, _ := countingDownloader(nil, assert.AnError)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}

	_, err := runPageActivity(t, a, FetchYahooLeaguePlayersPageInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, Start: 0})

	require.Error(t, err)
}

// ────────────────────────────────────────────────────────────────────────────
// CommitYahooLeaguePlayerPool
// ────────────────────────────────────────────────────────────────────────────

func TestCommitYahooLeaguePlayerPool_WritesReadableManifest(t *testing.T) {
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	fetchedAt := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.CommitYahooLeaguePlayerPool)
	_, err := env.ExecuteActivity(a.CommitYahooLeaguePlayerPool, CommitYahooLeaguePlayerPoolInput{
		Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID,
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: fetchedAt,
		Starts: []int{0, 25}, Players: 32,
	})
	require.NoError(t, err)

	manifest, err := resource.ReadParsed(context.Background(), mem,
		resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})
	require.NoError(t, err)
	assert.Equal(t, "465.l.77777", manifest.LeagueKey)
	assert.Equal(t, yahoofixtures.PointsGameKey, manifest.GameKey)
	assert.True(t, fetchedAt.Equal(manifest.FetchedAt))
	assert.Equal(t, []int{0, 25}, manifest.Starts)
	assert.Equal(t, 32, manifest.Players)
}

// ────────────────────────────────────────────────────────────────────────────
// leagueSeasonEnded
// ────────────────────────────────────────────────────────────────────────────

func TestLeagueSeasonEnded(t *testing.T) {
	t.Parallel()
	const endDate = "2026-04-16" // matches yahoofixtures.RotoLeague's end_date

	tests := []struct {
		name   string
		league store.League
		now    time.Time
		ended  bool
	}{
		{"last_day_still_current", store.League{EndDate: endDate}, time.Date(2026, 4, 16, 23, 0, 0, 0, time.UTC), false},
		{"day_after_grace_is_ended", store.League{EndDate: endDate}, time.Date(2026, 4, 18, 0, 1, 0, 0, time.UTC), true},
		{"unparseable_end_date_is_not_ended", store.League{EndDate: "not-a-date"}, fetchPlayersTestNow, false},
		{"empty_end_date_is_not_ended", store.League{EndDate: ""}, fetchPlayersTestNow, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.ended, leagueSeasonEnded(tt.league, tt.now))
		})
	}
}

package yahoo

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// gamePlayersTestNow falls in the fixture's current (2026) season.
var gamePlayersTestNow = time.Date(2026, time.November, 19, 12, 0, 0, 0, time.UTC)

const (
	gameListDownloadID     = int64(1763500000000)
	gameListNewDownloadID  = int64(1763550000000)
	gameListSecondPage     = resource.GamePlayersPageSize
	gamePlayersPageTTL     = 3 * time.Hour
	committedGamePlayers   = 1613
	gameListFreshAge       = 20 * time.Hour
	gameListStaleAge       = 24*time.Hour + time.Minute
	gameListFinalListAge   = 400 * 24 * time.Hour
	expectedListMaxAgeHour = 24
)

// gameOverBeforeNextSeason falls in the 2025 season by the NHL calendar
// (resource.DeduceSeason), after Yahoo marked its game over.
var gameOverBeforeNextSeason = time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)

// errManifestStorage is the storage failure failingManifestStorage returns.
var errManifestStorage = errors.New("storage unavailable")

// failingManifestStorage fails every read of the season's game player list
// manifest with errManifestStorage.
type failingManifestStorage struct {
	*store.MemStorage
	season int
}

func (s failingManifestStorage) Read(ctx context.Context, path string) ([]byte, error) {
	if path == (resource.GamePlayerList{Season: s.season}).Path() {
		return nil, errManifestStorage
	}
	return s.MemStorage.Read(ctx, path)
}

// gameActivities returns activities over mem whose games list is cached on
// disk, downloading through dl (nil for a test that downloads nothing).
func gameActivities(mem *store.MemStorage, dl shared.Downloader) *FetchActivities {
	mem.SetFile(resource.YahooGames{}.Path(), yahoofixtures.Read(yahoofixtures.NHLGames))
	return &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}
}

func writeGameListManifest(t *testing.T, mem *store.MemStorage, season int, manifest resource.GamePlayerListManifest) {
	t.Helper()
	require.NoError(t, resource.WriteParsed(context.Background(), mem, resource.GamePlayerList{Season: season}, manifest))
}

func runPlanGameActivity(t *testing.T, a *FetchActivities, season int) (YahooGamePlayerListPlan, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.PlanYahooGamePlayerList)
	val, err := env.ExecuteActivity(a.PlanYahooGamePlayerList, PlanYahooGamePlayerListInput{Season: season})
	var plan YahooGamePlayerListPlan
	if err == nil {
		require.NoError(t, val.Get(&plan))
	}
	return plan, err
}

// ────────────────────────────────────────────────────────────────────────────
// PlanYahooGamePlayerList
// ────────────────────────────────────────────────────────────────────────────

func TestPlanYahooGamePlayerList_NoManifest_Refreshes(t *testing.T) {
	dl, calls := countingDownloader(nil, errors.New("must not download"))
	a := gameActivities(store.NewMemStorage(), dl)

	plan, err := runPlanGameActivity(t, a, yahoofixtures.CurrentGameSeason)

	require.NoError(t, err)
	assert.Equal(t, YahooGamePlayerListPlan{
		Refresh: true, Reason: "list never downloaded", GameKey: yahoofixtures.CurrentGameKey,
	}, plan)
	assert.Zero(t, *calls, "the cached games list names the season's game")
}

func TestPlanYahooGamePlayerList_UnreadableManifest_Refreshes(t *testing.T) {
	mem := store.NewMemStorage()
	a := gameActivities(mem, nil)
	mem.SetFile(resource.GamePlayerList{Season: yahoofixtures.CurrentGameSeason}.Path(), []byte("not json"))

	plan, err := a.planYahooGamePlayerList(context.Background(),
		PlanYahooGamePlayerListInput{Season: yahoofixtures.CurrentGameSeason}, gamePlayersTestNow)

	require.NoError(t, err)
	assert.Equal(t, YahooGamePlayerListPlan{
		Refresh: true, Reason: "list manifest unreadable", GameKey: yahoofixtures.CurrentGameKey,
	}, plan)
}

func TestPlanYahooGamePlayerList_ManifestReadError(t *testing.T) {
	mem := store.NewMemStorage()
	a := gameActivities(mem, nil)
	a.Storage = failingManifestStorage{MemStorage: mem, season: yahoofixtures.CurrentGameSeason}

	_, err := a.planYahooGamePlayerList(context.Background(),
		PlanYahooGamePlayerListInput{Season: yahoofixtures.CurrentGameSeason}, gamePlayersTestNow)

	require.ErrorIs(t, err, errManifestStorage)
	assert.Contains(t, err.Error(), "read game player list manifest 2026")
}

func TestPlanYahooGamePlayerList_CommittedList(t *testing.T) {
	assert.Equal(t, expectedListMaxAgeHour, config.DefaultYahooGamePlayerListMaxAge)
	tests := []struct {
		name        string
		season      int
		gameKey     int
		manifestKey int
		now         time.Time
		age         time.Duration
		wantRefresh bool
		wantReason  string
	}{
		{"current_season_fresh", yahoofixtures.CurrentGameSeason, yahoofixtures.CurrentGameKey,
			yahoofixtures.CurrentGameKey, gamePlayersTestNow, gameListFreshAge, false, "list downloaded 20h0m0s ago"},
		{"current_season_stale", yahoofixtures.CurrentGameSeason, yahoofixtures.CurrentGameKey,
			yahoofixtures.CurrentGameKey, gamePlayersTestNow, gameListStaleAge, true, "list older than 24h0m0s"},
		{"game_over_is_final", yahoofixtures.PastGameSeason, yahoofixtures.PastGameKey,
			yahoofixtures.PastGameKey, gamePlayersTestNow, gameListFinalListAge, false, "season 2025 is over, its list is final"},
		{"yahoo_game_over_is_final_before_next_season", yahoofixtures.PastGameSeason, yahoofixtures.PastGameKey,
			yahoofixtures.PastGameKey, gameOverBeforeNextSeason, gameListFinalListAge, false, "season 2025 is over, its list is final"},
		{"later_season_started_is_final", yahoofixtures.CurrentGameSeason, yahoofixtures.CurrentGameKey,
			yahoofixtures.CurrentGameKey, time.Date(2027, time.September, 1, 0, 0, 0, 0, time.UTC), gameListFinalListAge,
			false, "season 2026 is over, its list is final"},
		{"other_game_key_refreshes_even_when_over", yahoofixtures.PastGameSeason, yahoofixtures.PastGameKey,
			yahoofixtures.CurrentGameKey, gamePlayersTestNow, gameListFreshAge, true, "list downloaded for game key 477"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := store.NewMemStorage()
			a := gameActivities(mem, nil)
			writeGameListManifest(t, mem, tt.season, resource.GamePlayerListManifest{
				DownloadID: gameListDownloadID, GameKey: tt.manifestKey, FetchedAt: tt.now.Add(-tt.age),
				Starts: []int{0}, Players: committedGamePlayers,
			})

			plan, err := a.planYahooGamePlayerList(context.Background(), PlanYahooGamePlayerListInput{Season: tt.season}, tt.now)

			require.NoError(t, err)
			assert.Equal(t, YahooGamePlayerListPlan{
				Refresh: tt.wantRefresh, Reason: tt.wantReason, GameKey: tt.gameKey, PreviousPlayerCount: committedGamePlayers,
			}, plan)
		})
	}
}

func TestPlanYahooGamePlayerList_MaxAgeFlag(t *testing.T) {
	const maxAgeHours = 6
	viper.Set(config.FlagYahooGamePlayerListMaxAge, maxAgeHours)
	t.Cleanup(viper.Reset)
	mem := store.NewMemStorage()
	a := gameActivities(mem, nil)
	writeGameListManifest(t, mem, yahoofixtures.CurrentGameSeason, resource.GamePlayerListManifest{
		DownloadID: gameListDownloadID, GameKey: yahoofixtures.CurrentGameKey,
		FetchedAt: gamePlayersTestNow.Add(-7 * time.Hour), Starts: []int{0},
	})

	plan, err := a.planYahooGamePlayerList(context.Background(),
		PlanYahooGamePlayerListInput{Season: yahoofixtures.CurrentGameSeason}, gamePlayersTestNow)

	require.NoError(t, err)
	assert.True(t, plan.Refresh)
	assert.Equal(t, "list older than 6h0m0s", plan.Reason)
}

// A games list cached before the season's game existed is downloaded again.
func TestPlanYahooGamePlayerList_CachedGamesListWithoutSeason_RefreshesList(t *testing.T) {
	mem := store.NewMemStorage()
	mem.SetFile(resource.YahooGames{}.Path(),
		[]byte(`<fantasy_content><games><game><game_key>465</game_key><code>nhl</code><season>2025</season></game></games></fantasy_content>`))
	fresh := yahoofixtures.Read(yahoofixtures.NHLGames)
	dl, calls := countingDownloader(fresh, nil)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanGameActivity(t, a, yahoofixtures.CurrentGameSeason)

	require.NoError(t, err)
	assert.Equal(t, yahoofixtures.CurrentGameKey, plan.GameKey)
	assert.True(t, plan.Refresh)
	assert.Equal(t, 1, *calls)
	stored, err := mem.Read(context.Background(), resource.YahooGames{}.Path())
	require.NoError(t, err)
	assert.Equal(t, fresh, stored, "the downloaded list replaces the cached one")
}

func TestPlanYahooGamePlayerList_SeasonWithoutGame_Unavailable(t *testing.T) {
	const futureSeason = yahoofixtures.CurrentGameSeason + 1
	dl, calls := countingDownloader(yahoofixtures.Read(yahoofixtures.NHLGames), nil)
	a := gameActivities(store.NewMemStorage(), dl)

	plan, err := runPlanGameActivity(t, a, futureSeason)

	require.NoError(t, err)
	assert.Equal(t, YahooGamePlayerListPlan{Unavailable: true, Reason: "Yahoo lists no NHL game for season 2027"}, plan)
	assert.Equal(t, 1, *calls, "the games list is downloaded once before the season is reported missing")
}

// A games list Fetch has just downloaded is not downloaded again for a
// season it does not list.
func TestPlanYahooGamePlayerList_DownloadedGamesListWithoutSeason_DownloadsOnce(t *testing.T) {
	const futureSeason = yahoofixtures.CurrentGameSeason + 1
	dl, calls := countingDownloader(yahoofixtures.Read(yahoofixtures.NHLGames), nil)
	a := &FetchActivities{Storage: store.NewMemStorage(), Download: dl, GobCache: cache.NewGobCache(nil)}

	plan, err := runPlanGameActivity(t, a, futureSeason)

	require.NoError(t, err)
	assert.Equal(t, YahooGamePlayerListPlan{Unavailable: true, Reason: "Yahoo lists no NHL game for season 2027"}, plan)
	assert.Equal(t, 1, *calls)
}

func TestPlanYahooGamePlayerList_GamesListDownloadError(t *testing.T) {
	dl, _ := countingDownloader(nil, errors.New("yahoo is down"))
	a := &FetchActivities{Storage: store.NewMemStorage(), Download: dl, GobCache: cache.NewGobCache(nil)}

	_, err := runPlanGameActivity(t, a, yahoofixtures.CurrentGameSeason)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch Yahoo NHL games")
	assert.Contains(t, err.Error(), "yahoo is down")
}

func TestPlanYahooGamePlayerList_GamesListCachedInValkey(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	mem := store.NewMemStorage()
	dl, calls := countingDownloader(yahoofixtures.Read(yahoofixtures.NHLGames), nil)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(client)}

	_, err := runPlanGameActivity(t, a, yahoofixtures.CurrentGameSeason)
	require.NoError(t, err)
	require.NoError(t, mem.Delete(context.Background(), resource.YahooGames{}.Path()))
	plan, err := runPlanGameActivity(t, a, yahoofixtures.CurrentGameSeason)

	require.NoError(t, err)
	assert.Equal(t, yahoofixtures.CurrentGameKey, plan.GameKey)
	assert.Equal(t, 1, *calls, "the second plan reads the games list from Valkey")
	assert.True(t, server.Exists(core.RedisKey(resource.YahooGames{})))
}

// ────────────────────────────────────────────────────────────────────────────
// FetchYahooGamePlayersPage
// ────────────────────────────────────────────────────────────────────────────

func runGamePageActivity(t *testing.T, a *FetchActivities, input FetchYahooGamePlayersPageInput) (FetchYahooGamePlayersPageResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.FetchYahooGamePlayersPage)
	val, err := env.ExecuteActivity(a.FetchYahooGamePlayersPage, input)
	var result FetchYahooGamePlayersPageResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

func currentGamePageInput(start int) FetchYahooGamePlayersPageInput {
	return FetchYahooGamePlayersPageInput{
		Season: yahoofixtures.CurrentGameSeason, GameKey: yahoofixtures.CurrentGameKey,
		DownloadID: gameListDownloadID, Start: start,
	}
}

func gamePage(downloadID int64, start int) resource.GamePlayers {
	return resource.GamePlayers{Season: yahoofixtures.CurrentGameSeason, DownloadID: downloadID, Start: start}
}

func TestFetchYahooGamePlayersPage_DownloadsStoresAndCaches(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	gobConfig := &cache.GobCacheConfig{Types: map[core.FileType]cache.GobCacheTypeConfig{
		core.YahooGamePlayers: {TTL: gamePlayersPageTTL},
	}}
	mem := store.NewMemStorage()
	page := gamePage(gameListDownloadID, 0)
	mem.SetFile(page.Path(), []byte("stale copy of an earlier attempt"))
	fresh := yahoofixtures.Read(yahoofixtures.GamePlayersPage)
	dl, calls := countingDownloader(fresh, nil)
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(client, cache.WithConfig(gobConfig))}

	result, err := runGamePageActivity(t, a, currentGamePageInput(0))

	require.NoError(t, err)
	assert.Equal(t, FetchYahooGamePlayersPageResult{Players: yahoofixtures.PlayersInGamePage}, result)
	assert.Equal(t, 1, *calls, "a page is always downloaded, even when stored")
	stored, err := mem.Read(context.Background(), page.Path())
	require.NoError(t, err)
	assert.Equal(t, fresh, stored, "the raw response is stored verbatim")
	assert.Equal(t, gamePlayersPageTTL, server.TTL(core.RedisKey(page)), "the parsed page is cached with its type's TTL")
}

// A page whose season, game key, download ID or offset is out of range is
// refused before any download, so it is never stored where no manifest can
// name it.
func TestFetchYahooGamePlayersPage_InvalidInputIsNonRetryable(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*FetchYahooGamePlayersPageInput)
		message string
	}{
		{"no season", func(in *FetchYahooGamePlayersPageInput) { in.Season = 0 },
			"game players 0 start 0: season 0 is not a season"},
		{"no game key", func(in *FetchYahooGamePlayersPageInput) { in.GameKey = 0 },
			"game players 2026 start 0: game key 0 is not a game key"},
		{"no download ID", func(in *FetchYahooGamePlayersPageInput) { in.DownloadID = 0 },
			"game players 2026 start 0: download ID 0 is not a download ID"},
		{"negative download ID", func(in *FetchYahooGamePlayersPageInput) { in.DownloadID = -1 },
			"game players 2026 start 0: download ID -1 is not a download ID"},
		{"negative offset", func(in *FetchYahooGamePlayersPageInput) { in.Start = -1 },
			"game players 2026 start -1: offset -1 is negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dl, calls := countingDownloader(nil, errors.New("must not download"))
			mem := store.NewMemStorage()
			a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}
			input := currentGamePageInput(0)
			tt.mutate(&input)

			_, err := runGamePageActivity(t, a, input)

			assertRetryability(t, err, false, invalidGamePlayersPageErrorType)
			assert.Contains(t, err.Error(), tt.message)
			assert.Zero(t, *calls)
		})
	}
}

func TestFetchYahooGamePlayersPage_OtherGameIsNeverStored(t *testing.T) {
	dl, _ := countingDownloader(yahoofixtures.Read(yahoofixtures.GamePlayersPage), nil)
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}
	input := currentGamePageInput(gameListSecondPage)
	input.GameKey = yahoofixtures.PastGameKey

	_, err := runGamePageActivity(t, a, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "response game key 477 does not match requested game key 465")
	assert.False(t, mem.Exists(context.Background(), gamePage(gameListDownloadID, gameListSecondPage).Path()))
}

func TestFetchYahooGamePlayersPage_DownloadErrorIsRetryable(t *testing.T) {
	dl, _ := countingDownloader(nil, errors.New("yahoo is down"))
	a := &FetchActivities{Storage: store.NewMemStorage(), Download: dl, GobCache: cache.NewGobCache(nil)}

	_, err := runGamePageActivity(t, a, currentGamePageInput(0))

	assertRetryability(t, err, true, "")
	assert.Contains(t, err.Error(), "fetch game players 2026 start 0")
}

// Yahoo answers 400 for an offset past the last page instead of an empty
// page, so a list whose size is a multiple of the page size ends there.
func TestFetchYahooGamePlayersPage_BadRequestPastFirstPageEndsList(t *testing.T) {
	rejection := &httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}
	dl, calls := countingDownloader(nil, rejection)
	mem := store.NewMemStorage()
	a := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(nil)}

	result, err := runGamePageActivity(t, a, currentGamePageInput(gameListSecondPage))

	require.NoError(t, err)
	assert.Equal(t, FetchYahooGamePlayersPageResult{End: true}, result)
	assert.Equal(t, 1, *calls, "the rejected offset is not retried")
	assert.False(t, mem.Exists(context.Background(), gamePage(gameListDownloadID, gameListSecondPage).Path()))
}

// A 400 on the first page is no end of list: a game always has players.
func TestFetchYahooGamePlayersPage_BadRequestOnFirstPageIsAnError(t *testing.T) {
	rejection := &httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}
	dl, _ := countingDownloader(nil, rejection)
	a := &FetchActivities{Storage: store.NewMemStorage(), Download: dl, GobCache: cache.NewGobCache(nil)}

	_, err := runGamePageActivity(t, a, currentGamePageInput(0))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch game players 2026 start 0")
}

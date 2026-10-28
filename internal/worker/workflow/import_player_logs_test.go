package workflow

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	// playerLogsFixtureYear is the season start year of the boxscore fixture.
	playerLogsFixtureYear = 2023
	// playerLogsFixtureFirstSkater is the lowest skater ID of the fixture.
	playerLogsFixtureFirstSkater = 8470000
	// playerLogsFixtureSkaters is the fixture's unique skater count: more
	// than two batches of playerGameLogBatchSize, so the last one is short.
	playerLogsFixtureSkaters = 2*playerGameLogBatchSize + 20
	// playerLogsFixtureOverlap is how many skaters both fixture games list.
	playerLogsFixtureOverlap = 30
	// playerLogsFixtureGoalie appears in a boxscore but is not a skater.
	playerLogsFixtureGoalie = 8460000
)

// playerLogsFixtureSeason is the season the boxscore fixture covers.
func playerLogsFixtureSeason() nhl.SeasonInfo {
	return testSeasonW(playerLogsFixtureYear, "2023-10-10", "2023-10-12")
}

// seedPlayerLogsBoxscores caches two final games whose skater lists
// overlap by playerLogsFixtureOverlap (one listing the goalie too) and an
// unreadable boxscore of a third game. It returns the unique skater IDs.
func seedPlayerLogsBoxscores(t *testing.T, mem *store.MemStorage) []int64 {
	t.Helper()
	ctx := context.Background()
	day1 := nhl.MustParseDate("2023-10-10").Time
	day2 := nhl.MustParseDate("2023-10-11").Time
	firstHalf := playerLogsFixtureSkaters/2 + playerLogsFixtureOverlap/2
	secondHalf := playerLogsFixtureSkaters/2 - playerLogsFixtureOverlap/2

	game1, game2, corrupt := nhl.GameID(2023020001), nhl.GameID(2023020002), nhl.GameID(2023020003)
	seedSchedule(t, mem, day1, game1)
	seedSchedule(t, mem, day2, game2, corrupt)
	seedSkaters(t, mem, resource.Boxscore{Date: day1, GameID: game1}, skaterRange(0, firstHalf), nil)
	seedSkaters(t, mem, resource.Boxscore{Date: day2, GameID: game2}, skaterRange(secondHalf, playerLogsFixtureSkaters),
		[]nhl.GoalieStats{{PlayerID: nhl.PlayerID(playerLogsFixtureGoalie), Position: "G"}})
	require.NoError(t, mem.Write(ctx, resource.Boxscore{Date: day2, GameID: corrupt}.Path(), []byte("{not json")))

	ids := make([]int64, playerLogsFixtureSkaters)
	for i := range ids {
		ids[i] = playerLogsFixtureFirstSkater + int64(i)
	}
	return ids
}

func seedSchedule(t *testing.T, mem *store.MemStorage, day time.Time, games ...nhl.GameID) {
	t.Helper()
	schedule := &nhl.DailySchedule{}
	for _, id := range games {
		schedule.Games = append(schedule.Games, nhl.ScheduleGame{ID: id, GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateOff})
	}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))
}

// skaterRange lists the fixture skaters [from, to) as forwards.
func skaterRange(from, to int) []nhl.SkaterStats {
	skaters := make([]nhl.SkaterStats, 0, to-from)
	for i := from; i < to; i++ {
		skaters = append(skaters, nhl.SkaterStats{PlayerID: nhl.PlayerID(playerLogsFixtureFirstSkater + i), Position: "C"})
	}
	return skaters
}

func seedSkaters(t *testing.T, mem *store.MemStorage, res resource.Boxscore, forwards []nhl.SkaterStats, goalies []nhl.GoalieStats) {
	t.Helper()
	data, err := json.Marshal(struct {
		PlayerByGameStats nhl.PlayerByGameStats `json:"playerByGameStats"`
	}{nhl.PlayerByGameStats{HomeTeam: nhl.TeamPlayerStats{Forwards: forwards, Goalies: goalies}}})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), res.Path(), data))
}

// savedReports records the last progress report each workflow saved.
type savedReports struct {
	mu   sync.Mutex
	last map[string]shared.ProgressReport
}

func recordSavedReports(env *testsuite.TestWorkflowEnvironment) *savedReports {
	saved := &savedReports{last: make(map[string]shared.ProgressReport)}
	env.OnActivity(((*shared.ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, workflowID, _ string, data []byte) error {
			var report shared.ProgressReport
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
				return err
			}
			saved.mu.Lock()
			defer saved.mu.Unlock()
			saved.last[workflowID] = report
			return nil
		}).Maybe()
	return saved
}

// recordImportedPlayers answers every ImportPlayerGameLogsBatch as fully
// imported and returns how often each player was imported.
func recordImportedPlayers(env *testsuite.TestWorkflowEnvironment) func() map[int64]int {
	var mu sync.Mutex
	imported := make(map[int64]int)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).
		Return(func(_ context.Context, in worknhl.ImportPlayerGameLogsBatchInput) (*worknhl.ImportPlayerGameLogsBatchResult, error) {
			mu.Lock()
			defer mu.Unlock()
			for _, id := range in.PlayerIDs {
				imported[id]++
			}
			return &worknhl.ImportPlayerGameLogsBatchResult{PlayersProcessed: len(in.PlayerIDs)}, nil
		})
	return func() map[int64]int {
		mu.Lock()
		defer mu.Unlock()
		return imported
	}
}

func newImportPlayerLogsEnv(t *testing.T, seasons ...nhl.SeasonInfo) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportPlayerLogsWorkflow)
	env.RegisterWorkflow(ImportSeasonPlayerLogsV2Workflow)
	var sa *worknhl.SeasonsActivities
	env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).
		Return(worknhl.FetchSeasonsManifestResult{Seasons: seasons, Origin: core.OriginFileSystem}, nil)
	return env
}

func queryProgressReport(t *testing.T, env *testsuite.TestWorkflowEnvironment) shared.ProgressReport {
	t.Helper()
	value, err := env.QueryWorkflow(shared.ProgressReportQueryName)
	require.NoError(t, err)
	var report shared.ProgressReport
	require.NoError(t, value.Get(&report))
	return report
}

// TestImportPlayerLogsBarsMatchImportedSkaters runs the real scan over
// boxscores that repeat skaters, list a goalie and include an unreadable
// file: the season's parent bar, the child's bar and the imported players
// are the same unique skaters, each bar completes exactly, and the scan
// reports the unreadable boxscore.
func TestImportPlayerLogsBarsMatchImportedSkaters(t *testing.T) {
	mem := store.NewMemStorage()
	want := seedPlayerLogsBoxscores(t, mem)
	env := newImportPlayerLogsEnv(t, playerLogsFixtureSeason())
	env.RegisterActivity(&worknhl.ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)})
	saved := recordSavedReports(env)
	imported := recordImportedPlayers(env)

	env.ExecuteWorkflow(ImportPlayerLogsWorkflow, &model.SeasonsInput{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	counts := imported()
	got := make([]int64, 0, len(counts))
	for id, n := range counts {
		assert.Equal(t, 1, n, "player %d imported more than once", id)
		got = append(got, id)
	}
	slices.Sort(got)
	assert.Equal(t, want, got)

	report := queryProgressReport(t, env)
	importBars := report.Groups[GroupImportPlayerLogsImport].Bars
	require.Len(t, importBars, 1)
	assert.Equal(t, shared.ProgressBar{
		Label: playerLogsFixtureSeason().Label(), Current: len(want), Total: len(want), Started: true,
		ProgressSourceKey: WorkflowIDImportSeasonPlayerLogs(playerLogsFixtureYear),
	}, importBars[0])
	assert.Contains(t, report.Groups[GroupImportPlayerLogsScan].CompletedMsg, "1 unreadable boxscores.")

	child, ok := saved.last[WorkflowIDImportSeasonPlayerLogs(playerLogsFixtureYear)]
	require.True(t, ok, "child saved no progress report")
	assert.Equal(t, len(want), child.Total)
	childBar := child.Groups[GroupImportPlayerLogs].Bars[0]
	assert.Equal(t, len(want), childBar.Total)
	assert.Equal(t, len(want), childBar.Current)
}

func TestImportPlayerLogsSizesEachSeasonFromItsManifest(t *testing.T) {
	seasons := []nhl.SeasonInfo{
		testSeasonW(2022, "2022-10-07", "2022-10-09"),
		testSeasonW(2023, "2023-10-10", "2023-10-12"),
	}
	manifests := map[nhl.Season][]int64{seasons[0].ID: {1, 2}, seasons[1].ID: {1, 2, 3}}
	env := newImportPlayerLogsEnv(t, seasons...)
	recordSavedReports(env)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.CollectSeasonSkaters, mock.Anything, mock.Anything).
		Return(func(_ context.Context, in worknhl.CollectSeasonPlayerIDsInput) (worknhl.SeasonSkaterManifest, error) {
			return worknhl.SeasonSkaterManifest{PlayerIDs: manifests[in.Season.ID]}, nil
		})
	for _, s := range seasons {
		env.OnWorkflow(ImportSeasonPlayerLogsV2Workflow, mock.Anything,
			ImportSeasonPlayerLogsInput{Season: s, PlayerIDs: manifests[s.ID]}).Return(core.OriginCounts{}, nil).Once()
	}

	env.ExecuteWorkflow(ImportPlayerLogsWorkflow, &model.SeasonsInput{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)

	report := queryProgressReport(t, env)
	scanBar := report.Groups[GroupImportPlayerLogsScan].Bars[0]
	assert.Equal(t, [2]int{len(seasons), len(seasons)}, [2]int{scanBar.Current, scanBar.Total})
	for i, bar := range report.Groups[GroupImportPlayerLogsImport].Bars {
		n := len(manifests[seasons[i].ID])
		assert.Equal(t, [2]int{n, n}, [2]int{bar.Current, bar.Total}, seasons[i].Label())
	}
}

func TestImportPlayerLogsNoSeasons(t *testing.T) {
	env := newImportPlayerLogsEnv(t)
	recordSavedReports(env)

	env.ExecuteWorkflow(ImportPlayerLogsWorkflow, &model.SeasonsInput{})
	require.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestImportPlayerLogsScanErrorFailsBeforeAnyChild(t *testing.T) {
	env := newImportPlayerLogsEnv(t, playerLogsFixtureSeason())
	recordSavedReports(env)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.CollectSeasonSkaters, mock.Anything, mock.Anything).
		Return(worknhl.SeasonSkaterManifest{}, errors.New("storage down"))

	env.ExecuteWorkflow(ImportPlayerLogsWorkflow, &model.SeasonsInput{})
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "list skaters of")
}

func TestScanSummary(t *testing.T) {
	assert.Equal(t, "Listed 3 season skaters from 2 seasons in 1s.",
		scanSummary([]worknhl.SeasonSkaterManifest{{PlayerIDs: []int64{1}}, {PlayerIDs: []int64{1, 2}}}, "1s"))
	assert.Equal(t, "Listed 0 season skaters from 1 seasons in 1s. 1 unreadable schedules. 2 boxscores not cached. 3 unreadable boxscores.",
		scanSummary([]worknhl.SeasonSkaterManifest{{UnreadableSchedules: 1, MissingBoxscores: 2, UnreadableBoxscores: 3}}, "1s"))
}

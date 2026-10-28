package nhl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// seedSchedule caches a day's schedule of final regular-season games.
func seedSchedule(t *testing.T, mem *store.MemStorage, day time.Time, games ...nhlapi.GameID) {
	t.Helper()
	schedule := &nhlapi.DailySchedule{}
	for _, id := range games {
		schedule.Games = append(schedule.Games, nhlapi.ScheduleGame{ID: id, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateOff})
	}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))
}

// seedTeams caches a boxscore with the given home and away player stats.
func seedTeams(t *testing.T, mem *store.MemStorage, res resource.Boxscore, home, away nhlapi.TeamPlayerStats) {
	t.Helper()
	data, err := json.Marshal(struct {
		PlayerByGameStats nhlapi.PlayerByGameStats `json:"playerByGameStats"`
	}{nhlapi.PlayerByGameStats{HomeTeam: home, AwayTeam: away}})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), res.Path(), data))
}

func skater(id int64) nhlapi.SkaterStats { return nhlapi.SkaterStats{PlayerID: nhlapi.PlayerID(id)} }

func collectSkaters(t *testing.T, mem *store.MemStorage, first, last time.Time) SeasonSkaterManifest {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	act := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	env.RegisterActivity(act.CollectSeasonSkaters)
	future, err := env.ExecuteActivity(act.CollectSeasonSkaters, CollectSeasonPlayerIDsInput{
		Season: nhlapi.SeasonInfo{
			ID:             nhlapi.NewSeason(2023),
			StandingsStart: nhlapi.DateFromTime(first),
			StandingsEnd:   nhlapi.DateFromTime(last),
		},
		EndDate: last,
	})
	require.NoError(t, err)
	var manifest SeasonSkaterManifest
	require.NoError(t, future.Get(&manifest))
	return manifest
}

// TestCollectSeasonSkatersListsEachSkaterOnce scans boxscores that repeat
// skaters across games, teams and positions and list goalies, next to an
// unreadable schedule, a missing and an unreadable boxscore: each skater is
// listed once, in order, goalies are not, and every skipped file is counted.
func TestCollectSeasonSkatersListsEachSkaterOnce(t *testing.T) {
	mem := store.NewMemStorage()
	day1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	game1, game2 := nhlapi.GameID(2023020001), nhlapi.GameID(2023020002)
	missing, corrupt := nhlapi.GameID(2023020003), nhlapi.GameID(2023020004)

	seedSchedule(t, mem, day1, game1)
	seedSchedule(t, mem, day2, game2, missing, corrupt)
	require.NoError(t, mem.Write(context.Background(), resource.DailySchedule{Date: day3}.Path(), []byte("{not json")))
	seedTeams(t, mem, resource.Boxscore{Date: day1, GameID: game1},
		nhlapi.TeamPlayerStats{Forwards: []nhlapi.SkaterStats{skater(30), skater(10)}, Defense: []nhlapi.SkaterStats{skater(20)},
			Goalies: []nhlapi.GoalieStats{{PlayerID: 90}}},
		nhlapi.TeamPlayerStats{Forwards: []nhlapi.SkaterStats{skater(10)}})
	seedTeams(t, mem, resource.Boxscore{Date: day2, GameID: game2},
		nhlapi.TeamPlayerStats{Defense: []nhlapi.SkaterStats{skater(30), skater(40)}, Goalies: []nhlapi.GoalieStats{{PlayerID: 91}}},
		nhlapi.TeamPlayerStats{Forwards: []nhlapi.SkaterStats{skater(20)}})
	require.NoError(t, mem.Write(context.Background(), resource.Boxscore{Date: day2, GameID: corrupt}.Path(), []byte("{not json")))

	manifest := collectSkaters(t, mem, day1, day3)

	assert.Equal(t, SeasonSkaterManifest{
		PlayerIDs:           []int64{10, 20, 30, 40},
		UnreadableSchedules: 1,
		MissingBoxscores:    1,
		UnreadableBoxscores: 1,
	}, manifest)
	assert.True(t, manifest.Incomplete())
}

func TestCollectSeasonSkatersCompleteCache(t *testing.T) {
	mem := store.NewMemStorage()
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	game := nhlapi.GameID(2023020001)
	seedSchedule(t, mem, day, game)
	seedTeams(t, mem, resource.Boxscore{Date: day, GameID: game},
		nhlapi.TeamPlayerStats{Forwards: []nhlapi.SkaterStats{skater(10)}}, nhlapi.TeamPlayerStats{})

	manifest := collectSkaters(t, mem, day, day.AddDate(0, 0, 1))

	assert.Equal(t, []int64{10}, manifest.PlayerIDs)
	assert.False(t, manifest.Incomplete())
}

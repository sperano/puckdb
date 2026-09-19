package nhl

import (
	"context"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// fetchClubStatsFixture wires a SeasonsActivities over mem with one team and
// the given cached club schedule, returning the mock client for expectations.
func fetchClubStatsFixture(t *testing.T, mem *store.MemStorage, schedule *nhlapi.TeamScheduleResponse) (*SeasonsActivities, *MockNHLClient) {
	t.Helper()
	if schedule != nil {
		require.NoError(t, resource.WriteParsed(context.Background(), mem,
			resource.ClubScheduleSeason{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}, schedule))
	}
	queries := new(MockClubStatsUpserter)
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, int32(nhlapi.NewSeason(testAggregateSeason).ID())).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 28, Abbrev: testAggregateTeam}}, nil)
	client := new(MockNHLClient)
	return &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), NHLClient: client, ClubStatsQueries: queries}, client
}

func runFetchClubStats(t *testing.T, activities *SeasonsActivities) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.FetchClubStats)
	_, err := env.ExecuteActivity(activities.FetchClubStats, FetchClubStatsInput{Season: testAggregateSeason})
	require.NoError(t, err)
}

func clubStatsPath(gameType nhlapi.GameType) string {
	return resource.ClubStatsResource{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam, GameType: gameType.Int()}.Path()
}

// TestFetchClubStats_RefetchesMidSeasonSnapshot reproduces the 2025-26
// incident: both club-stats files were cached on April 1, the season ended
// April 16, and the cached schedule now shows every game final. Both files
// must be refetched.
func TestFetchClubStats_RefetchesMidSeasonSnapshot(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	stale := []byte(`{"season":20252026,"gameType":2,"skaters":[],"goalies":[]}`)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypeRegularSeason), stale, testMidSeasonAt)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypePlayoffs), stale, testMidSeasonAt)
	activities, client := fetchClubStatsFixture(t, mem, completeSchedule())

	for _, gameType := range gameTypesToFetch {
		fresh := &nhlapi.ClubStats{Season: nhlapi.NewSeason(testAggregateSeason), GameType: gameType,
			Skaters: []nhlapi.ClubSkaterStats{{PlayerID: 8484801, GamesPlayed: 82}}}
		client.On("ClubStats", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason), gameType).Return(fresh, nil).Once()
	}

	runFetchClubStats(t, activities)

	client.AssertExpectations(t)
	got, err := resource.ReadParsed(context.Background(), mem, resource.ClubStatsResource{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam, GameType: nhlapi.GameTypeRegularSeason.Int()})
	require.NoError(t, err)
	require.Len(t, got.Skaters, 1)
	assert.Equal(t, 82, got.Skaters[0].GamesPlayed)
}

// TestFetchClubStats_KeepsSettledFiles checks that files fetched well after
// the last game are served from cache without touching the NHL API.
func TestFetchClubStats_KeepsSettledFiles(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	settled := []byte(`{"season":20252026,"gameType":2,"skaters":[],"goalies":[]}`)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypeRegularSeason), settled, testSettledAt)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypePlayoffs), settled, testSettledAt)
	activities, client := fetchClubStatsFixture(t, mem, completeSchedule())

	runFetchClubStats(t, activities)

	client.AssertNotCalled(t, "ClubStats", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestFetchClubStats_RegularSeasonSettledPlayoffsLive checks the per-game-type
// decision: with the regular season over but playoffs still running, only
// the playoff file is refetched.
func TestFetchClubStats_RegularSeasonSettledPlayoffsLive(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	cachedAt := mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay)
	cached := []byte(`{"season":20252026,"gameType":2,"skaters":[],"goalies":[]}`)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypeRegularSeason), cached, cachedAt)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypePlayoffs), cached, cachedAt)
	schedule := completeSchedule()
	schedule.Games = append(schedule.Games,
		scheduleGame(4, nhlapi.GameTypePlayoffs, nhlapi.GameStateFuture, "UTA", testAggregateTeam, "2026-04-22"))
	activities, client := fetchClubStatsFixture(t, mem, schedule)

	client.On("ClubStats", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason), nhlapi.GameTypePlayoffs).
		Return(&nhlapi.ClubStats{Season: nhlapi.NewSeason(testAggregateSeason), GameType: nhlapi.GameTypePlayoffs}, nil).Once()

	runFetchClubStats(t, activities)

	client.AssertExpectations(t)
	client.AssertNotCalled(t, "ClubStats", mock.Anything, mock.Anything, mock.Anything, nhlapi.GameTypeRegularSeason)
}

// TestFetchClubStats_KeepsStaleFileWhenRefetchFails guards the force-fetch
// design: a stale file is only replaced once a fresh copy is written. A 404
// (a team the API no longer serves) or a transient failure must leave the
// old file in place rather than silently dropping the team from the import.
func TestFetchClubStats_KeepsStaleFileWhenRefetchFails(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	stale := []byte(`{"season":20252026,"gameType":2,"skaters":[],"goalies":[]}`)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypeRegularSeason), stale, testMidSeasonAt)
	mem.SetFileWithTime(clubStatsPath(nhlapi.GameTypePlayoffs), stale, testMidSeasonAt)
	activities, client := fetchClubStatsFixture(t, mem, nil)
	client.On("ClubStats", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason), mock.Anything).
		Return(nil, nhlapi.ErrNotFound)

	runFetchClubStats(t, activities)

	for _, gameType := range gameTypesToFetch {
		got, err := mem.Read(context.Background(), clubStatsPath(gameType))
		require.NoError(t, err)
		assert.Equal(t, stale, got)
	}
}

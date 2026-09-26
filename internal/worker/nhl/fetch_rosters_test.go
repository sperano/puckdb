package nhl

import (
	"context"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestFetchSeasonRosters_RefetchesWhileSeasonRuns checks that a roster cached
// while the team still has games to play is refetched: rosters change with
// trades and call-ups until the last game.
func TestFetchSeasonRosters_RefetchesWhileSeasonRuns(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	res := resource.SeasonRoster{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}
	mem.SetFileWithTime(res.Path(), []byte(`{}`), testMidSeasonAt)
	schedule := completeSchedule()
	schedule.Games = append(schedule.Games,
		scheduleGame(4, nhlapi.GameTypePlayoffs, nhlapi.GameStateFuture, "UTA", testAggregateTeam, "2026-04-22"))
	require.NoError(t, resource.WriteParsed(context.Background(), mem,
		resource.ClubScheduleSeason{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}, schedule))

	queries := new(MockSeasonRosterUpserter)
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, int32(nhlapi.NewSeason(testAggregateSeason).ID())).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 28, Abbrev: testAggregateTeam}}, nil)
	client := new(MockNHLClient)
	client.On("RosterSeason", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason)).
		Return(&nhlapi.Roster{}, nil).Once()
	activities := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), NHLClient: client, RosterQueries: queries}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.FetchSeasonRosters)
	_, err := env.ExecuteActivity(activities.FetchSeasonRosters, FetchSeasonRostersInput{Season: testAggregateSeason})
	require.NoError(t, err)
	client.AssertExpectations(t)
}

// runFetchSeasonRosters runs FetchSeasonRosters for the test team over mem,
// whose club schedule is complete and settled, with client as the NHL API.
func runFetchSeasonRosters(t *testing.T, mem *store.MemStorage, client *MockNHLClient) {
	t.Helper()
	require.NoError(t, resource.WriteParsed(context.Background(), mem,
		resource.ClubScheduleSeason{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}, completeSchedule()))
	queries := new(MockSeasonRosterUpserter)
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, int32(nhlapi.NewSeason(testAggregateSeason).ID())).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 28, Abbrev: testAggregateTeam}}, nil)
	activities := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), NHLClient: client, RosterQueries: queries}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.FetchSeasonRosters)
	_, err := env.ExecuteActivity(activities.FetchSeasonRosters, FetchSeasonRostersInput{Season: testAggregateSeason})
	require.NoError(t, err)
}

// TestFetchSeasonRosters_RefetchesRosterWithoutPositions checks that a roster
// cached before positions were decoded (under the old empty "position" key) is
// refetched even though the season is over, and that the refetched positions
// replace the cached copy.
func TestFetchSeasonRosters_RefetchesRosterWithoutPositions(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	res := resource.SeasonRoster{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}
	mem.SetFileWithTime(res.Path(), []byte(`{"forwards": [{"id": 1, "position": ""}]}`), testSettledAt)
	fresh := &nhlapi.Roster{Forwards: []nhlapi.RosterPlayer{{ID: 1, Position: nhlapi.PositionCenter}}}
	client := new(MockNHLClient)
	client.On("RosterSeason", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason)).
		Return(fresh, nil).Once()

	runFetchSeasonRosters(t, mem, client)

	client.AssertExpectations(t)
	cached, err := resource.ReadParsed(context.Background(), mem, res)
	require.NoError(t, err)
	assert.Equal(t, nhlapi.PositionCenter, cached.Forwards[0].Position)
}

// TestFetchSeasonRosters_KeepsSettledRosterWithPositions checks that a roster
// with positions, cached after the season settled, is not refetched.
func TestFetchSeasonRosters_KeepsSettledRosterWithPositions(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	res := resource.SeasonRoster{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam}
	mem.SetFileWithTime(res.Path(), []byte(`{"forwards": [{"id": 1, "positionCode": "C"}, {"id": 2, "positionCode": ""}]}`), testSettledAt)
	client := new(MockNHLClient)

	runFetchSeasonRosters(t, mem, client)

	client.AssertNotCalled(t, "RosterSeason", mock.Anything, mock.Anything, mock.Anything)
}

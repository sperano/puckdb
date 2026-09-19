package nhl

import (
	"context"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
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

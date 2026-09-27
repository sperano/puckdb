package nhl

import (
	"context"
	"encoding/json"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// The activities select against the wall clock, so their fixtures use a
// season far enough ahead to always be upcoming.
const (
	upcomingYear       = 2099
	upcomingPriorYear  = upcomingYear - 1
	upcomingTeamID     = 8
	upcomingRosterTeam = "MTL"
	upcomingMissing    = "TOR"
	utahHockeyClubID   = 59
	utahMammothID      = 68
)

type mockUpcomingQueries struct {
	mock.Mock
}

func (m *mockUpcomingQueries) UpsertSeason(ctx context.Context, arg sqlcdb.UpsertSeasonParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *mockUpcomingQueries) CarryForwardSeasonTeam(ctx context.Context, arg sqlcdb.CarryForwardSeasonTeamParams) (int64, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(int64), args.Error(1)
}

func upcomingManifest() []nhlapi.SeasonInfo {
	return []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2025), StandingsStart: d("2025-10-07"), StandingsEnd: d("2026-04-16")},
		{ID: nhlapi.NewSeason(upcomingYear + 1), StandingsStart: d("2100-10-05"), StandingsEnd: d("2101-04-15")},
		{ID: nhlapi.NewSeason(upcomingYear), StandingsStart: d("2099-10-06"), StandingsEnd: d("2100-04-15")},
	}
}

func upcomingStorage(t *testing.T) *store.MemStorage {
	t.Helper()
	mem := store.NewMemStorage()
	require.NoError(t, resource.WriteParsed(context.Background(), mem, resource.SeasonsManifest{},
		nhlapi.SeasonsResponse{Seasons: upcomingManifest()}))
	return mem
}

func TestSelectUpcomingSeason(t *testing.T) {
	t.Parallel()
	now := d("2099-09-20").Time

	upcoming, found := selectUpcomingSeason(upcomingManifest(), nil, now)
	require.True(t, found)
	assert.Equal(t, upcomingYear, upcoming.ID.StartYear(), "the earliest season not yet started")

	_, found = selectUpcomingSeason(upcomingManifest(), &model.SeasonsInput{EndSeason: ptr(upcomingPriorYear)}, now)
	assert.False(t, found, "outside the requested range, and 2025-26 has started")

	upcoming, found = selectUpcomingSeason(upcomingManifest(), &model.SeasonsInput{StartSeason: ptr(upcomingYear + 1)}, now)
	require.True(t, found)
	assert.Equal(t, upcomingYear+1, upcoming.ID.StartYear())

	_, found = selectUpcomingSeason(upcomingManifest(), nil, d("2101-01-01").Time)
	assert.False(t, found, "every season has started")
}

func TestFetchUpcomingSeasonRosters_FetchesPriorSeasonClubs(t *testing.T) {
	t.Parallel()
	mem := upcomingStorage(t)
	queries := new(MockSeasonRosterUpserter)
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, int32(nhlapi.NewSeason(upcomingPriorYear).ID())).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: upcomingTeamID, Abbrev: upcomingRosterTeam}, {TeamID: 10, Abbrev: upcomingMissing}}, nil)
	season := nhlapi.NewSeason(upcomingYear)
	client := new(MockNHLClient)
	client.On("RosterSeason", mock.Anything, upcomingRosterTeam, season).
		Return(&nhlapi.Roster{Forwards: testRosterPlayers()}, nil).Once()
	client.On("RosterSeason", mock.Anything, upcomingMissing, season).Return(nil, nhlapi.ErrNotFound).Once()
	activities := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), NHLClient: client, RosterQueries: queries}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.FetchUpcomingSeasonRosters)
	encoded, err := env.ExecuteActivity(activities.FetchUpcomingSeasonRosters, &model.SeasonsInput{EndSeason: ptr(upcomingYear)})
	require.NoError(t, err)
	var result UpcomingSeasonRostersResult
	require.NoError(t, encoded.Get(&result))

	assert.Equal(t, UpcomingSeasonRostersResult{Season: upcomingYear, Teams: 2, TeamsWithRosters: 1}, result)
	assert.True(t, mem.Exists(context.Background(), resource.SeasonRoster{Season: upcomingYear, TeamAbbrev: upcomingRosterTeam}.Path()))
	client.AssertExpectations(t)
}

func TestImportUpcomingSeasonRosters_CarriesClubsForwardAndImports(t *testing.T) {
	t.Parallel()
	mem := upcomingStorage(t)
	data, err := json.Marshal(&nhlapi.Roster{Forwards: testRosterPlayers()})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.SeasonRoster{Season: upcomingYear, TeamAbbrev: upcomingRosterTeam}.Path(), data))

	upcomingID := int32(nhlapi.NewSeason(upcomingYear).ID())
	upcoming := new(mockUpcomingQueries)
	upcoming.On("UpsertSeason", mock.Anything, mock.MatchedBy(func(arg sqlcdb.UpsertSeasonParams) bool {
		return arg.ID == upcomingID && arg.StandingsStart.Time.Equal(d("2099-10-06").Time)
	})).Return(nil).Once()
	priorID := int32(nhlapi.NewSeason(upcomingPriorYear).ID())
	// UTA carried a stale ID: the carry-forward must resolve the upcoming
	// season's (Utah Hockey Club 59 became Utah Mammoth 68).
	for from, to := range map[int64]int64{upcomingTeamID: upcomingTeamID, 10: 10, utahHockeyClubID: utahMammothID} {
		upcoming.On("CarryForwardSeasonTeam", mock.Anything, sqlcdb.CarryForwardSeasonTeamParams{
			ToSeason: upcomingID, ToTeamID: to, FromSeason: priorID, FromTeamID: from,
		}).Return(int64(1), nil).Once()
	}
	queries := new(MockSeasonRosterUpserter)
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, priorID).Return([]sqlcdb.GetSeasonTeamAbbrevsRow{
		{TeamID: upcomingTeamID, Abbrev: upcomingRosterTeam}, {TeamID: 10, Abbrev: upcomingMissing}, {TeamID: utahHockeyClubID, Abbrev: "UTA"},
	}, nil).Once()
	queries.On("GetSeasonTeamAbbrevs", mock.Anything, upcomingID).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: upcomingTeamID, Abbrev: upcomingRosterTeam}, {TeamID: 10, Abbrev: upcomingMissing}}, nil)
	queries.On("EnsurePlayerExistsBatch", mock.Anything, mock.Anything).Return(nil).Once()
	queries.On("UpsertSeasonRosterBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertSeasonRosterBatchParams) bool {
		return len(params) > 0 && params[0].Season == upcomingID && params[0].TeamID == upcomingTeamID
	})).Return(nil).Once()
	activities := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), RosterQueries: queries, UpcomingQueries: upcoming}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.ImportUpcomingSeasonRosters)
	encoded, err := env.ExecuteActivity(activities.ImportUpcomingSeasonRosters, &model.SeasonsInput{EndSeason: ptr(upcomingYear)})
	require.NoError(t, err)
	var result UpcomingSeasonRostersResult
	require.NoError(t, encoded.Get(&result))

	assert.Equal(t, UpcomingSeasonRostersResult{Season: upcomingYear, Teams: 2, TeamsWithRosters: 1}, result)
	upcoming.AssertExpectations(t)
	queries.AssertExpectations(t)
}

func TestImportUpcomingSeasonRosters_NothingUpcomingTouchesNothing(t *testing.T) {
	t.Parallel()
	upcoming := new(mockUpcomingQueries)
	queries := new(MockSeasonRosterUpserter)
	activities := &SeasonsActivities{
		Storage: upcomingStorage(t), GobCache: newImportTestGobCache(), RosterQueries: queries, UpcomingQueries: upcoming,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.ImportUpcomingSeasonRosters)
	encoded, err := env.ExecuteActivity(activities.ImportUpcomingSeasonRosters, &model.SeasonsInput{EndSeason: ptr(upcomingPriorYear)})
	require.NoError(t, err)
	var result UpcomingSeasonRostersResult
	require.NoError(t, encoded.Get(&result))

	assert.Zero(t, result)
	upcoming.AssertNotCalled(t, "CarryForwardSeasonTeam", mock.Anything, mock.Anything)
	queries.AssertNotCalled(t, "GetSeasonTeamAbbrevs", mock.Anything, mock.Anything)
}

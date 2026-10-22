package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	importStandingsDate     = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	importStandingsCaptured = time.Date(2026, 11, 15, 17, 4, 5, 0, time.UTC)
)

func importStandingsInput(teams ...TeamInfo) ImportYahooDataForDateInput {
	return ImportYahooDataForDateInput{Season: yahoofixtures.PointsSeason, Teams: teams, Date: importStandingsDate}
}

func pointsTeam(teamID int) TeamInfo {
	return TeamInfo{LeagueID: yahoofixtures.PointsLeagueID, TeamID: teamID}
}

func writeStandings(mem *store.MemStorage, leagueID int, fixture string) {
	res := standingsResource(yahoofixtures.PointsSeason, leagueID, importStandingsDate)
	mem.SetFileWithTime(res.Path(), yahoofixtures.Read(fixture), importStandingsCaptured)
}

func TestDateInputs_ListsOneStandingsSnapshotPerLeague(t *testing.T) {
	t.Parallel()
	input := importStandingsInput(pointsTeam(1), TeamInfo{LeagueID: 9, TeamID: 4}, pointsTeam(2))

	paths := make([]string, 0)
	for _, r := range DateInputs(input) {
		paths = append(paths, r.Path())
	}

	assert.Equal(t, []string{
		resource.Roster{LeagueID: yahoofixtures.PointsLeagueID, TeamID: 1, Date: importStandingsDate}.Path(),
		resource.TeamSummary{LeagueID: yahoofixtures.PointsLeagueID, TeamID: 1, Date: importStandingsDate}.Path(),
		resource.Roster{LeagueID: 9, TeamID: 4, Date: importStandingsDate}.Path(),
		resource.TeamSummary{LeagueID: 9, TeamID: 4, Date: importStandingsDate}.Path(),
		resource.Roster{LeagueID: yahoofixtures.PointsLeagueID, TeamID: 2, Date: importStandingsDate}.Path(),
		resource.TeamSummary{LeagueID: yahoofixtures.PointsLeagueID, TeamID: 2, Date: importStandingsDate}.Path(),
		standingsResource(yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, importStandingsDate).Path(),
		standingsResource(yahoofixtures.PointsSeason, 9, importStandingsDate).Path(),
	}, paths)
}

func TestCollectStandingParams_ConvertsTheConfiguredTeams(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	writeStandings(mem, yahoofixtures.PointsLeagueID, yahoofixtures.H2HStandings)
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	origins := core.OriginCounts{}

	// Teams 3 and 1 of the four ranked; 2 and 4 are not configured.
	leagues, failures := a.collectStandingParams(context.Background(), importStandingsInput(pointsTeam(1), pointsTeam(3)), origins)

	require.Zero(t, failures)
	require.Len(t, leagues, 1)
	assert.Equal(t, yahoofixtures.PointsLeagueID, leagues[0].leagueID)
	date := pgDate(importStandingsDate)
	captured := pgtype.Timestamptz{Time: importStandingsCaptured, Valid: true}
	assert.Equal(t, []sqlcdb.UpsertYahooTeamStandingBatchParams{
		{
			LeagueID: yahoofixtures.PointsLeagueID, TeamID: 3, Date: date, StandingsCapturedAt: captured,
			StandingsRank: pgtype.Int4{Int32: 1, Valid: true}, StandingsPlayoffSeed: pgtype.Int4{Int32: 1, Valid: true},
			H2hWins: countInt4(4), H2hLosses: countInt4(1), H2hTies: countInt4(0),
			H2hPercentage: pgtype.Float8{Float64: 0.8, Valid: true},
		},
		{
			LeagueID: yahoofixtures.PointsLeagueID, TeamID: 1, Date: date, StandingsCapturedAt: captured,
			StandingsRank: pgtype.Int4{Int32: 2, Valid: true}, StandingsPlayoffSeed: pgtype.Int4{Int32: 2, Valid: true},
			H2hWins: countInt4(3), H2hLosses: countInt4(1), H2hTies: countInt4(1),
			H2hPercentage:   pgtype.Float8{Float64: 0.7, Valid: true},
			H2hDivisionWins: countInt4(2), H2hDivisionLosses: countInt4(0), H2hDivisionTies: countInt4(0),
		},
	}, leagues[0].rows, "Yahoo's rank order, the capture time from the file")
	assert.Equal(t, 1, origins[core.OriginFileSystem])
}

func TestCollectStandingParams_PreseasonAndRotoLeaveNulls(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	writeStandings(mem, yahoofixtures.PointsLeagueID, yahoofixtures.PreseasonStandings)
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	leagues, failures := a.collectStandingParams(context.Background(), importStandingsInput(pointsTeam(1)), core.OriginCounts{})

	require.Zero(t, failures)
	require.Len(t, leagues, 1)
	require.Len(t, leagues[0].rows, 1)
	row := leagues[0].rows[0]
	assert.False(t, row.StandingsRank.Valid, "an empty rank is NULL")
	assert.False(t, row.StandingsPlayoffSeed.Valid)
	assert.Equal(t, countInt4(0), row.H2hWins, "a 0-0-0 record is kept")
	assert.False(t, row.H2hPercentage.Valid, "an empty percentage is NULL")
	assert.True(t, row.StandingsCapturedAt.Valid)

	roto, err := standingParams(yahoofixtures.RotoLeagueID, pgDate(importStandingsDate),
		pgtype.Timestamptz{Time: importStandingsCaptured, Valid: true},
		resource.TeamStandingRecord{TeamID: 1, Rank: 2})
	require.NoError(t, err)
	assert.Equal(t, pgtype.Int4{Int32: 2, Valid: true}, roto.StandingsRank)
	assert.False(t, roto.H2hWins.Valid, "a league without a record has NULL record columns")
	assert.False(t, roto.H2hDivisionWins.Valid)
}

func TestCollectStandingParams_MissingAndUnreadableSnapshots(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	const unreadableLeague = 9
	mem.SetFile(standingsResource(yahoofixtures.PointsSeason, unreadableLeague, importStandingsDate).Path(), []byte("<not xml"))
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	leagues, failures := a.collectStandingParams(context.Background(),
		importStandingsInput(pointsTeam(1), TeamInfo{LeagueID: unreadableLeague, TeamID: 1}), core.OriginCounts{})

	assert.Empty(t, leagues, "no snapshot for the points league: nothing to write")
	assert.Equal(t, 1, failures, "the unreadable snapshot is a read failure")
}

// A file whose league is not the requested one fails its parse (the path
// names league 9, the document league 77777).
func TestCollectStandingParams_ForeignLeagueIsAReadFailure(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	writeStandings(mem, 9, yahoofixtures.H2HStandings)
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}

	leagues, failures := a.collectStandingParams(context.Background(),
		importStandingsInput(TeamInfo{LeagueID: 9, TeamID: 1}), core.OriginCounts{})

	assert.Empty(t, leagues)
	assert.Equal(t, 1, failures)
}

func TestStandingParams_RefusesAPercentageOutsideTheUnitRange(t *testing.T) {
	t.Parallel()
	tooHigh := 1.5
	_, err := standingParams(1, pgDate(importStandingsDate), pgtype.Timestamptz{Time: importStandingsCaptured, Valid: true},
		resource.TeamStandingRecord{TeamID: 1, Rank: 1, Outcome: &resource.OutcomeRecord{Wins: 1, Percentage: &tooHigh}})

	require.ErrorContains(t, err, "outside [0, 1]")
}

func TestImportStandings_WritesRowsThenClearsTheOtherTeams(t *testing.T) {
	t.Parallel()
	q := &MockQueries{}
	date := pgDate(importStandingsDate)
	leagues := []leagueStandingRows{
		{leagueID: 1, rows: []sqlcdb.UpsertYahooTeamStandingBatchParams{{LeagueID: 1, TeamID: 3, Date: date}, {LeagueID: 1, TeamID: 1, Date: date}}},
		{leagueID: 2}, // a snapshot without configured teams clears every team
	}
	q.On("UpsertYahooTeamStandingBatch", mock.Anything, append(leagues[0].rows[:0:0], leagues[0].rows...)).
		Return(sqlcdb.NewUpsertYahooTeamStandingBatchBatchResults(&mockBatchResults{}, 2)).Once()
	q.On("ClearYahooTeamStandingsExcept", mock.Anything,
		sqlcdb.ClearYahooTeamStandingsExceptParams{LeagueID: 1, Date: date, TeamIds: []int32{3, 1}}).Return(nil).Once()
	q.On("ClearYahooTeamStandingsExcept", mock.Anything, mock.MatchedBy(func(p sqlcdb.ClearYahooTeamStandingsExceptParams) bool {
		// A nil array would be NULL in SQL, and "<> ALL(NULL)" clears nothing.
		return p.LeagueID == 2 && p.TeamIds != nil && len(p.TeamIds) == 0
	})).Return(nil).Once()

	written, err := importStandings(context.Background(), q, importStandingsDate, leagues)

	require.NoError(t, err)
	assert.Equal(t, 2, written)
	q.AssertExpectations(t)
}

func TestImportStandings_ClearErrorFails(t *testing.T) {
	t.Parallel()
	q := &MockQueries{}
	q.On("ClearYahooTeamStandingsExcept", mock.Anything, mock.Anything).Return(assert.AnError)

	_, err := importStandings(context.Background(), q, importStandingsDate, []leagueStandingRows{{leagueID: 1}})

	require.ErrorIs(t, err, assert.AnError)
	q.AssertNotCalled(t, "UpsertYahooTeamStandingBatch", mock.Anything, mock.Anything)
}

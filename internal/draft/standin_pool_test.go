package draft

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	standInTestLeagueSeason = 2026 // start year; NHL season ID 20262027
	standInTestRosterSeason = 20262027
	standInTestPriorSeason  = 20252026
	// standInTestFloorSeason is DefaultLookbackSeasons (3) seasons before
	// standInTestRosterSeason's start year (2026-3=2023): 20232024.
	standInTestFloorSeason = 20232024
	standInTestNHLTeams    = 32
)

var standInTestUpdatedAt = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

func standInRow(playerID, teamID int64, position sqlcdb.PlayerPosition, updatedAt time.Time, skaterHistory, goalieHistory bool) sqlcdb.ListSeasonRosterPoolCandidatesRow {
	return standInRowWithPosition(playerID, teamID, sqlcdb.NullPlayerPosition{PlayerPosition: position, Valid: true}, updatedAt, skaterHistory, goalieHistory)
}

// standInRowWithPosition builds a row with an explicit (possibly NULL)
// position, standing in for whatever ListSeasonRosterPoolCandidates'
// COALESCE(r.position, p.position) resolved to.
func standInRowWithPosition(playerID, teamID int64, position sqlcdb.NullPlayerPosition, updatedAt time.Time, skaterHistory, goalieHistory bool) sqlcdb.ListSeasonRosterPoolCandidatesRow {
	return sqlcdb.ListSeasonRosterPoolCandidatesRow{
		PlayerID: playerID, TeamID: teamID, Position: position,
		UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true},
		FirstName: "Player", LastName: "P" + strconv.FormatInt(playerID, 10), TeamAbbrev: "TM" + strconv.FormatInt(teamID, 10),
		HasSkaterHistory: skaterHistory, HasGoalieHistory: goalieHistory,
	}
}

// standInFullCoverage reports every NHL team of a season as having a roster
// row, so standInRosterSeason picks the season itself rather than falling
// back.
func standInFullCoverage() sqlcdb.GetSeasonRosterCoverageRow {
	return sqlcdb.GetSeasonRosterCoverageRow{NhlTeams: standInTestNHLTeams, TeamsWithRosters: standInTestNHLTeams}
}

func TestStandInEligiblePositions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		position sqlcdb.NullPlayerPosition
		want     []string
	}{
		{"center", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}, []string{PositionCenter}},
		{"left wing", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}, []string{PositionLeftWing}},
		{"right wing", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionRW, Valid: true}, []string{PositionRightWing}},
		{"defense", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionD, Valid: true}, []string{PositionDefense}},
		{"goalie", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}, []string{PositionGoalie}},
		{"unspecified forward", sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionF, Valid: true}, nil},
		{"null", sqlcdb.NullPlayerPosition{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, standInEligiblePositions(tt.position))
		})
	}
}

func TestStandInHasHistory(t *testing.T) {
	t.Parallel()
	goalie := standInRow(1, 1, sqlcdb.PlayerPositionG, standInTestUpdatedAt, false, true)
	assert.True(t, standInHasHistory(goalie))

	goalieNoHistory := standInRow(1, 1, sqlcdb.PlayerPositionG, standInTestUpdatedAt, true, false)
	assert.False(t, standInHasHistory(goalieNoHistory), "a goalie's skater history does not count")

	skater := standInRow(2, 1, sqlcdb.PlayerPositionC, standInTestUpdatedAt, true, false)
	assert.True(t, standInHasHistory(skater))

	skaterNoHistory := standInRow(2, 1, sqlcdb.PlayerPositionC, standInTestUpdatedAt, false, true)
	assert.False(t, standInHasHistory(skaterNoHistory), "a skater's goalie history does not count")
}

func TestStandInRosterSeason_UsesOwnSeasonWhenComplete(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverage: standInFullCoverage()}
	season, reason, err := standInRosterSeason(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	assert.Equal(t, standInTestRosterSeason, season)
	assert.Empty(t, reason)
}

func TestStandInRosterSeason_FallsBackWhenIncomplete(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverage: sqlcdb.GetSeasonRosterCoverageRow{NhlTeams: 32, TeamsWithRosters: 20}}
	season, reason, err := standInRosterSeason(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	assert.Equal(t, standInTestPriorSeason, season)
	assert.Contains(t, reason, "rosters incomplete: 20 of 32 teams")
}

func TestStandInRosterSeason_FallsBackWhenNoSeasonTeams(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverage: sqlcdb.GetSeasonRosterCoverageRow{}}
	season, reason, err := standInRosterSeason(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	assert.Equal(t, standInTestPriorSeason, season)
	assert.Contains(t, reason, "has no NHL teams imported")
}

func TestStandInRosterSeason_CoverageError(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverageErr: assert.AnError}
	_, _, err := standInRosterSeason(context.Background(), q, standInTestLeagueSeason)
	require.Error(t, err)
}

func TestLoadStandInPool_PassesHistoryWindowAndRosterSeasonToQuery(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverage: standInFullCoverage()}
	_, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	assert.EqualValues(t, standInTestRosterSeason, q.lastListParams.Season)
	assert.EqualValues(t, standInTestFloorSeason, q.lastListParams.MinSeason,
		"floor = target start year - DefaultLookbackSeasons (2026-3=2023)")
	assert.EqualValues(t, standInTestRosterSeason, q.lastListParams.TargetSeason,
		"target season excludes the league's own season's games from history, since they have not been played as history yet")
}

func TestLoadStandInPool_ListRowsError(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{rosterCoverage: standInFullCoverage(), rosterRowsErr: assert.AnError}
	_, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.Error(t, err)
}

func TestLoadStandInPool_DedupesMostRecentRosterRow(t *testing.T) {
	t.Parallel()
	older := standInTestUpdatedAt.Add(-24 * time.Hour)
	q := &fakeQueries{
		rosterCoverage: standInFullCoverage(),
		// Traded player rostered by two teams in the season: rows arrive in
		// the query's own order (player_id, updated_at DESC, team_id).
		rosterRows: []sqlcdb.ListSeasonRosterPoolCandidatesRow{
			standInRow(100, 2, sqlcdb.PlayerPositionC, standInTestUpdatedAt, true, false),
			standInRow(100, 1, sqlcdb.PlayerPositionC, older, true, false),
		},
	}
	result, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	require.Len(t, result.Players, 1)
	assert.EqualValues(t, 100, result.Players[0].NHLPlayerID)
	assert.Equal(t, StandInPlayerKey(100), result.Players[0].PlayerKey)
	assert.Equal(t, "TM2", result.Players[0].Team, "the most recently updated roster row (team 2) wins")
	assert.True(t, result.Players[0].FetchedAt.Equal(standInTestUpdatedAt))
}

func TestLoadStandInPool_ExcludesNoHistoryPlayers(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{
		rosterCoverage: standInFullCoverage(),
		rosterRows: []sqlcdb.ListSeasonRosterPoolCandidatesRow{
			standInRow(1, 1, sqlcdb.PlayerPositionC, standInTestUpdatedAt, true, false),
			standInRow(2, 1, sqlcdb.PlayerPositionD, standInTestUpdatedAt, false, false),
		},
	}
	result, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	require.Len(t, result.Players, 1)
	assert.EqualValues(t, 1, result.Players[0].NHLPlayerID)
	assert.Empty(t, result.ExcludedNoPosition)
	require.Len(t, result.ExcludedNoHistory, 1)
	assert.EqualValues(t, 2, result.ExcludedNoHistory[0].NHLPlayerID)

	notes := result.Notes()
	require.Len(t, notes, 2)
	assert.Contains(t, notes[1], "excluded 1 of 2 roster players with no NHL regular-season games in 2023-24–2025-26")
}

func TestLoadStandInPool_ExcludesNoPositionPlayers(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{
		rosterCoverage: standInFullCoverage(),
		rosterRows: []sqlcdb.ListSeasonRosterPoolCandidatesRow{
			standInRow(1, 1, sqlcdb.PlayerPositionC, standInTestUpdatedAt, true, false),
			standInRowWithPosition(2, 1, sqlcdb.NullPlayerPosition{}, standInTestUpdatedAt, true, true),
			standInRowWithPosition(3, 1, sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionF, Valid: true}, standInTestUpdatedAt, true, true),
		},
	}
	result, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	require.Len(t, result.Players, 1)
	assert.EqualValues(t, 1, result.Players[0].NHLPlayerID)
	assert.Empty(t, result.ExcludedNoHistory)
	require.Len(t, result.ExcludedNoPosition, 2, "NULL and 'F' positions must be excluded, not passed through unpositioned")
	assert.EqualValues(t, 2, result.ExcludedNoPosition[0].NHLPlayerID)
	assert.EqualValues(t, 3, result.ExcludedNoPosition[1].NHLPlayerID)

	notes := result.Notes()
	require.Len(t, notes, 2)
	assert.Contains(t, notes[1], "excluded 2 of 3 roster players with no known position")
}

func TestLoadStandInPool_MapsYahooIDAndTeam(t *testing.T) {
	t.Parallel()
	row := standInRow(5, 1, sqlcdb.PlayerPositionRW, standInTestUpdatedAt, true, false)
	row.YahooID = pgtype.Int8{Int64: 9001, Valid: true}
	q := &fakeQueries{rosterCoverage: standInFullCoverage(), rosterRows: []sqlcdb.ListSeasonRosterPoolCandidatesRow{row}}
	result, err := LoadStandInPool(context.Background(), q, standInTestLeagueSeason)
	require.NoError(t, err)
	require.Len(t, result.Players, 1)
	p := result.Players[0]
	assert.Equal(t, 9001, p.YahooPlayerID)
	assert.Equal(t, "TM1", p.Team)
	assert.Equal(t, []string{PositionRightWing}, p.EligiblePositions)
}

func TestStandInPoolResult_Notes_FallbackReason(t *testing.T) {
	t.Parallel()
	result := StandInPoolResult{RosterSeason: standInTestPriorSeason, FallbackReason: "2026-27 rosters incomplete: 20 of 32 teams"}
	notes := result.Notes()
	require.Len(t, notes, 2)
	assert.Equal(t, "provisional pool from NHL 2025-26 rosters (Yahoo unavailable); one position per player", notes[0])
	assert.Contains(t, notes[1], "used the prior season's rosters: 2026-27 rosters incomplete: 20 of 32 teams")
}

func TestStandInSeasonLabel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "2026-27", standInSeasonLabel(standInTestRosterSeason))
	assert.Equal(t, "2023-24", standInSeasonLabel(standInTestFloorSeason))
}

func TestLoadLeaguePool_DispatchesBySource(t *testing.T) {
	t.Parallel()

	t.Run("yahoo api uses LoadPool", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueries{players: []sqlcdb.ListYahooLeaguePlayersWithNHLRow{
			{PlayerID: 1, PlayerKey: "465.p.1", FullName: "Yahoo Player"},
		}}
		snapshot := Snapshot{Source: SourceYahooAPI, Rules: Rules{LeagueKey: "465.l.1001", Season: standInTestLeagueSeason}}
		players, notes, err := LoadLeaguePool(context.Background(), q, snapshot)
		require.NoError(t, err)
		require.Len(t, players, 1)
		assert.Equal(t, "465.p.1", players[0].PlayerKey)
		assert.Empty(t, notes)
	})

	t.Run("temporary stand-in uses LoadStandInPool", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueries{
			rosterCoverage: standInFullCoverage(),
			rosterRows:     []sqlcdb.ListSeasonRosterPoolCandidatesRow{standInRow(1, 1, sqlcdb.PlayerPositionC, standInTestUpdatedAt, true, false)},
		}
		snapshot := Snapshot{Source: SourceTemporaryStandIn, Rules: Rules{LeagueKey: "temp.l.1002", Season: standInTestLeagueSeason}}
		players, notes, err := LoadLeaguePool(context.Background(), q, snapshot)
		require.NoError(t, err)
		require.Len(t, players, 1)
		assert.Equal(t, StandInPlayerKey(1), players[0].PlayerKey)
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0], "provisional pool from NHL 2026-27 rosters")
	})
}

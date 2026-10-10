package mcpserver

import (
	"context"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayersByBirthplaceLimit(t *testing.T) {
	tests := map[string]struct {
		limit any
		want  int32
	}{
		"absent":   {want: defaultBirthplaceLimit},
		"zero":     {limit: float64(0), want: defaultBirthplaceLimit},
		"explicit": {limit: float64(7), want: 7},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{"birth_country": "CAN"}
			if tc.limit != nil {
				args["limit"] = tc.limit
			}
			result, db := callNHLTool(t, "get_players_by_birthplace", args)
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			// GetPlayersByBirthplace binds the limit first.
			assert.Equal(t, tc.want, db.calls[0].args[0])
		})
	}

	// The old handler silently replaced a negative limit with the default.
	result, db := callNHLTool(t, "get_players_by_birthplace", map[string]any{"birth_country": "CAN", "limit": float64(-5)})
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid limit -5: want an integer from 0 to 2147483647", resultText(t, result))
	assert.Empty(t, db.calls)
}

const (
	seasonRosterToolName = "get_season_roster"

	// Fixture for the PostgreSQL-backed get_season_roster test: a season
	// and IDs no import uses.
	rosterTestSeason      = 30003001
	rosterTestTeamID      = 9701
	rosterTestOtherTeamID = 9702
	rosterTestCenterID    = 9701001
	rosterTestDefenderID  = 9701002
	rosterTestGoalieID    = 9701003
	rosterTestOtherID     = 9702001
)

func TestSeasonRosterBindsSeasonAndTeam(t *testing.T) {
	const teamID int64 = 8
	result, db := callNHLTool(t, seasonRosterToolName, map[string]any{"team_id": float64(teamID), "season": float64(testSeasonID)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, noResultsText, resultText(t, result))
	require.Len(t, db.calls, 1)
	assert.Contains(t, db.calls[0].sql, "name: GetSeasonRosterByTeam")
	// GetSeasonRosterByTeam binds season, then team_id.
	assert.Equal(t, []any{testSeasonID, teamID}, db.calls[0].args)
}

// TestSeasonRosterFromDatabase runs the tool against the real query: only
// the requested club and season, names joined from players, ordered by
// position then last name.
func TestSeasonRosterFromDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	deleteRosterFixture(t, pool)
	t.Cleanup(func() { deleteRosterFixture(t, pool) })
	seedRosterFixture(t, pool)

	tool := seasonRosterTool(sqlcdb.New(pool))
	result := callTool(t, tool.Handler, map[string]any{"team_id": float64(rosterTestTeamID), "season": float64(rosterTestSeason)})
	text := resultText(t, result)
	require.False(t, result.IsError, text)

	rows := parseCSV(t, text)
	require.Len(t, rows, 4, text)
	header := rows[0]
	var got [][]string
	for _, row := range rows[1:] {
		got = append(got, []string{
			row[columnIndex(t, header, "player_id")],
			row[columnIndex(t, header, "last_name")],
			row[columnIndex(t, header, "position")],
			row[columnIndex(t, header, "team_id")],
			row[columnIndex(t, header, "season")],
		})
	}
	team, season := strconv.Itoa(rosterTestTeamID), strconv.Itoa(rosterTestSeason)
	assert.Equal(t, [][]string{
		{strconv.Itoa(rosterTestCenterID), "Center", "C", team, season},
		{strconv.Itoa(rosterTestDefenderID), "Defender", "D", team, season},
		{strconv.Itoa(rosterTestGoalieID), "Goalie", "G", team, season},
	}, got)
}

// rosterFixturePlayer is one season_rosters row of the fixture.
type rosterFixturePlayer struct {
	id       int64
	teamID   int64
	lastName string
	position sqlcdb.PlayerPosition
}

// rosterFixturePlayers are inserted out of order, so the result order
// comes from the query; the last one plays for another club.
var rosterFixturePlayers = []rosterFixturePlayer{
	{rosterTestGoalieID, rosterTestTeamID, "Goalie", sqlcdb.PlayerPositionG},
	{rosterTestDefenderID, rosterTestTeamID, "Defender", sqlcdb.PlayerPositionD},
	{rosterTestCenterID, rosterTestTeamID, "Center", sqlcdb.PlayerPositionC},
	{rosterTestOtherID, rosterTestOtherTeamID, "Elsewhere", sqlcdb.PlayerPositionC},
}

func seedRosterFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '3000-10-01', '3001-04-30')`, rosterTestSeason)
	for _, teamID := range []int64{rosterTestTeamID, rosterTestOtherTeamID} {
		exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
			VALUES ($1, $2, 'Fixture Club', 'FIX', 'Atlantic', 'A')`, rosterTestSeason, teamID)
	}
	for _, p := range rosterFixturePlayers {
		exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, 'Fixture', $2, $3)`,
			p.id, p.lastName, p.position)
		exec(`INSERT INTO season_rosters (season, team_id, player_id, position, shoots_catches, sweater_number,
			height_inches, weight_pounds, birth_date, birth_country)
			VALUES ($1, $2, $3, $4, 'L', 9, 72, 190, '2980-01-01', 'CAN')`,
			rosterTestSeason, p.teamID, p.id, p.position)
	}
}

// deleteRosterFixture removes the fixture rows, children first.
func deleteRosterFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM season_rosters WHERE season = $1`,
		`DELETE FROM season_teams WHERE season = $1`,
		`DELETE FROM seasons WHERE id = $1`,
	} {
		_, err := pool.Exec(ctx, stmt, rosterTestSeason)
		require.NoError(t, err)
	}
	ids := make([]int64, len(rosterFixturePlayers))
	for i, p := range rosterFixturePlayers {
		ids[i] = p.id
	}
	_, err := pool.Exec(ctx, `DELETE FROM players WHERE id = ANY($1)`, ids)
	require.NoError(t, err)
}

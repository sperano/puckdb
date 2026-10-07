package mcpserver

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	teamsToolName = "get_yahoo_teams_by_league"

	// PostgreSQL-backed test settings (see CLAUDE.md "Database-backed tests").
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBMarker     = "test"
	pgTestLeagueID   = 990763
	pgTestLeagueKey  = "465.l.990763"
	pgTestSeason     = 20262027
	pgTestTeamID     = 1
	pgTestManagerID  = 1
	pgTestNickname   = "Nick"
	pgTestGUID       = "GUIDSECRET763"
	pgTestEmail      = "manager763@example.invalid"
	personalGUIDTag  = "guid"
	personalEmailTag = "email"
)

// wantTeamColumns is the full get_yahoo_teams_by_league header; pinning it
// keeps any new column, personal or not, a deliberate change.
var wantTeamColumns = []string{
	"league_id", "id", "team_key", "name", "url", "logo_url", "draft_position",
	"waiver_priority", "number_of_moves", "number_of_trades",
	"is_owned_by_current_login", "managers", "created_at", "updated_at",
}

func TestYahooTeamsToolListsManagers(t *testing.T) {
	q := newFakeYahooQueries()
	q.teams = []sqlcdb.YahooTeam{
		{LeagueID: allowedLeagueID, ID: 1, Name: "Co-managed"},
		{LeagueID: allowedLeagueID, ID: 2, Name: "Solo"},
		{LeagueID: allowedLeagueID, ID: 3, Name: "Orphan"},
	}
	q.managers = []sqlcdb.GetYahooTeamManagersByLeagueRow{
		{LeagueID: allowedLeagueID, TeamID: 1, ID: 1, Nickname: "Alice", IsCommissioner: true, IsCurrentLogin: true},
		{LeagueID: allowedLeagueID, TeamID: 1, ID: 2, Nickname: "Bob"},
		{LeagueID: allowedLeagueID, TeamID: 2, ID: 3, Nickname: "Carol", IsCurrentLogin: true},
	}

	rows := callTeamsTool(t, q)
	require.Equal(t, wantTeamColumns, rows[0])
	col := columnIndex(t, rows[0], "managers")
	var got []string
	for _, row := range rows[1:] {
		got = append(got, row[col])
	}
	assert.Equal(t, []string{
		"Alice (commissioner, current login); Bob",
		"Carol (current login)",
		"",
	}, got)
}

func TestYahooTeamsToolReportsManagerQueryError(t *testing.T) {
	q := newFakeYahooQueries()
	q.managerErr = errors.New("connection refused")
	tool := yahooTestServer(q, nil).GetTool(teamsToolName)
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.True(t, result.IsError)
	assert.Equal(t, "connection refused", resultText(t, result))
}

// TestYahooTeamsToolSkipsManagersForEmptyLeague: without teams the manager
// query does not run, so its stubbed error never surfaces.
func TestYahooTeamsToolSkipsManagersForEmptyLeague(t *testing.T) {
	q := newFakeYahooQueries()
	q.teams = []sqlcdb.YahooTeam{}
	q.managerErr = errors.New("manager query ran")
	tool := yahooTestServer(q, nil).GetTool(teamsToolName)
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.False(t, result.IsError)
	assert.Equal(t, "no results", resultText(t, result))
}

// TestYahooTeamRowsCarryNoPersonalData checks the types: neither the manager
// query row nor the tool row has a guid or email field, so no code path can
// put them in the CSV.
func TestYahooTeamRowsCarryNoPersonalData(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[sqlcdb.GetYahooTeamManagersByLeagueRow](),
		reflect.TypeFor[yahooTeamRow](),
	} {
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			assert.NotContains(t, []string{personalGUIDTag, personalEmailTag}, tag, "%s.%s", typ.Name(), f.Name)
		}
	}
}

// TestYahooTeamsToolOmitsPersonalDataFromDatabase stores a manager with a guid
// and email and checks the tool output against the real query.
func TestYahooTeamsToolOmitsPersonalDataFromDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	deleteTestLeague(t, pool)
	t.Cleanup(func() { deleteTestLeague(t, pool) })
	seedManagedTeam(t, pool)

	tool := yahooTestServer(sqlcdb.New(pool), []string{pgTestLeagueKey}).GetTool(teamsToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(pgTestLeagueID)})
	text := resultText(t, result)
	require.False(t, result.IsError, text)

	rows := parseCSV(t, text)
	require.Equal(t, wantTeamColumns, rows[0])
	require.Len(t, rows, 2)
	assert.Equal(t, pgTestNickname+" (commissioner)", rows[1][columnIndex(t, rows[0], "managers")])
	assert.NotContains(t, text, pgTestGUID)
	assert.NotContains(t, text, pgTestEmail)
}

func callTeamsTool(t *testing.T, q yahooQueries) [][]string {
	t.Helper()
	tool := yahooTestServer(q, nil).GetTool(teamsToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	require.False(t, result.IsError, resultText(t, result))
	return parseCSV(t, resultText(t, result))
}

func openMCPTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL MCP server tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBMarker, "test database URL must contain %q", testDBMarker)
	require.NoError(t, database.MigrateUp(dbURL))
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedManagedTeam(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'MCP test', $3)`,
		pgTestLeagueID, pgTestLeagueKey, pgTestSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, 'Team')`,
		pgTestLeagueID, pgTestTeamID, pgTestLeagueKey+".t.1")
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, guid, email, is_commissioner)
		 VALUES ($1, $2, $3, $4, $5, $6, true)`,
		pgTestLeagueID, pgTestTeamID, pgTestManagerID, pgTestNickname, pgTestGUID, pgTestEmail)
	require.NoError(t, err)
}

// deleteTestLeague removes the fixture league; teams and managers cascade.
func deleteTestLeague(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `DELETE FROM yahoo_leagues WHERE id = $1`, pgTestLeagueID)
	require.NoError(t, err)
}

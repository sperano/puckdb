package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rosterDate          = "2026-11-02"
	rosterSeason  int32 = 20262027
	rosterTeamOne int32 = 1
	rosterTeamTwo int32 = 2
	// rosterTeamEmpty has no roster rows on rosterDate.
	rosterTeamEmpty int32 = 3

	// PostgreSQL fixture of TestYahooLeagueRostersToolReadsDatabase, in the
	// league of the pgTest* constants (tools_yahoo_teams_test.go).
	pgRosterMatchedYahooID  int32 = 990754001
	pgRosterPoolYahooID     int32 = 990754002
	pgRosterNamelessYahooID int32 = 990754003
	pgRosterOtherDayYahooID int32 = 990754004
	pgRosterNHLPlayerID     int64 = 990754001
	pgRosterOtherTeamID     int32 = 2
)

var rosterTeams = []sqlcdb.YahooTeam{
	{LeagueID: allowedLeagueID, ID: rosterTeamOne, Name: "Team One"},
	{LeagueID: allowedLeagueID, ID: rosterTeamTwo, Name: "Team Two"},
	{LeagueID: allowedLeagueID, ID: rosterTeamEmpty, Name: "Team, Three"},
}

// fakeLeagueRosterQueries adds the league roster query to fakeYahooQueries.
type fakeLeagueRosterQueries struct {
	*fakeYahooQueries
	rosters     []sqlcdb.GetYahooLeagueRostersWithPlayersRow
	rostersErr  error
	rosterCalls []sqlcdb.GetYahooLeagueRostersWithPlayersParams
	teamsErr    error
}

func (f *fakeLeagueRosterQueries) GetYahooLeagueRostersWithPlayers(_ context.Context, arg sqlcdb.GetYahooLeagueRostersWithPlayersParams) ([]sqlcdb.GetYahooLeagueRostersWithPlayersRow, error) {
	f.rosterCalls = append(f.rosterCalls, arg)
	return f.rosters, f.rostersErr
}

func (f *fakeLeagueRosterQueries) GetYahooTeamsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooTeam, error) {
	if f.teamsErr != nil {
		return nil, f.teamsErr
	}
	return f.fakeYahooQueries.GetYahooTeamsByLeague(ctx, leagueID)
}

func newFakeLeagueRosterQueries() *fakeLeagueRosterQueries {
	base := newFakeYahooQueries()
	base.teams = rosterTeams
	for i := range base.leagues {
		base.leagues[i].Season = rosterSeason
	}
	return &fakeLeagueRosterQueries{fakeYahooQueries: base, rosters: []sqlcdb.GetYahooLeagueRostersWithPlayersRow{
		rosterRow(rosterTeamOne, "C", 101, "Connor Center", 8471675, "EDM", "C,LW", "", ""),
		rosterRow(rosterTeamOne, "IR", 102, "Wade Winger", 0, "TOR", "RW", "IR-LT", "Lower body"),
		rosterRow(rosterTeamTwo, "G", 103, "Gus Goalie", 8476883, "MTL", "G", "DTD", ""),
	}}
}

// rosterRow builds one stored roster row; nhlID 0 means unmatched.
func rosterRow(teamID int32, slot string, yahooID int32, name string, nhlID int64, team, positions, status, note string) sqlcdb.GetYahooLeagueRostersWithPlayersRow {
	text := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }
	return sqlcdb.GetYahooLeagueRostersWithPlayersRow{
		TeamID: teamID, SelectedPosition: slot, YahooPlayerID: yahooID, PlayerKey: fmt.Sprintf("465.p.%d", yahooID),
		Name: name, NhlPlayerID: pgtype.Int8{Int64: nhlID, Valid: nhlID != 0}, EditorialTeamAbbr: text(team),
		DisplayPosition: text(positions), PlayerStatus: text(status), InjuryNote: text(note),
	}
}

// callLeagueRostersTool calls get_yahoo_league_rosters for the allowed
// league on rosterDate with extra arguments, which may override both; a
// nil value leaves the argument out.
func callLeagueRostersTool(t *testing.T, q yahooQueries, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTestServer(q, nil).GetTool(leagueRostersToolName)
	require.NotNil(t, tool)
	all := map[string]any{leagueIDArg: float64(allowedLeagueID), "date": rosterDate}
	for name, value := range args {
		if value == nil {
			delete(all, name)
			continue
		}
		all[name] = value
	}
	return callTool(t, tool.Handler, all)
}

// leagueRostersOutput splits a successful result into its header and sections.
func leagueRostersOutput(t *testing.T, result *mcp.CallToolResult) settingsOutput {
	t.Helper()
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	return parseSettingsOutput(t, text)
}

func TestYahooLeagueRostersToolReportsEveryTeam(t *testing.T) {
	q := newFakeLeagueRosterQueries()
	out := leagueRostersOutput(t, callLeagueRostersTool(t, q, nil))

	wantDate, err := time.Parse(time.DateOnly, rosterDate)
	require.NoError(t, err)
	assert.Equal(t, []sqlcdb.GetYahooLeagueRostersWithPlayersParams{
		{LeagueID: allowedLeagueID, Date: pgtype.Date{Time: wantDate, Valid: true}},
	}, q.rosterCalls)
	assert.Equal(t, fmt.Sprintf("# league_id=%d league_key=%s season=%d date=%s teams=3 roster_rows=3 returned=3",
		allowedLeagueID, allowedLeagueKey, rosterSeason, rosterDate), out.header)
	assert.Equal(t, [][]string{
		{"team_id", "name", "roster_count"},
		{"1", "Team One", "2"},
		{"2", "Team Two", "1"},
		{"3", "Team, Three", "0"},
	}, out.sections[leagueRosterTeamsSection], "every team is listed, even one with no rows on the date")
	assert.Equal(t, [][]string{
		{"team_id", "selected_position", "yahoo_player_id", "player_key", "name", "nhl_player_id", "team", "positions",
			"status", "injury_note"},
		{"1", "C", "101", "465.p.101", "Connor Center", "8471675", "EDM", "C,LW", "", ""},
		{"1", "IR", "102", "465.p.102", "Wade Winger", "", "TOR", "RW", "IR-LT", "Lower body"},
		{"2", "G", "103", "465.p.103", "Gus Goalie", "8476883", "MTL", "G", "DTD", ""},
	}, out.sections[leagueRostersSection])
}

func TestYahooLeagueRostersToolLimit(t *testing.T) {
	out := leagueRostersOutput(t, callLeagueRostersTool(t, newFakeLeagueRosterQueries(), map[string]any{"limit": float64(2)}))
	assert.Contains(t, out.header, " roster_rows=3 returned=2")
	assert.Equal(t, []string{"1", "Team One", "2"}, out.sections[leagueRosterTeamsSection][1])
	assert.Equal(t, []string{"2", "Team Two", "1"}, out.sections[leagueRosterTeamsSection][2],
		"team roster counts cover the rows the limit cut")
	assert.Len(t, out.sections[leagueRostersSection], 3, "header row plus two roster rows")
}

func TestYahooLeagueRostersToolDefaultLimit(t *testing.T) {
	q := newFakeLeagueRosterQueries()
	q.rosters = nil
	for i := range defaultLeagueRostersLimit + 1 {
		q.rosters = append(q.rosters, rosterRow(rosterTeamOne, "BN", int32(i+1), "Player", 0, "EDM", "C", "", ""))
	}
	out := leagueRostersOutput(t, callLeagueRostersTool(t, q, nil))
	assert.Contains(t, out.header, fmt.Sprintf(" roster_rows=%d returned=%d", defaultLeagueRostersLimit+1, defaultLeagueRostersLimit))
	assert.Len(t, out.sections[leagueRostersSection], defaultLeagueRostersLimit+1, "header row plus the default number of rows")
}

func TestYahooLeagueRostersToolNoRostersOnDate(t *testing.T) {
	q := newFakeLeagueRosterQueries()
	q.rosters = []sqlcdb.GetYahooLeagueRostersWithPlayersRow{}
	out := leagueRostersOutput(t, callLeagueRostersTool(t, q, nil))
	assert.Contains(t, out.header, " teams=3 roster_rows=0 returned=0")
	assert.Equal(t, []string{"3", "Team, Three", "0"}, out.sections[leagueRosterTeamsSection][3])
	assert.Equal(t, [][]string{{noResultsText}}, out.sections[leagueRostersSection])
}

func TestYahooLeagueRostersToolRejectsInvalidArguments(t *testing.T) {
	limitRange := fmt.Sprintf("want an integer from 1 to %d", maxLeagueRostersLimit)
	tests := map[string]struct {
		args map[string]any
		want string
	}{
		"missing date":     {map[string]any{"date": nil}, "date is required"},
		"malformed date":   {map[string]any{"date": "11/02/2026"}, `invalid date "11/02/2026": want a date as YYYY-MM-DD`},
		"date not string":  {map[string]any{"date": float64(20261102)}, "invalid date 20261102: want a date as YYYY-MM-DD"},
		"zero limit":       {map[string]any{"limit": float64(0)}, "invalid limit 0: " + limitRange},
		"limit over max":   {map[string]any{"limit": float64(maxLeagueRostersLimit + 1)}, fmt.Sprintf("invalid limit %d: %s", maxLeagueRostersLimit+1, limitRange)},
		"fractional limit": {map[string]any{"limit": 2.5}, "invalid limit 2.5: " + limitRange},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := newFakeLeagueRosterQueries()
			result := callLeagueRostersTool(t, q, tt.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
			assert.Empty(t, q.rosterCalls, "an invalid argument must be refused before any query")
		})
	}
}

// TestYahooLeagueRostersToolMissingLeague pins that an unrestricted server,
// whose guard does not look leagues up, answers a missing league with the
// guard's unknown-league error instead of empty rosters.
func TestYahooLeagueRostersToolMissingLeague(t *testing.T) {
	q := newFakeLeagueRosterQueries()
	result := callLeagueRostersTool(t, q, map[string]any{leagueIDArg: float64(missingLeagueID)})
	assert.Equal(t, unknownLeagueResult(missingLeagueID), result)
	assert.Empty(t, q.rosterCalls)
}

func TestYahooLeagueRostersToolQueryErrors(t *testing.T) {
	tests := map[string]struct {
		setup func(*fakeLeagueRosterQueries)
		want  string
	}{
		"league": {func(q *fakeLeagueRosterQueries) { q.lookupErr = errors.New("connection refused") },
			fmt.Sprintf("load league %d: connection refused", allowedLeagueID)},
		"teams": {func(q *fakeLeagueRosterQueries) { q.teamsErr = errors.New("connection reset") },
			fmt.Sprintf("load teams of league %d: connection reset", allowedLeagueID)},
		"rosters": {func(q *fakeLeagueRosterQueries) { q.rostersErr = errors.New("timeout") },
			fmt.Sprintf("load rosters of league %d on %s: timeout", allowedLeagueID, rosterDate)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := newFakeLeagueRosterQueries()
			tt.setup(q)
			result := callLeagueRostersTool(t, q, nil)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
		})
	}
}

// TestYahooLeagueRostersToolReadsDatabase runs the tool against the real
// query: a player matched to an NHL player (whose name wins over the pool's),
// an unmatched player named by the league's pool, an unmatched player with
// no name anywhere, and a row on another date that must not show.
func TestYahooLeagueRostersToolReadsDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanup := func() {
		deleteTestLeague(t, pool)
		_, err := pool.Exec(context.Background(), `DELETE FROM yahoo_league_players WHERE league_key = $1`, pgTestLeagueKey)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), `DELETE FROM players WHERE id = $1`, pgRosterNHLPlayerID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	seedLeagueRosters(t, pool)

	tool := yahooTestServer(sqlcdb.New(pool), []string{pgTestLeagueKey}).GetTool(leagueRostersToolName)
	require.NotNil(t, tool)
	out := leagueRostersOutput(t, callTool(t, tool.Handler,
		map[string]any{leagueIDArg: float64(pgTestLeagueID), "date": rosterDate}))

	assert.Equal(t, fmt.Sprintf("# league_id=%d league_key=%s season=%d date=%s teams=2 roster_rows=3 returned=3",
		pgTestLeagueID, pgTestLeagueKey, pgTestSeason, rosterDate), out.header)
	assert.Equal(t, [][]string{
		{"team_id", "name", "roster_count"},
		{"1", "Team", "2"},
		{"2", "Other", "1"},
	}, out.sections[leagueRosterTeamsSection])
	rows := out.sections[leagueRostersSection]
	require.Len(t, rows, 4)
	cols := []string{"team_id", "selected_position", "yahoo_player_id", "name", "nhl_player_id", "status"}
	got := make([][]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		picked := make([]string, 0, len(cols))
		for _, col := range cols {
			picked = append(picked, row[columnIndex(t, rows[0], col)])
		}
		got = append(got, picked)
	}
	assert.Equal(t, [][]string{
		{"1", "BN", fmt.Sprint(pgRosterPoolYahooID), "Pool Name", "", "IR"},
		{"1", "C", fmt.Sprint(pgRosterMatchedYahooID), "Matched Player", fmt.Sprint(pgRosterNHLPlayerID), ""},
		{"2", "LW", fmt.Sprint(pgRosterNamelessYahooID), "", "", ""},
	}, got)
}

// seedLeagueRosters stores the fixture league with two teams, one NHL
// player, two pool players and roster rows on rosterDate plus one on the
// day before.
func seedLeagueRosters(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'MCP test', $3)`,
		pgTestLeagueID, pgTestLeagueKey, pgTestSeason)
	for id, name := range map[int32]string{pgTestTeamID: "Team", pgRosterOtherTeamID: "Other"} {
		exec(`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, $4)`,
			pgTestLeagueID, id, fmt.Sprintf("%s.t.%d", pgTestLeagueKey, id), name)
	}
	exec(`INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'Matched', 'Player')`,
		pgRosterNHLPlayerID, pgRosterMatchedYahooID)
	for id, name := range map[int32]string{pgRosterMatchedYahooID: "Yahoo Spelling", pgRosterPoolYahooID: "Pool Name"} {
		exec(`INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id, player_key, full_name,
			 eligible_positions, fetched_at) VALUES ($1, $2, $3, 465, $4, $5, $6, '{C}', now())`,
			pgTestLeagueKey, pgTestSeason, pgTestLeagueID, id, fmt.Sprintf("465.p.%d", id), name)
	}
	for _, r := range []struct {
		team, player int32
		date, slot   string
		status       *string
	}{
		{pgTestTeamID, pgRosterMatchedYahooID, rosterDate, "C", nil},
		{pgTestTeamID, pgRosterPoolYahooID, rosterDate, "BN", new("IR")},
		{pgRosterOtherTeamID, pgRosterNamelessYahooID, rosterDate, "LW", nil},
		{pgTestTeamID, pgRosterOtherDayYahooID, "2026-11-01", "D", nil},
	} {
		exec(`INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, player_key, selected_position, player_status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			pgTestLeagueID, r.team, r.date, r.player, fmt.Sprintf("465.p.%d", r.player), r.slot, r.status)
	}
}

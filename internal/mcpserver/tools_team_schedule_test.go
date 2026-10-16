package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wantScheduleTeamColumns pins the teams section's CSV header.
var wantScheduleTeamColumns = []string{
	"team_id", "team_abbrev", "games", "home", "away", "back_to_backs",
	"light_night_games", "heavy_night_games", "game_dates",
}

type teamScheduleQueryFake struct {
	calls       int
	nightParams sqlcdb.ListScheduleNightsParams
	teamParams  sqlcdb.GetTeamScheduleCountsParams
	nights      []sqlcdb.ListScheduleNightsRow
	teams       []sqlcdb.GetTeamScheduleCountsRow
	nightErr    error
	teamErr     error
}

func (f *teamScheduleQueryFake) ListScheduleNights(_ context.Context, params sqlcdb.ListScheduleNightsParams) ([]sqlcdb.ListScheduleNightsRow, error) {
	f.calls++
	f.nightParams = params
	return f.nights, f.nightErr
}

func (f *teamScheduleQueryFake) GetTeamScheduleCounts(_ context.Context, params sqlcdb.GetTeamScheduleCountsParams) ([]sqlcdb.GetTeamScheduleCountsRow, error) {
	f.calls++
	f.teamParams = params
	return f.teams, f.teamErr
}

func teamScheduleTestServer(q teamScheduleQueries) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerTeamScheduleTools(srv, q)
	return srv
}

func callTeamScheduleTool(t *testing.T, q teamScheduleQueries, args map[string]any) string {
	t.Helper()
	tool := teamScheduleTestServer(q).GetTool(teamScheduleToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, args)
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	return text
}

func scheduleDate(t *testing.T, s string) pgtype.Date {
	t.Helper()
	day, err := time.Parse(time.DateOnly, s)
	require.NoError(t, err)
	return pgtype.Date{Time: day, Valid: true}
}

func scheduleWeek() map[string]any {
	return map[string]any{scheduleStartArg: "2025-10-13", scheduleEndArg: "2025-10-19"}
}

func TestTeamScheduleToolDefaults(t *testing.T) {
	q := &teamScheduleQueryFake{}
	callTeamScheduleTool(t, q, scheduleWeek())

	require.Equal(t, 2, q.calls)
	assert.Equal(t, sqlcdb.GetTeamScheduleCountsParams{
		LightNightMaxGames: lightNightMaxGames,
		HeavyNightMinGames: heavyNightMinGames,
		StartDate:          scheduleDate(t, "2025-10-13"),
		EndDate:            scheduleDate(t, "2025-10-19"),
	}, q.teamParams, "every team")
	assert.Equal(t, sqlcdb.ListScheduleNightsParams{
		StartDate: q.teamParams.StartDate, EndDate: q.teamParams.EndDate,
	}, q.nightParams)
}

func TestTeamScheduleToolPassesTeamFilter(t *testing.T) {
	q := &teamScheduleQueryFake{}
	callTeamScheduleTool(t, q, map[string]any{
		scheduleStartArg: "2026-01-10",
		scheduleEndArg:   "2026-01-10",
		"team_id":        float64(8),
	})

	assert.Equal(t, q.teamParams.StartDate, q.teamParams.EndDate, "a one-day range is valid")
	assert.Equal(t, pgtype.Int8{Int64: 8, Valid: true}, q.teamParams.TeamID)
}

func TestTeamScheduleToolTreatsZeroTeamAsEveryTeam(t *testing.T) {
	q := &teamScheduleQueryFake{}
	callTeamScheduleTool(t, q, withArg(scheduleWeek(), "team_id", float64(0)))
	assert.False(t, q.teamParams.TeamID.Valid)
}

func TestTeamScheduleToolAcceptsMaximumSpan(t *testing.T) {
	q := &teamScheduleQueryFake{}
	text := callTeamScheduleTool(t, q, map[string]any{scheduleStartArg: "2026-01-01", scheduleEndArg: "2026-01-31"})
	assert.Contains(t, text, "days=31 ")
}

func TestTeamScheduleToolRejectsBadArgumentsBeforeSQL(t *testing.T) {
	week := scheduleWeek()
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no start_date", map[string]any{scheduleEndArg: "2025-10-19"}, "start_date is required"},
		{"no end_date", map[string]any{scheduleStartArg: "2025-10-13"}, "end_date is required"},
		{"malformed date", withArg(week, scheduleStartArg, "10/13/2025"), `invalid start_date "10/13/2025": want a date as YYYY-MM-DD`},
		{"impossible date", withArg(week, scheduleEndArg, "2025-02-30"), `invalid end_date "2025-02-30"`},
		{"reversed range", withArg(week, scheduleEndArg, "2025-10-12"), "start_date is after end_date"},
		{"span too long", map[string]any{scheduleStartArg: "2026-01-01", scheduleEndArg: "2026-02-01"}, "the range spans 32 days; at most 31 are allowed"},
		{"negative team", withArg(week, "team_id", float64(-1)), "invalid team_id -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &teamScheduleQueryFake{}
			result := callTool(t, teamScheduleTestServer(q).GetTool(teamScheduleToolName).Handler, tt.args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tt.want)
			assert.Zero(t, q.calls)
		})
	}
}

func TestTeamScheduleToolReportsQueryErrors(t *testing.T) {
	for name, q := range map[string]*teamScheduleQueryFake{
		"nights": {nightErr: errors.New("nights failed")},
		"teams":  {teamErr: errors.New("teams failed")},
	} {
		t.Run(name, func(t *testing.T) {
			result := callTool(t, teamScheduleTestServer(q).GetTool(teamScheduleToolName).Handler, scheduleWeek())
			assert.True(t, result.IsError)
			assert.Equal(t, name+" failed", resultText(t, result))
		})
	}
}

func TestTeamScheduleToolRendersSections(t *testing.T) {
	q := &teamScheduleQueryFake{
		nights: []sqlcdb.ListScheduleNightsRow{
			{GameDate: scheduleDate(t, "2025-10-13"), Games: lightNightMaxGames},
			{GameDate: scheduleDate(t, "2025-10-14"), Games: lightNightMaxGames + 1},
			{GameDate: scheduleDate(t, "2025-10-15"), Games: heavyNightMinGames},
		},
		teams: []sqlcdb.GetTeamScheduleCountsRow{
			{TeamID: 8, TeamAbbrev: "MTL", Games: 2, Home: 1, Away: 1, BackToBacks: 1, LightNightGames: 1, GameDates: "2025-10-13;2025-10-14"},
		},
	}
	text := callTeamScheduleTool(t, q, withArg(scheduleWeek(), "team_id", float64(8)))
	assert.Equal(t, "# start_date=2025-10-13 end_date=2025-10-19 days=7 game_type=regular_season team_id=8 "+
		"games=25 nights_with_games=3 light_nights=1 heavy_nights=1 light_night_max_games=7 heavy_night_min_games=10\n"+
		"# teams\n"+
		strings.Join(wantScheduleTeamColumns, ",")+"\n"+
		"8,MTL,2,1,1,1,1,0,2025-10-13;2025-10-14\n"+
		"# nights\n"+
		"date,games,night\n"+
		"2025-10-13,7,light\n"+
		"2025-10-14,8,normal\n"+
		"2025-10-15,10,heavy\n", text)
}

func TestTeamScheduleToolRendersEmptyRange(t *testing.T) {
	text := callTeamScheduleTool(t, &teamScheduleQueryFake{}, scheduleWeek())
	assert.Equal(t, "# start_date=2025-10-13 end_date=2025-10-19 days=7 game_type=regular_season "+
		"games=0 nights_with_games=0 light_nights=0 heavy_nights=0 light_night_max_games=7 heavy_night_min_games=10\n"+
		"# teams\nno results\n# nights\nno results\n", text)
}

// PostgreSQL fixture: a far-future season (no real games share its dates)
// with named clubs A-D and F, an international club E, and filler clubs
// X0..X17 that make 2097-10-07 a heavy night and 2097-10-08 a normal one.
const (
	scheduleIDBase     int64 = 990751000
	scheduleClubA            = scheduleIDBase + 1
	scheduleClubB            = scheduleIDBase + 2
	scheduleClubC            = scheduleIDBase + 3
	scheduleClubD            = scheduleIDBase + 4
	scheduleClubE            = scheduleIDBase + 5
	scheduleClubF            = scheduleIDBase + 6
	scheduleFillerBase       = scheduleIDBase + 50
	// scheduleNamedClubs counts the listed named clubs A-D and F.
	scheduleNamedClubs = 5
	// With A-D on 10-07 and B-C on 10-08, the fillers make 10-07 just
	// heavy and 10-08 one game over light.
	scheduleHeavyFillerGames        = heavyNightMinGames - 1
	scheduleNormalFillerGames       = lightNightMaxGames
	scheduleSeason            int32 = 20972098
	scheduleGameBase                = scheduleIDBase * 10
)

// scheduleGame is one fixture game.
type scheduleGame struct {
	date       string
	home, away int64
	gameType   sqlcdb.GameType
	state      sqlcdb.GameScheduleState
}

func scheduleFixtureGames() []scheduleGame {
	regular := func(date string, home, away int64) scheduleGame {
		return scheduleGame{date, home, away, sqlcdb.GameTypeRegularSeason, sqlcdb.GameScheduleStateOK}
	}
	games := []scheduleGame{
		// Before the range: makes B's 10-05 game a back-to-back that
		// must not count.
		regular("2097-10-04", scheduleClubB, scheduleClubD),
		regular("2097-10-05", scheduleClubB, scheduleClubA),
		regular("2097-10-06", scheduleClubA, scheduleClubC),
		regular("2097-10-07", scheduleClubA, scheduleClubD),
		regular("2097-10-08", scheduleClubB, scheduleClubC),
		{"2097-10-09", scheduleClubA, scheduleClubB, sqlcdb.GameTypeRegularSeason, sqlcdb.GameScheduleStatePPD},
		{"2097-10-09", scheduleClubC, scheduleClubD, sqlcdb.GameTypeRegularSeason, sqlcdb.GameScheduleStateCNCL},
		{"2097-10-10", scheduleClubC, scheduleClubD, sqlcdb.GameTypePreseason, sqlcdb.GameScheduleStateOK},
		{"2097-10-11", scheduleClubA, scheduleClubB, sqlcdb.GameTypePlayoffs, sqlcdb.GameScheduleStateOK},
		// After the range.
		regular("2097-10-12", scheduleClubA, scheduleClubC),
	}
	for i := range int64(scheduleHeavyFillerGames) {
		games = append(games, regular("2097-10-07", scheduleFillerBase+2*i, scheduleFillerBase+2*i+1))
	}
	for i := range int64(scheduleNormalFillerGames) {
		games = append(games, regular("2097-10-08", scheduleFillerBase+2*i, scheduleFillerBase+2*i+1))
	}
	return games
}

func TestTeamScheduleQueriesAggregateAgainstPostgres(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanupScheduleFixture(t, pool)
	t.Cleanup(func() { cleanupScheduleFixture(t, pool) })
	seedScheduleFixture(t, pool)
	q := sqlcdb.New(pool)
	week := map[string]any{scheduleStartArg: "2097-10-05", scheduleEndArg: "2097-10-11"}

	header, teams, nights := scheduleSections(t, callTeamScheduleTool(t, q, week))
	assert.Equal(t, "# start_date=2097-10-05 end_date=2097-10-11 days=7 game_type=regular_season "+
		"games=20 nights_with_games=4 light_nights=2 heavy_nights=1 light_night_max_games=7 heavy_night_min_games=10", header)
	assert.Equal(t, [][]string{
		{"A", "3", "2", "1", "2", "2", "1", "2097-10-05;2097-10-06;2097-10-07"},
		{"B", "2", "2", "0", "0", "1", "0", "2097-10-05;2097-10-08"},
		{"C", "2", "0", "2", "0", "1", "0", "2097-10-06;2097-10-08"},
		{"D", "1", "0", "1", "0", "0", "1", "2097-10-07"},
		{"F", "0", "0", "0", "0", "0", "0", ""},
	}, namedScheduleRows(t, teams), "E is international and not listed; the 10-09 to 10-11 games do not count")
	assert.Len(t, teams, 1+scheduleNamedClubs+2*scheduleHeavyFillerGames, "header, named clubs, filler clubs")
	assert.Equal(t, [][]string{
		{"date", "games", "night"},
		{"2097-10-05", "1", "light"},
		{"2097-10-06", "1", "light"},
		{"2097-10-07", "10", "heavy"},
		{"2097-10-08", "8", "normal"},
	}, nights)

	_, teams, nights = scheduleSections(t, callTeamScheduleTool(t, q, withArg(week, "team_id", float64(scheduleClubF))))
	assert.Equal(t, [][]string{{"F", "0", "0", "0", "0", "0", "0", ""}}, namedScheduleRows(t, teams), "a bye week is a 0 row")
	assert.Len(t, nights, 5, "nights stay league-wide with a team filter")
}

// scheduleSections splits a get_team_schedule result into its header line
// and its two parsed CSV sections.
func scheduleSections(t *testing.T, text string) (header string, teams, nights [][]string) {
	t.Helper()
	header, rest, found := strings.Cut(text, "\n"+commentPrefix+scheduleTeamsSection+"\n")
	require.True(t, found, text)
	teamsText, nightsText, found := strings.Cut(rest, commentPrefix+scheduleNightsSection+"\n")
	require.True(t, found, text)
	return header, parseCSV(t, teamsText), parseCSV(t, nightsText)
}

// namedScheduleRows drops the CSV header, the filler clubs and team_id.
func namedScheduleRows(t *testing.T, rows [][]string) [][]string {
	t.Helper()
	require.Equal(t, wantScheduleTeamColumns, rows[0])
	var named [][]string
	for _, row := range rows[1:] {
		if !strings.HasPrefix(row[1], "X") {
			named = append(named, row[1:])
		}
	}
	return named
}

func seedScheduleFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '2097-10-01', '2098-04-15')`, scheduleSeason)
	exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
	      VALUES ($1, $2, 'Club A', 'A', 'D', 'D'), ($1, $3, 'Club B', 'B', 'D', 'D'),
	             ($1, $4, 'Club C', 'C', 'D', 'D'), ($1, $5, 'Club D', 'D', 'D', 'D'),
	             ($1, $6, 'Club F', 'F', 'D', 'D')`,
		scheduleSeason, scheduleClubA, scheduleClubB, scheduleClubC, scheduleClubD, scheduleClubF)
	exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, team_kind)
	      VALUES ($1, $2, 'Club E', 'E', 'international')`, scheduleSeason, scheduleClubE)
	exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
	      SELECT $1, $2 + n, 'Filler', 'X' || n, 'D', 'D' FROM generate_series(0, $3 - 1) AS n`,
		scheduleSeason, scheduleFillerBase, 2*scheduleHeavyFillerGames)
	for i, g := range scheduleFixtureGames() {
		exec(`INSERT INTO games (id, season, game_type, game_date, game_state, game_schedule_state, home_team_id, away_team_id)
		      VALUES ($1, $2, $3, $4, 'FUT', $5, $6, $7)`,
			scheduleGameBase+int64(i), scheduleSeason, g.gameType, g.date, g.state, g.home, g.away)
	}
}

func cleanupScheduleFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DELETE FROM games WHERE id BETWEEN $1 AND $1 + 99`, scheduleGameBase)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM season_teams WHERE season = $1`, scheduleSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM seasons WHERE id = $1`, scheduleSeason)
	require.NoError(t, err)
}

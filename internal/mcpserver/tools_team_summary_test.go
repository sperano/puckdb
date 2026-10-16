package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wantSkaterColumns = "player_id,first_name,last_name,position,games_played,total_goals,total_assists," +
		"total_points,total_plus_minus,total_shots,total_pim"
	wantGoalieColumns = "player_id,first_name,last_name,games_played,starts,wins,losses,otl,total_shots_against," +
		"total_saves,total_goals_against,total_toi_seconds,season_save_pctg,season_gaa"
)

type teamSummaryQueryFake struct {
	calls       []string
	goalsParams sqlcdb.GetTeamGoalsPerGameParams
	skaterArgs  sqlcdb.GetTeamSkaterSeasonTotalsParams
	goalieArgs  sqlcdb.GetTeamGoalieSeasonTotalsParams
	goals       sqlcdb.GetTeamGoalsPerGameRow
	skaters     []sqlcdb.GetTeamSkaterSeasonTotalsRow
	goalies     []sqlcdb.GetTeamGoalieSeasonTotalsRow
	// failing names the query that returns errFakeQuery.
	failing string
}

var errFakeQuery = errors.New("boom")

func (f *teamSummaryQueryFake) result(name string) error {
	f.calls = append(f.calls, name)
	if f.failing == name {
		return errFakeQuery
	}
	return nil
}

func (f *teamSummaryQueryFake) GetTeamGoalsPerGame(_ context.Context, p sqlcdb.GetTeamGoalsPerGameParams) (sqlcdb.GetTeamGoalsPerGameRow, error) {
	f.goalsParams = p
	return f.goals, f.result("goals")
}

func (f *teamSummaryQueryFake) GetTeamSkaterSeasonTotals(_ context.Context, p sqlcdb.GetTeamSkaterSeasonTotalsParams) ([]sqlcdb.GetTeamSkaterSeasonTotalsRow, error) {
	f.skaterArgs = p
	return f.skaters, f.result("skaters")
}

func (f *teamSummaryQueryFake) GetTeamGoalieSeasonTotals(_ context.Context, p sqlcdb.GetTeamGoalieSeasonTotalsParams) ([]sqlcdb.GetTeamGoalieSeasonTotalsRow, error) {
	f.goalieArgs = p
	return f.goalies, f.result("goalies")
}

func teamSummaryTestServer(q teamSummaryQueries) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerTeamSummaryTools(srv, q)
	return srv
}

// callTeamSummary calls the tool and returns its text and error flag.
func callTeamSummary(t *testing.T, q teamSummaryQueries, args map[string]any) (string, bool) {
	t.Helper()
	tool := teamSummaryTestServer(q).GetTool(teamSummaryToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, args)
	return resultText(t, result), result.IsError
}

func teamSummaryArgsFor(teamID int64, season int32) map[string]any {
	return map[string]any{teamIDParam.name: float64(teamID), seasonParam.name: float64(season)}
}

func TestTeamSummaryDefaultsToRegularSeasonWithoutDateCutoff(t *testing.T) {
	const teamID = 8
	q := &teamSummaryQueryFake{}
	text, isError := callTeamSummary(t, q, teamSummaryArgsFor(teamID, testSeasonID))
	require.False(t, isError, text)

	assert.Equal(t, []string{"goals", "skaters", "goalies"}, q.calls)
	assert.Equal(t, sqlcdb.GetTeamGoalsPerGameParams{
		Season: testSeasonID, GameDate: noDateCutoff, HomeTeamID: teamID, GameType: sqlcdb.GameTypeRegularSeason,
	}, q.goalsParams)
	assert.Equal(t, sqlcdb.GetTeamSkaterSeasonTotalsParams{
		TeamID: teamID, Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason,
	}, q.skaterArgs)
	assert.Equal(t, sqlcdb.GetTeamGoalieSeasonTotalsParams{
		TeamID: teamID, Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason,
	}, q.goalieArgs)
}

func TestTeamSummaryPassesPlayoffsToEveryQuery(t *testing.T) {
	q := &teamSummaryQueryFake{}
	text, isError := callTeamSummary(t, q, withArg(teamSummaryArgsFor(8, testSeasonID), teamSummaryGameTypeArg, " Playoffs "))
	require.False(t, isError, text)

	assert.Equal(t, sqlcdb.GameTypePlayoffs, q.goalsParams.GameType)
	assert.Equal(t, sqlcdb.GameTypePlayoffs, q.skaterArgs.GameType)
	assert.Equal(t, sqlcdb.GameTypePlayoffs, q.goalieArgs.GameType)
	assert.True(t, strings.HasPrefix(text, "# team_id=8 season=20252026 game_type=playoffs "), text)
}

func TestTeamSummaryRejectsBadGameTypeBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"preseason", "preseason", `unknown game_type "preseason"; accepted: regular_season, playoffs`},
		{"unknown", "all", `unknown game_type "all"; accepted: regular_season, playoffs`},
		{"not a string", float64(2), "invalid game_type 2: want a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &teamSummaryQueryFake{}
			text, isError := callTeamSummary(t, q, withArg(teamSummaryArgsFor(8, testSeasonID), teamSummaryGameTypeArg, tt.value))
			assert.True(t, isError)
			assert.Equal(t, tt.want, text)
			assert.Empty(t, q.calls)
		})
	}
}

func TestTeamSummaryReportsEachQueryError(t *testing.T) {
	for _, failing := range []string{"goals", "skaters", "goalies"} {
		t.Run(failing, func(t *testing.T) {
			q := &teamSummaryQueryFake{failing: failing}
			text, isError := callTeamSummary(t, q, teamSummaryArgsFor(8, testSeasonID))
			assert.True(t, isError)
			assert.Contains(t, text, errFakeQuery.Error())
			assert.Equal(t, failing, q.calls[len(q.calls)-1], "no query runs after a failure")
		})
	}
}

func TestTeamSummaryRendersHeaderAndRoundedSections(t *testing.T) {
	q := &teamSummaryQueryFake{
		goals: sqlcdb.GetTeamGoalsPerGameRow{
			GamesPlayed:         3,
			GoalsForPerGame:     numeric(t, "3.3333333333"),
			GoalsAgainstPerGame: numeric(t, "2.6666666667"),
		},
		skaters: []sqlcdb.GetTeamSkaterSeasonTotalsRow{{
			PlayerID: 8480018, FirstName: "Nick", LastName: "Suzuki",
			Position:    sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true},
			GamesPlayed: 3, TotalGoals: 1, TotalAssists: 2, TotalPoints: 3, TotalPlusMinus: -1, TotalShots: 7, TotalPim: 2,
		}},
		goalies: []sqlcdb.GetTeamGoalieSeasonTotalsRow{
			{
				PlayerID: 8478470, FirstName: "Sam", LastName: "Montembeault",
				GamesPlayed: 3, Starts: 3, Wins: 2, Losses: 1, TotalShotsAgainst: 90, TotalSaves: 82,
				TotalGoalsAgainst: 8, TotalToiSeconds: 10800, SeasonSavePctg: 82.0 / 90.0, SeasonGaa: 8.0 / 3.0,
			},
			// A dressed backup who never played: no shots, no time.
			{PlayerID: 8482487, FirstName: "Jakub", LastName: "Dobes", GamesPlayed: 1},
		},
	}
	text, isError := callTeamSummary(t, q, teamSummaryArgsFor(8, testSeasonID))
	require.False(t, isError, text)

	assert.Equal(t, `# team_id=8 season=20252026 game_type=regular_season games_played=3 goals_for_per_game=3.33 goals_against_per_game=2.67
# skaters
`+wantSkaterColumns+`
8480018,Nick,Suzuki,C,3,1,2,3,-1,7,2
# goalies
`+wantGoalieColumns+`
8478470,Sam,Montembeault,3,3,2,1,0,90,82,8,10800,0.911,2.67
8482487,Jakub,Dobes,1,0,0,0,0,0,0,0,0,,
`, text)
}

func TestTeamSummaryLeavesAveragesEmptyWithoutGames(t *testing.T) {
	q := &teamSummaryQueryFake{goals: sqlcdb.GetTeamGoalsPerGameRow{
		GoalsForPerGame: numeric(t, "0"), GoalsAgainstPerGame: numeric(t, "0"),
	}}
	text, isError := callTeamSummary(t, q, teamSummaryArgsFor(8, testSeasonID))
	require.False(t, isError, text)

	assert.Equal(t, `# team_id=8 season=20252026 game_type=regular_season games_played=0 goals_for_per_game="" goals_against_per_game=""
# skaters
no results
# goalies
no results
`, text)
}

func TestTeamGoalieRowsRefuseUnexpectedFloatType(t *testing.T) {
	_, err := teamGoalieRows([]sqlcdb.GetTeamGoalieSeasonTotalsRow{{PlayerID: 1, SeasonSavePctg: "0.9"}})
	assert.EqualError(t, err, "goalie 1 save percentage: unexpected type string")
}

func numeric(t *testing.T, value string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	require.NoError(t, n.Scan(value))
	return n
}

// PostgreSQL fixture: club A's season with regular-season and playoff
// games, a game not played yet, a game of the previous season and a
// skater traded to club B, so every filter of the three queries matters.
const (
	summaryIDBase     int64 = 990774000
	summaryClubA            = summaryIDBase + 10
	summaryClubB            = summaryIDBase + 11
	summaryCenter           = summaryIDBase + 1
	summaryTraded           = summaryIDBase + 2
	summaryStarter          = summaryIDBase + 3
	summaryBackup           = summaryIDBase + 4
	summaryPlayoffG         = summaryIDBase + 5
	summarySeason     int32 = 20242025
	summaryPrevSeason int32 = 20232024
	summaryGameBase         = summaryIDBase * 10
	summaryUnusedClub       = summaryIDBase + 99
)

// summaryGame is one fixture game; a game not played yet scores 0-0, as
// the column default stores it.
type summaryGame struct {
	season               int32
	gameType             sqlcdb.GameType
	state                string
	home, away           int64
	homeScore, awayScore int32
}

var summaryGames = []summaryGame{
	{summarySeason, sqlcdb.GameTypeRegularSeason, "FINAL", summaryClubA, summaryClubB, 4, 2},
	{summarySeason, sqlcdb.GameTypeRegularSeason, "OFF", summaryClubB, summaryClubA, 3, 2},
	{summarySeason, sqlcdb.GameTypeRegularSeason, "FUT", summaryClubA, summaryClubB, 0, 0},
	{summarySeason, sqlcdb.GameTypePlayoffs, "FINAL", summaryClubA, summaryClubB, 5, 0},
	{summaryPrevSeason, sqlcdb.GameTypeRegularSeason, "FINAL", summaryClubA, summaryClubB, 6, 0},
}

// summarySkaterLine is one game_skater_stats row: game index, player,
// club, goals, assists, plus-minus, shots, PIM.
type summarySkaterLine struct {
	game                                   int
	player, club                           int64
	goals, assists, plusMinus, shots, pims int32
}

var summarySkaterLines = []summarySkaterLine{
	{0, summaryCenter, summaryClubA, 1, 1, 1, 3, 2},
	{1, summaryCenter, summaryClubA, 0, 2, -1, 4, 0},
	{3, summaryCenter, summaryClubA, 2, 0, 2, 5, 0},
	{4, summaryCenter, summaryClubA, 3, 0, 3, 6, 0},
	{0, summaryTraded, summaryClubA, 0, 1, 0, 1, 0},
	{1, summaryTraded, summaryClubB, 1, 0, 1, 2, 0},
}

// summaryGoalieLine is one game_goalie_stats row.
type summaryGoalieLine struct {
	game                  int
	player                int64
	starter               bool
	decision              pgtype.Text
	shots, saves, ga, toi int32
}

func decision(d string) pgtype.Text { return pgtype.Text{String: d, Valid: true} }

var summaryGoalieLines = []summaryGoalieLine{
	{0, summaryStarter, true, decision("W"), 30, 28, 2, 3600},
	{1, summaryStarter, true, decision("L"), 25, 22, 3, 3600},
	{0, summaryBackup, false, pgtype.Text{}, 0, 0, 0, 0},
	{3, summaryPlayoffG, true, decision("W"), 20, 20, 0, 3600},
}

func TestTeamSummaryFromDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanupSummaryFixture(t, pool)
	t.Cleanup(func() { cleanupSummaryFixture(t, pool) })
	seedSummaryFixture(t, pool)
	q := sqlcdb.New(pool)

	text, isError := callTeamSummary(t, q, teamSummaryArgsFor(summaryClubA, summarySeason))
	require.False(t, isError, text)
	assert.Equal(t, `# team_id=990774010 season=20242025 game_type=regular_season games_played=2 goals_for_per_game=3.00 goals_against_per_game=2.50
# skaters
`+wantSkaterColumns+`
990774001,Summary,1,C,2,1,3,4,0,7,2
990774002,Summary,2,D,1,0,1,1,0,1,0
# goalies
`+wantGoalieColumns+`
990774003,Summary,3,2,2,1,1,0,55,50,5,7200,0.909,2.50
990774004,Summary,4,1,0,0,0,0,0,0,0,0,,
`, text)

	text, isError = callTeamSummary(t, q, withArg(teamSummaryArgsFor(summaryClubA, summarySeason), teamSummaryGameTypeArg, "playoffs"))
	require.False(t, isError, text)
	assert.Equal(t, `# team_id=990774010 season=20242025 game_type=playoffs games_played=1 goals_for_per_game=5.00 goals_against_per_game=0.00
# skaters
`+wantSkaterColumns+`
990774001,Summary,1,C,1,2,0,2,2,5,0
# goalies
`+wantGoalieColumns+`
990774005,Summary,5,1,1,1,0,0,20,20,0,3600,1.000,0.00
`, text)

	text, isError = callTeamSummary(t, q, teamSummaryArgsFor(summaryUnusedClub, summarySeason))
	require.False(t, isError, text)
	assert.Equal(t, `# team_id=990774099 season=20242025 game_type=regular_season games_played=0 goals_for_per_game="" goals_against_per_game=""
# skaters
no results
# goalies
no results
`, text)
}

func seedSummaryFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO players (id, first_name, last_name, position)
	      SELECT $1 + id, 'Summary', id::text, CASE id WHEN 1 THEN 'C' WHEN 2 THEN 'D' ELSE 'G' END::player_position
	      FROM generate_series(1, 5) AS id`, summaryIDBase)
	for i, g := range summaryGames {
		exec(`INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id, home_team_score, away_team_score)
		      VALUES ($1, $2, $3, '2024-10-10'::date + $4::int, $5, $6, $7, $8, $9)`,
			summaryGameBase+int64(i), g.season, g.gameType, i, g.state, g.home, g.away, g.homeScore, g.awayScore)
	}
	for _, l := range summarySkaterLines {
		g := summaryGames[l.game]
		exec(`INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position,
		      goals, assists, points, plus_minus, shots_on_goal, penalty_minutes)
		      SELECT $1, $2, $3, $4, 1, p.position, $5, $6, $7, $8, $9, $10 FROM players p WHERE p.id = $2`,
			summaryGameBase+int64(l.game), l.player, l.club, l.club == g.home,
			l.goals, l.assists, l.goals+l.assists, l.plusMinus, l.shots, l.pims)
	}
	for _, l := range summaryGoalieLines {
		exec(`INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, starter, decision,
		      shots_against, saves, goals_against, toi_seconds)
		      VALUES ($1, $2, $3, true, 30, $4, $5, $6, $7, $8, $9)`,
			summaryGameBase+int64(l.game), l.player, summaryClubA, l.starter, l.decision, l.shots, l.saves, l.ga, l.toi)
	}
}

// cleanupSummaryFixture removes the fixture; the stat rows cascade from
// games.
func cleanupSummaryFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DELETE FROM games WHERE id BETWEEN $1 AND $1 + 99`, summaryGameBase)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM players WHERE id BETWEEN $1 + 1 AND $1 + 5`, summaryIDBase)
	require.NoError(t, err)
}

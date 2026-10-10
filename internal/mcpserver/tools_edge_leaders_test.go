package mcpserver

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const edgeTestSeason = 20242025

type edgeLeadersQueryFake struct {
	calls   int
	skater  sqlcdb.GetEdgeSkaterLeadersParams
	goalie  sqlcdb.GetEdgeGoalieLeadersParams
	team    sqlcdb.GetEdgeTeamLeadersParams
	skaters []sqlcdb.GetEdgeSkaterLeadersRow
	goalies []sqlcdb.GetEdgeGoalieLeadersRow
	teams   []sqlcdb.GetEdgeTeamLeadersRow
}

func (f *edgeLeadersQueryFake) GetEdgeSkaterLeaders(_ context.Context, p sqlcdb.GetEdgeSkaterLeadersParams) ([]sqlcdb.GetEdgeSkaterLeadersRow, error) {
	f.calls++
	f.skater = p
	return f.skaters, nil
}

func (f *edgeLeadersQueryFake) GetEdgeGoalieLeaders(_ context.Context, p sqlcdb.GetEdgeGoalieLeadersParams) ([]sqlcdb.GetEdgeGoalieLeadersRow, error) {
	f.calls++
	f.goalie = p
	return f.goalies, nil
}

func (f *edgeLeadersQueryFake) GetEdgeTeamLeaders(_ context.Context, p sqlcdb.GetEdgeTeamLeadersParams) ([]sqlcdb.GetEdgeTeamLeadersRow, error) {
	f.calls++
	f.team = p
	return f.teams, nil
}

// callEdgeLeaders calls get_edge_leaders over q and returns the result text.
func callEdgeLeaders(t *testing.T, q edgeLeadersQueries, args map[string]any) (string, bool) {
	t.Helper()
	srv := server.NewMCPServer("test", serverVersion)
	srv.AddTools(edgeLeadersTool(q))
	tool := srv.GetTool(edgeLeadersToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, args)
	return resultText(t, result), result.IsError
}

// splitLeaders splits a get_edge_leaders result into its metadata header
// line and the CSV rows of its leaders section.
func splitLeaders(t *testing.T, text string) (string, [][]string) {
	t.Helper()
	header, rest, ok := strings.Cut(text, "\n")
	require.True(t, ok, text)
	section, csvText, ok := strings.Cut(rest, "\n")
	require.True(t, ok, text)
	require.Equal(t, commentPrefix+edgeLeadersSection, section)
	if strings.TrimSpace(csvText) == noResultsText {
		return header, nil
	}
	return header, parseCSV(t, csvText)
}

func f4v(v float32) pgtype.Float4 { return pgtype.Float4{Float32: v, Valid: true} }
func i4v(v int32) pgtype.Int4     { return pgtype.Int4{Int32: v, Valid: true} }

func TestEdgeLeadersSkaterDefaultsAndRows(t *testing.T) {
	q := &edgeLeadersQueryFake{skaters: []sqlcdb.GetEdgeSkaterLeadersRow{
		{PlayerID: 1, FirstName: "A", LastName: "Fast", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true},
			GamesPlayed: 82, Teams: "MTL", TopSpeedImperial: f4v(24.1), TopSpeedMetric: f4v(38.8), TopSpeedPercentile: f4v(0.99),
			TopSpeedLeagueAvgImperial: f4v(22.1), TopSpeedLeagueAvgMetric: f4v(35.6)},
		{PlayerID: 2, FirstName: "B", LastName: "Tied", GamesPlayed: 0, TopSpeedImperial: f4v(24.1)},
		{PlayerID: 3, FirstName: "C", LastName: "Slow", GamesPlayed: 10, Teams: "BOS/TOR", TopSpeedImperial: f4v(23)},
	}}

	text, isError := callEdgeLeaders(t, q, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: "Skater", edgeMetricArg: "TOP_SPEED",
	})
	require.False(t, isError, text)

	assert.Equal(t, sqlcdb.GetEdgeSkaterLeadersParams{
		Season: edgeTestSeason, GameType: sqlcdb.GameTypeRegularSeason, SortBy: "top_speed",
		ResultLimit: defaultEdgeLeadersLimit,
	}, q.skater)
	header, rows := splitLeaders(t, text)
	assert.Equal(t, `# group=skater metric=top_speed season=20242025 game_type=regular_season order=desc unit=mph value_metric_unit=km/h`, header)
	assert.Equal(t, []string{"rank", "player_id", "first_name", "last_name", "position", "teams", "gp",
		"value", "value_metric", "percentile", "league_avg", "league_avg_metric"}, rows[0])
	assert.Equal(t, []string{"1", "1", "A", "Fast", "C", "MTL", "82", "24.1", "38.8", "0.99", "22.1", "35.6"}, rows[1])
	assert.Equal(t, []string{"1", "2", "B", "Tied", "", "", "", "24.1", "", "", "", ""}, rows[2], "ties share a rank; 0 games is unknown")
	assert.Equal(t, []string{"3", "3", "C", "Slow", "", "BOS/TOR", "10", "23", "", "", "", ""}, rows[3])
}

func TestEdgeLeadersGoalieAscendingWithMinGames(t *testing.T) {
	q := &edgeLeadersQueryFake{goalies: []sqlcdb.GetEdgeGoalieLeadersRow{
		{PlayerID: 30, FirstName: "G", LastName: "Wall", GamesPlayed: 60, GaaValue: f4v(2.05), GaaPercentile: f4v(0.97), GaaLeagueAvg: f4v(2.9)},
	}}

	text, isError := callEdgeLeaders(t, q, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupGoalie, edgeMetricArg: "gaa",
		edgeGameTypeArg: "playoffs", edgeMinGamesArg: float64(20), "limit": float64(maxEdgeLeadersLimit),
	})
	require.False(t, isError, text)

	assert.Equal(t, sqlcdb.GetEdgeGoalieLeadersParams{
		Season: edgeTestSeason, GameType: sqlcdb.GameTypePlayoffs, SortBy: "gaa",
		MinGames: 20, Ascending: true, ResultLimit: maxEdgeLeadersLimit,
	}, q.goalie)
	header, rows := splitLeaders(t, text)
	assert.Equal(t, `# group=goalie metric=gaa season=20242025 game_type=playoffs order=asc min_games=20`, header)
	assert.Equal(t, []string{"1", "30", "G", "Wall", "", "", "60", "2.05", "", "0.97", "2.9", ""}, rows[1])
}

func TestEdgeLeadersTeamRows(t *testing.T) {
	q := &edgeLeadersQueryFake{teams: []sqlcdb.GetEdgeTeamLeadersRow{
		{TeamID: 8, Abbrev: "MTL", FullName: "Montréal Canadiens", DzPctg: f4v(0.38), DzRank: i4v(1), DzLeagueAvg: f4v(0.41)},
	}}

	text, isError := callEdgeLeaders(t, q, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupTeam, edgeMetricArg: "dz_pctg", "limit": float64(3),
	})
	require.False(t, isError, text)

	assert.Equal(t, sqlcdb.GetEdgeTeamLeadersParams{
		Season: edgeTestSeason, GameType: sqlcdb.GameTypeRegularSeason, SortBy: "dz_pctg", Ascending: true, ResultLimit: 3,
	}, q.team)
	header, rows := splitLeaders(t, text)
	assert.Equal(t, `# group=team metric=dz_pctg season=20242025 game_type=regular_season order=asc`, header)
	assert.Equal(t, []string{"rank", "team_id", "abbrev", "full_name", "value", "value_metric", "league_rank", "league_avg"}, rows[0])
	assert.Equal(t, []string{"1", "8", "MTL", "Montréal Canadiens", "0.38", "", "1", "0.41"}, rows[1])
}

func TestEdgeLeadersEmptyResult(t *testing.T) {
	text, isError := callEdgeLeaders(t, &edgeLeadersQueryFake{}, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupTeam, edgeMetricArg: "top_speed",
	})
	require.False(t, isError, text)
	_, rows := splitLeaders(t, text)
	assert.Empty(t, rows)
}

func TestEdgeLeadersRejectsBadArgumentsBeforeSQL(t *testing.T) {
	valid := map[string]any{"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupSkater, edgeMetricArg: "top_speed"}
	for name, tc := range map[string]struct {
		arg   string
		value any
		want  string
	}{
		"season start year":     {"season", float64(2024), "invalid season 2024: want an integer from 20212022 to 99999999"},
		"pre-Edge season":       {"season", float64(20202021), "invalid season 20202021"},
		"unknown group":         {edgeGroupArg, "skaters", `unknown group "skaters"; accepted groups: skater, goalie, team`},
		"metric of other group": {edgeMetricArg, "gaa", `unknown skater metric "gaa"; accepted metrics: top_speed, bursts_over_20,`},
		"injection":             {edgeMetricArg, "top_speed' OR 1=1 --", "unknown skater metric"},
		"preseason":             {edgeGameTypeArg, "preseason", `unknown game_type "preseason"; accepted: regular_season, playoffs`},
		"limit zero":            {"limit", float64(0), "invalid limit 0: want an integer from 1 to 200"},
		"limit above max":       {"limit", float64(maxEdgeLeadersLimit + 1), "invalid limit 201"},
		"min_games negative":    {edgeMinGamesArg, float64(-3), "invalid min_games -3"},
	} {
		t.Run(name, func(t *testing.T) {
			q := &edgeLeadersQueryFake{}
			text, isError := callEdgeLeaders(t, q, withArg(valid, tc.arg, tc.value))
			assert.True(t, isError)
			assert.Contains(t, text, tc.want)
			assert.Zero(t, q.calls)
		})
	}
}

func TestEdgeLeadersRefusesMinGamesForTeams(t *testing.T) {
	q := &edgeLeadersQueryFake{}
	text, isError := callEdgeLeaders(t, q, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupTeam, edgeMetricArg: "top_speed", edgeMinGamesArg: float64(10),
	})
	assert.True(t, isError)
	assert.Equal(t, "min_games applies to skater and goalie groups only", text)
	assert.Zero(t, q.calls)

	text, isError = callEdgeLeaders(t, q, map[string]any{
		"season": float64(edgeTestSeason), edgeGroupArg: edgeGroupTeam, edgeMetricArg: "top_speed", edgeMinGamesArg: float64(0),
	})
	assert.False(t, isError, text, "0 means no minimum")
}

// TestEdgeMetricsReadTheirOwnColumns gives every column of a row a distinct
// value and checks each metric prints a value no other metric of its group
// prints, so a copy-paste slip in the extractors shows up.
func TestEdgeMetricsReadTheirOwnColumns(t *testing.T) {
	assertDistinctValues(t, skaterEdgeMetrics, distinctRow[sqlcdb.GetEdgeSkaterLeadersRow]())
	assertDistinctValues(t, goalieEdgeMetrics, distinctRow[sqlcdb.GetEdgeGoalieLeadersRow]())
	assertDistinctValues(t, teamEdgeMetrics, distinctRow[sqlcdb.GetEdgeTeamLeadersRow]())
}

func assertDistinctValues[T any](t *testing.T, metrics edgeMetricSet[T], row T) {
	t.Helper()
	seen := map[string]string{}
	for _, m := range metrics {
		v := m.values(row)
		require.NotEmpty(t, v.value, m.key)
		require.NotEmpty(t, v.standing, m.key)
		assert.Equal(t, v.valueMetric == "", m.metricUnit == "", "%s: value_metric and its unit go together", m.key)
		if other, dup := seen[v.value]; dup {
			t.Errorf("%s and %s print the same value column", m.key, other)
		}
		seen[v.value] = m.key
	}
}

// distinctRow sets every int4 and float4 field of a query row to a value
// no other field holds.
func distinctRow[T any]() T {
	var row T
	v := reflect.ValueOf(&row).Elem()
	for i := range v.NumField() {
		switch f := v.Field(i).Addr().Interface().(type) {
		case *pgtype.Float4:
			*f = f4v(float32(i) + 0.5)
		case *pgtype.Int4:
			*f = i4v(int32(i))
		}
	}
	return row
}

// PostgreSQL fixture for the leaders queries: a season no import writes.
const (
	pgEdgeSeason      = 20992100
	pgEdgeFirstPlayer = 99076600
	pgEdgeSkaters     = 3
	pgEdgeGoalies     = 2
	pgEdgeTeams       = 3
	// pgEdgeNullPlayer has an Edge row with no metric, so no list shows it.
	pgEdgeNullPlayer = pgEdgeFirstPlayer + pgEdgeSkaters + pgEdgeGoalies
	pgEdgeMinGames   = 10
)

// pgEdgeColumns is the column each metric must sort by, per table; it is the
// test's own copy of the leaders queries' CASE, which it checks.
var pgEdgeColumns = map[string]map[string]string{
	edgeGroupSkater: {
		"top_speed": "top_speed_imperial", "bursts_over_20": "bursts_over_20",
		"total_distance": "total_distance_imperial", "max_game_distance": "max_game_distance_imperial",
		"top_shot_speed": "top_shot_speed_imperial", "oz_pctg": "oz_pctg", "oz_ev_pctg": "oz_ev_pctg",
		"nz_pctg": "nz_pctg", "dz_pctg": "dz_pctg",
	},
	edgeGroupGoalie: {
		"gaa": "gaa_value", "games_above_900": "games_above_900_value", "goal_diff_per_60": "goal_diff_per_60_value",
		"goal_support_avg": "goal_support_avg_value", "point_pctg": "point_pctg_value",
	},
	edgeGroupTeam: {
		"shot_attempts_over_90": "shot_attempts_over_90", "top_shot_speed": "top_shot_speed_imperial",
		"bursts_over_22": "bursts_over_22", "bursts_over_20": "bursts_over_20", "top_speed": "speed_max_imperial",
		"total_distance": "total_distance", "oz_pctg": "oz_pctg", "oz_ev_pctg": "oz_ev_pctg",
		"nz_pctg": "nz_pctg", "dz_pctg": "dz_pctg",
	},
}

var pgEdgeTables = map[string]string{
	edgeGroupSkater: "edge_skater_stats", edgeGroupGoalie: "edge_goalie_stats", edgeGroupTeam: "edge_team_stats",
}

// pgEdgePermutations give each metric a different order of the fixture
// rows, so a key sorting by another metric's column shows up.
var pgEdgePermutations = [][]int{{1, 2, 3}, {3, 1, 2}, {2, 3, 1}, {1, 3, 2}, {2, 1, 3}, {3, 2, 1}}

// pgEdgeEntity is one fixture row of a group: its key column and value.
type pgEdgeEntity struct {
	keyColumn string
	id        int64
}

func pgEdgeEntities(group string) []pgEdgeEntity {
	var entities []pgEdgeEntity
	switch group {
	case edgeGroupSkater:
		for i := range pgEdgeSkaters {
			entities = append(entities, pgEdgeEntity{"player_id", pgEdgeFirstPlayer + int64(i)})
		}
	case edgeGroupGoalie:
		for i := range pgEdgeGoalies {
			entities = append(entities, pgEdgeEntity{"player_id", pgEdgeFirstPlayer + pgEdgeSkaters + int64(i)})
		}
	default:
		for i := range pgEdgeTeams {
			entities = append(entities, pgEdgeEntity{"team_id", int64(i + 1)})
		}
	}
	return entities
}

// TestEdgeLeadersEveryMetricSortsInSQL runs every whitelisted metric of
// every group against PostgreSQL: the rows come back best first by that
// metric's own column, and rows without a value are left out.
func TestEdgeLeadersEveryMetricSortsInSQL(t *testing.T) {
	pool := openMCPTestDB(t)
	deleteEdgeFixture(t, pool)
	t.Cleanup(func() { deleteEdgeFixture(t, pool) })
	seedEdgeFixture(t, pool)
	q := sqlcdb.New(pool)

	for group, infos := range edgeMetricInfos {
		require.Len(t, pgEdgeColumns[group], len(infos), "pgEdgeColumns[%s] must cover every metric", group)
		for i, m := range infos {
			t.Run(group+"/"+m.key, func(t *testing.T) {
				entities := pgEdgeEntities(group)
				values := pgEdgePermutations[i%len(pgEdgePermutations)][:len(entities)]
				setEdgeMetric(t, pool, group, pgEdgeColumns[group][m.key], entities, values)

				text, isError := callEdgeLeaders(t, q, map[string]any{
					"season": float64(pgEdgeSeason), edgeGroupArg: group, edgeMetricArg: m.key,
				})
				require.False(t, isError, text)
				_, rows := splitLeaders(t, text)
				require.Len(t, rows, len(entities)+1, text)

				want := wantEdgeOrder(entities, values, m.ascending)
				for r, row := range rows[1:] {
					assert.Equal(t, want[r].id, mustParseInt(t, row[1]), "row %d of %s", r+1, text)
					assert.Equal(t, strconv.Itoa(want[r].value), row[columnIndex(t, rows[0], "value")])
					assert.Equal(t, strconv.Itoa(r+1), row[0])
				}
			})
		}
	}
}

// TestEdgeLeadersMinGamesUsesClubStats checks games played and clubs come
// from the club stats of the season, summed over a traded player's clubs.
func TestEdgeLeadersMinGamesUsesClubStats(t *testing.T) {
	pool := openMCPTestDB(t)
	deleteEdgeFixture(t, pool)
	t.Cleanup(func() { deleteEdgeFixture(t, pool) })
	seedEdgeFixture(t, pool)
	q := sqlcdb.New(pool)
	skaters, goalies := pgEdgeEntities(edgeGroupSkater), pgEdgeEntities(edgeGroupGoalie)
	setEdgeMetric(t, pool, edgeGroupSkater, "top_speed_imperial", skaters, []int{3, 2, 1})
	setEdgeMetric(t, pool, edgeGroupGoalie, "gaa_value", goalies, []int{1, 2})

	text, isError := callEdgeLeaders(t, q, map[string]any{
		"season": float64(pgEdgeSeason), edgeGroupArg: edgeGroupSkater, edgeMetricArg: "top_speed", edgeMinGamesArg: float64(pgEdgeMinGames),
	})
	require.False(t, isError, text)
	_, rows := splitLeaders(t, text)
	require.Len(t, rows, 3, "the skater without club stats counts as 0 games: %s", text)
	assert.Equal(t, []string{"T1", "50"}, []string{rows[1][5], rows[1][6]})
	assert.Equal(t, []string{"T1/T2", "10"}, []string{rows[2][5], rows[2][6]}, "a traded skater's games add up")

	text, isError = callEdgeLeaders(t, q, map[string]any{
		"season": float64(pgEdgeSeason), edgeGroupArg: edgeGroupGoalie, edgeMetricArg: "gaa", edgeMinGamesArg: float64(pgEdgeMinGames),
	})
	require.False(t, isError, text)
	_, rows = splitLeaders(t, text)
	require.Len(t, rows, 2, text)
	assert.Equal(t, strconv.FormatInt(goalies[1].id, 10), rows[1][1], "the 2-game goalie with the best GAA is left out")
}

type pgEdgeExpected struct {
	id    int64
	value int
}

func wantEdgeOrder(entities []pgEdgeEntity, values []int, ascending bool) []pgEdgeExpected {
	want := make([]pgEdgeExpected, len(entities))
	for i, e := range entities {
		want[i] = pgEdgeExpected{e.id, values[i]}
	}
	slices.SortFunc(want, func(a, b pgEdgeExpected) int {
		if ascending {
			return a.value - b.value
		}
		return b.value - a.value
	})
	return want
}

func mustParseInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err)
	return n
}

// setEdgeMetric clears every metric column of the group's fixture rows, then
// sets column to values, so only that metric has data.
func setEdgeMetric(t *testing.T, pool *pgxpool.Pool, group, column string, entities []pgEdgeEntity, values []int) {
	t.Helper()
	ctx := context.Background()
	table := pgEdgeTables[group]
	var clear []string
	for _, c := range pgEdgeColumns[group] {
		clear = append(clear, c+" = NULL")
	}
	_, err := pool.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE season = $1", table, strings.Join(clear, ", ")), pgEdgeSeason)
	require.NoError(t, err)
	for i, e := range entities {
		_, err := pool.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s = $1 WHERE season = $2 AND %s = $3", table, column, e.keyColumn),
			values[i], pgEdgeSeason, e.id)
		require.NoError(t, err)
	}
}

func seedEdgeFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	exec(`INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '2099-10-01', '2100-04-15')`, pgEdgeSeason)
	for _, team := range pgEdgeEntities(edgeGroupTeam) {
		exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
			VALUES ($1, $2, 'Team', $3, 'Div', 'D')`, pgEdgeSeason, team.id, fmt.Sprintf("T%d", team.id))
		exec(`INSERT INTO edge_team_stats (team_id, season, game_type) VALUES ($1, $2, 'regular_season')`, team.id, pgEdgeSeason)
	}
	for _, group := range []string{edgeGroupSkater, edgeGroupGoalie} {
		position := "C"
		if group == edgeGroupGoalie {
			position = "G"
		}
		for _, p := range pgEdgeEntities(group) {
			exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, 'Edge', $2, $3)`, p.id, fmt.Sprint(p.id), position)
			exec(fmt.Sprintf(`INSERT INTO %s (player_id, season, game_type) VALUES ($1, $2, 'regular_season')`, pgEdgeTables[group]), p.id, pgEdgeSeason)
		}
	}
	exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, 'Edge', 'Null', 'C')`, pgEdgeNullPlayer)
	exec(`INSERT INTO edge_skater_stats (player_id, season, game_type) VALUES ($1, $2, 'regular_season')`, pgEdgeNullPlayer, pgEdgeSeason)

	skaters, goalies := pgEdgeEntities(edgeGroupSkater), pgEdgeEntities(edgeGroupGoalie)
	clubSkater := `INSERT INTO club_skater_stats (season, game_type, team_id, player_id, games_played, goals, assists, points,
		plus_minus, penalty_minutes, power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals, shots,
		shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg)
		VALUES ($1, 'regular_season', $2, $3, $4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)`
	exec(clubSkater, pgEdgeSeason, 1, skaters[0].id, 50)
	exec(clubSkater, pgEdgeSeason, 1, skaters[1].id, 5)
	exec(clubSkater, pgEdgeSeason, 2, skaters[1].id, 5)
	clubGoalie := `INSERT INTO club_goalie_stats (season, game_type, team_id, player_id, games_played, games_started, wins,
		losses, overtime_losses, goals_against_average, save_percentage, shots_against, saves, goals_against, shutouts,
		goals, assists, points, penalty_minutes, toi_seconds)
		VALUES ($1, 'regular_season', 1, $2, $3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)`
	exec(clubGoalie, pgEdgeSeason, goalies[0].id, 2)
	exec(clubGoalie, pgEdgeSeason, goalies[1].id, 30)
}

// deleteEdgeFixture removes the fixture season and players, children first.
func deleteEdgeFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"club_skater_stats", "club_goalie_stats", "edge_skater_stats", "edge_goalie_stats", "edge_team_stats", "season_teams"} {
		_, err := pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE season = $1", table), pgEdgeSeason)
		require.NoError(t, err, table)
	}
	_, err := pool.Exec(ctx, `DELETE FROM seasons WHERE id = $1`, pgEdgeSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM players WHERE id BETWEEN $1 AND $2`, pgEdgeFirstPlayer, pgEdgeNullPlayer)
	require.NoError(t, err)
}

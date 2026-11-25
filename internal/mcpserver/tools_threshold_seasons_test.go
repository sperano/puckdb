package mcpserver

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestThresholdToolsBindDefaults checks the value every optional argument
// takes when it is left out.
func TestThresholdToolsBindDefaults(t *testing.T) {
	tests := map[string]string{
		skaterThresholdToolName: "points",
		goalieThresholdToolName: "wins",
	}
	for tool, stat := range tests {
		t.Run(tool, func(t *testing.T) {
			result, db := callNHLTool(t, tool, map[string]any{"min_value": float64(80)})
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{
				int32(80), int32(defaultMinThresholdSeasons), int32(defaultThresholdLimit), stat,
				sqlcdb.GameTypeRegularSeason, int32(firstNHLSeasonID), int32(lastSeasonID),
			}, db.calls[0].args)
		})
	}
}

// TestThresholdToolsBindGivenArguments checks each given argument reaches
// SQL, the stat and game type case-insensitively and trimmed.
func TestThresholdToolsBindGivenArguments(t *testing.T) {
	result, db := callNHLTool(t, skaterThresholdToolName, map[string]any{
		thresholdStatArg: " Goals ", "min_value": float64(0), "min_seasons": float64(10),
		"from_season": float64(20012002), "to_season": float64(20012002),
		thresholdGameTypeArg: "PLAYOFFS", "limit": float64(maxThresholdLimit),
	})
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, db.calls, 1)
	assert.Equal(t, []any{
		int32(0), int32(10), int32(maxThresholdLimit), "goals",
		sqlcdb.GameTypePlayoffs, int32(20012002), int32(20012002),
	}, db.calls[0].args)
}

// TestThresholdToolsRejectInvalidArguments checks every refusal is a tool
// error naming the problem, before any SQL runs.
func TestThresholdToolsRejectInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"unknown stat", skaterThresholdToolName, map[string]any{thresholdStatArg: "hits"},
			`unknown stat "hits"; accepted: points, goals, assists, plus_minus, pim, ppg, shg, gwg, otg, sog, gp`},
		{"skater stat for goalies", goalieThresholdToolName, map[string]any{thresholdStatArg: "points"},
			`unknown stat "points"; accepted: wins, losses, otl, shutouts, saves, shots_against, gp, gs`},
		{"stat not a string", skaterThresholdToolName, map[string]any{thresholdStatArg: float64(1)},
			"invalid stat 1: want a string"},
		{"seasons reversed", skaterThresholdToolName, map[string]any{"from_season": float64(20252026), "to_season": float64(20242025)},
			"from_season 20252026 is after to_season 20242025"},
		{"unknown game type", goalieThresholdToolName, map[string]any{thresholdGameTypeArg: "preseason"},
			`unknown game_type "preseason"; accepted: regular_season, playoffs`},
		{"limit above maximum", skaterThresholdToolName, map[string]any{"limit": float64(maxThresholdLimit + 1)},
			fmt.Sprintf("invalid limit %d: want an integer from 0 to %d", maxThresholdLimit+1, maxThresholdLimit)},
		{"no qualifying season needed", skaterThresholdToolName, map[string]any{"min_seasons": float64(0)},
			fmt.Sprintf("invalid min_seasons 0: want an integer from 1 to %d", math.MaxInt32)},
		{"season before the NHL", goalieThresholdToolName, map[string]any{"from_season": float64(19001901)},
			fmt.Sprintf("invalid from_season 19001901: want an integer from %d to %d", firstNHLSeasonID, lastSeasonID)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"min_value": float64(1)}
			for name, value := range tt.args {
				args[name] = value
			}
			result, db := callNHLTool(t, tt.tool, args)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
			assert.Empty(t, db.calls)
		})
	}
}

// Fixture seasons, clubs and players of the PostgreSQL tests; far from
// real data and from the other packages' fixtures.
const (
	thresholdSeasonA   = 20912092
	thresholdSeasonB   = 20922093
	thresholdSeasonC   = 20932094
	thresholdSeasonD   = 20942095
	thresholdFirstClub = 1
	thresholdOtherClub = 2
	thresholdPlayer1   = 99086601 // Alpha One: traded in season B
	thresholdPlayer2   = 99086602 // Beta Two: three seasons
	thresholdPlayer3   = 99086603 // Gamma Three: regular season and playoffs
	thresholdPlayer4   = 99086604 // Delta Four: season C only
	thresholdPlayer5   = 99086605 // Echo Five: a distinct value in every column, season D
	thresholdGoalie    = 99086606 // Golf Six: traded in season A, playoffs in season A
	thresholdGoalie2   = 99086607 // Hotel Seven: two seasons
)

var thresholdSeasons = []int32{thresholdSeasonA, thresholdSeasonB, thresholdSeasonC, thresholdSeasonD}

// TestThresholdSeasonsSkatersAgainstPostgres checks qualifying seasons are
// counted on the sum of a season's clubs, inclusively, within the season
// range and game type, with the documented order and limit.
func TestThresholdSeasonsSkatersAgainstPostgres(t *testing.T) {
	q := thresholdFixture(t)
	const header = "player_id,first_name,last_name,position,qualifying_seasons,best_value,seasons\n"
	beta := "99086602,Beta,Two,C,3,120,20912092:100 20922093:80 20932094:120\n"
	alpha := "99086601,Alpha,One,C,2,90,20912092:90 20922093:90\n"
	gamma := "99086603,Gamma,Three,C,1,85,20912092:85\n"
	delta := "99086604,Delta,Four,C,1,81,20932094:81\n"
	upToC := map[string]any{"min_value": float64(80), "to_season": float64(thresholdSeasonC)}
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"every season", upToC, header + beta + alpha + gamma + delta},
		{"min seasons", withArg(upToC, "min_seasons", float64(2)), header + beta + alpha},
		{"season range", withArg(upToC, "from_season", float64(thresholdSeasonB)),
			header + "99086602,Beta,Two,C,2,120,20922093:80 20932094:120\n" +
				"99086601,Alpha,One,C,1,90,20922093:90\n" + delta},
		{"playoffs", withArg(upToC, thresholdGameTypeArg, "playoffs"), header + "99086603,Gamma,Three,C,1,200,20912092:200\n"},
		{"limit", withArg(upToC, "limit", float64(1)), header + beta},
		{"none qualifies", withArg(upToC, "min_seasons", float64(4)), noResultsText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isError := callThreshold(t, q, skaterThresholdToolName, tt.args)
			require.False(t, isError, text)
			assert.Equal(t, tt.want, text)
		})
	}
}

// TestThresholdSeasonsEverySkaterStatReadsItsColumn sweeps every skater
// stat key over a row holding a distinct value in each column.
func TestThresholdSeasonsEverySkaterStatReadsItsColumn(t *testing.T) {
	q := thresholdFixture(t)
	want := map[string]int{
		"gp": 1, "goals": 2, "assists": 3, "points": 4, "plus_minus": 5, "pim": 6,
		"ppg": 7, "shg": 8, "gwg": 9, "otg": 10, "sog": 11,
	}
	require.Len(t, want, len(skaterThresholdStats))
	for _, stat := range skaterThresholdStats {
		t.Run(stat, func(t *testing.T) {
			text, isError := callThreshold(t, q, skaterThresholdToolName, map[string]any{
				thresholdStatArg: stat, "min_value": float64(0),
				"from_season": float64(thresholdSeasonD), "to_season": float64(thresholdSeasonD),
			})
			require.False(t, isError, text)
			assert.Equal(t, fmt.Sprintf("player_id,first_name,last_name,position,qualifying_seasons,best_value,seasons\n"+
				"99086605,Echo,Five,C,1,%[1]d,20942095:%[1]d\n", want[stat]), text)
		})
	}
}

// TestThresholdSeasonsGoaliesAgainstPostgres checks the goalie tool sums a
// traded goalie's clubs and honours min_seasons, the season range, the game
// type and the limit, and that every goalie stat key reads its column.
func TestThresholdSeasonsGoaliesAgainstPostgres(t *testing.T) {
	q := thresholdFixture(t)
	const header = "player_id,first_name,last_name,qualifying_seasons,best_value,seasons\n"
	hotel := "99086607,Hotel,Seven,2,45,20912092:45 20922093:42\n"
	golf := "99086606,Golf,Six,1,41,20912092:41\n"
	upToC := map[string]any{"min_value": float64(40), "to_season": float64(thresholdSeasonC)}
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"every season", upToC, header + hotel + golf},
		{"min seasons", withArg(upToC, "min_seasons", float64(2)), header + hotel},
		{"season range", withArg(upToC, "from_season", float64(thresholdSeasonB)),
			header + "99086607,Hotel,Seven,1,42,20922093:42\n"},
		{"playoffs", withArg(withArg(upToC, thresholdGameTypeArg, "playoffs"), "min_value", float64(10)),
			header + "99086606,Golf,Six,1,12,20912092:12\n"},
		{"limit", withArg(upToC, "limit", float64(1)), header + hotel},
		{"none qualifies", withArg(upToC, "min_seasons", float64(3)), noResultsText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isError := callThreshold(t, q, goalieThresholdToolName, tt.args)
			require.False(t, isError, text)
			assert.Equal(t, tt.want, text)
		})
	}

	// Season D holds one club row with a distinct value per column.
	want := map[string]int{"gp": 1, "gs": 2, "wins": 3, "losses": 4, "otl": 5, "shots_against": 6, "saves": 7, "shutouts": 8}
	require.Len(t, want, len(goalieThresholdStats))
	for _, stat := range goalieThresholdStats {
		t.Run(stat, func(t *testing.T) {
			text, isError := callThreshold(t, q, goalieThresholdToolName, map[string]any{
				thresholdStatArg: stat, "min_value": float64(0),
				"from_season": float64(thresholdSeasonD), "to_season": float64(thresholdSeasonD),
			})
			require.False(t, isError, text)
			assert.Equal(t, fmt.Sprintf("player_id,first_name,last_name,qualifying_seasons,best_value,seasons\n"+
				"99086606,Golf,Six,1,%[1]d,20942095:%[1]d\n", want[stat]), text)
		})
	}
}

func callThreshold(t *testing.T, q *sqlcdb.Queries, name string, args map[string]any) (string, bool) {
	t.Helper()
	for _, tool := range thresholdTools(q) {
		if tool.Tool.Name == name {
			result := callTool(t, tool.Handler, args)
			return resultText(t, result), result.IsError
		}
	}
	t.Fatalf("no tool %s", name)
	return "", false
}

// thresholdFixture seeds the fixture into the test database and removes
// it again at cleanup.
func thresholdFixture(t *testing.T) *sqlcdb.Queries {
	t.Helper()
	pool := pgtest.Open(t)
	deleteThresholdFixture(t, pool)
	t.Cleanup(func() { deleteThresholdFixture(t, pool) })
	seedThresholdFixture(t, pool)
	return sqlcdb.New(pool)
}

func seedThresholdFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(context.Background(), sql, args...)
		require.NoError(t, err, sql)
	}
	for i, season := range thresholdSeasons {
		exec(`INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, $2, $3)`,
			season, fmt.Sprintf("%d-10-01", 2091+i), fmt.Sprintf("%d-04-15", 2092+i))
		for _, club := range []int{thresholdFirstClub, thresholdOtherClub} {
			exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
				VALUES ($1, $2, 'Team', $3, 'Div', 'D')`, season, club, fmt.Sprintf("T%d", club))
		}
	}
	players := map[int]string{
		thresholdPlayer1: "Alpha One", thresholdPlayer2: "Beta Two", thresholdPlayer3: "Gamma Three",
		thresholdPlayer4: "Delta Four", thresholdPlayer5: "Echo Five",
	}
	for id, name := range players {
		var first, last string
		_, err := fmt.Sscan(name, &first, &last)
		require.NoError(t, err)
		exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, $2, $3, 'C')`, id, first, last)
	}
	exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, 'Golf', 'Six', 'G')`, thresholdGoalie)
	exec(`INSERT INTO players (id, first_name, last_name, position) VALUES ($1, 'Hotel', 'Seven', 'G')`, thresholdGoalie2)

	skater := `INSERT INTO club_skater_stats (season, game_type, team_id, player_id, games_played, goals, assists, points,
		plus_minus, penalty_minutes, power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals, shots,
		shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg)
		VALUES ($1, $2, $3, $4, 0, 0, 0, $5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)`
	points := []struct {
		season, club, player, points int
		gameType                     string
	}{
		{thresholdSeasonA, thresholdFirstClub, thresholdPlayer1, 90, "regular_season"},
		{thresholdSeasonB, thresholdFirstClub, thresholdPlayer1, 50, "regular_season"},
		{thresholdSeasonB, thresholdOtherClub, thresholdPlayer1, 40, "regular_season"},
		{thresholdSeasonC, thresholdFirstClub, thresholdPlayer1, 79, "regular_season"},
		{thresholdSeasonA, thresholdFirstClub, thresholdPlayer2, 100, "regular_season"},
		{thresholdSeasonB, thresholdFirstClub, thresholdPlayer2, 80, "regular_season"},
		{thresholdSeasonC, thresholdFirstClub, thresholdPlayer2, 120, "regular_season"},
		{thresholdSeasonA, thresholdFirstClub, thresholdPlayer3, 85, "regular_season"},
		{thresholdSeasonA, thresholdFirstClub, thresholdPlayer3, 200, "playoffs"},
		{thresholdSeasonC, thresholdOtherClub, thresholdPlayer4, 81, "regular_season"},
	}
	for _, row := range points {
		exec(skater, row.season, row.gameType, row.club, row.player, row.points)
	}
	exec(`INSERT INTO club_skater_stats (season, game_type, team_id, player_id, games_played, goals, assists, points,
		plus_minus, penalty_minutes, power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals, shots,
		shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg)
		VALUES ($1, 'regular_season', $2, $3, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0, 0, 0, 0)`,
		thresholdSeasonD, thresholdFirstClub, thresholdPlayer5)

	goalie := `INSERT INTO club_goalie_stats (season, game_type, team_id, player_id, games_played, games_started, wins,
		losses, overtime_losses, goals_against_average, save_percentage, shots_against, saves, goals_against, shutouts,
		goals, assists, points, penalty_minutes, toi_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, 0, $10, $11, 0, $12, 0, 0, 0, 0, 0)`
	const regular, playoffs = "regular_season", "playoffs"
	exec(goalie, thresholdSeasonA, regular, thresholdFirstClub, thresholdGoalie, 0, 0, 20, 0, 0, 0, 0, 0)
	exec(goalie, thresholdSeasonA, regular, thresholdOtherClub, thresholdGoalie, 0, 0, 21, 0, 0, 0, 0, 0)
	exec(goalie, thresholdSeasonA, playoffs, thresholdOtherClub, thresholdGoalie, 0, 0, 12, 0, 0, 0, 0, 0)
	exec(goalie, thresholdSeasonB, regular, thresholdFirstClub, thresholdGoalie, 0, 0, 39, 0, 0, 0, 0, 0)
	exec(goalie, thresholdSeasonD, regular, thresholdFirstClub, thresholdGoalie, 1, 2, 3, 4, 5, 6, 7, 8)
	exec(goalie, thresholdSeasonA, regular, thresholdFirstClub, thresholdGoalie2, 0, 0, 45, 0, 0, 0, 0, 0)
	exec(goalie, thresholdSeasonB, regular, thresholdOtherClub, thresholdGoalie2, 0, 0, 42, 0, 0, 0, 0, 0)
}

// deleteThresholdFixture removes the fixture seasons and players, children
// first.
func deleteThresholdFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"club_skater_stats", "club_goalie_stats", "season_teams"} {
		_, err := pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE season = ANY($1)", table), thresholdSeasons)
		require.NoError(t, err, table)
	}
	_, err := pool.Exec(ctx, `DELETE FROM seasons WHERE id = ANY($1)`, thresholdSeasons)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM players WHERE id BETWEEN $1 AND $2`, thresholdPlayer1, thresholdGoalie2)
	require.NoError(t, err)
}

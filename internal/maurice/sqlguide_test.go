package maurice

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqlGuideColumns lists every table and column the guide names, so
// TestSQLGuide_NamesExistingColumns fails when a migration renames or drops
// one and the guide would send the model to a column that is not there.
var sqlGuideColumns = map[string][]string{
	"players": {"id", "first_name", "last_name", "position", "team_id", "birth_date",
		"is_active", "draft_year", "draft_overall_pick"},
	"club_skater_stats": {"season", "game_type", "team_id", "player_id", "games_played", "goals",
		"assists", "points", "plus_minus", "penalty_minutes", "power_play_goals", "shots", "avg_toi_per_game"},
	"club_goalie_stats": {"season", "game_type", "team_id", "player_id", "games_played", "games_started",
		"wins", "losses", "overtime_losses", "saves", "shots_against", "goals_against_average",
		"save_percentage", "shutouts"},
	"player_season_totals": {"player_id", "season", "game_type", "league_abbrev", "sequence",
		"games_played", "goals", "assists", "points", "plus_minus", "pim"},
	"games": {"id", "season", "game_type", "game_date", "home_team_id", "away_team_id",
		"home_team_score", "away_team_score", "game_state"},
	"game_skater_stats": {"game_id", "player_id", "team_id", "shots_on_goal", "hits", "blocked_shots",
		"power_play_points", "toi_seconds"},
	"game_goalie_stats": {"game_id", "player_id", "team_id", "decision", "saves", "shots_against",
		"goals_against", "toi_seconds"},
	"season_teams": {"season", "team_id", "abbrev", "full_name"},
}

// sqlGuideEnumLabels lists the enum values the guide quotes, by PostgreSQL
// type.
var sqlGuideEnumLabels = map[string][]string{
	"game_type":       {"regular_season", "playoffs", "all_star"},
	"player_position": {"C", "LW", "RW", "D", "G"},
	"game_state":      {"FUT", "LIVE", "OFF", "FINAL"},
}

func TestSystemPrompt_IncludesSQLGuide(t *testing.T) {
	assert.Contains(t, SystemPrompt(nil), "\n\n"+sqlGuide)
}

// guideTablePattern matches a guide bullet that describes tables: one or
// more table names separated by " / ", then ':' or " (".
var guideTablePattern = regexp.MustCompile(`(?m)^- ([a-z_]+(?: / [a-z_]+)*)(?::| \()`)

// guideTables returns the tables the guide's bullets describe.
func guideTables(guide string) []string {
	var tables []string
	for _, m := range guideTablePattern.FindAllStringSubmatch(guide, -1) {
		tables = append(tables, strings.Split(m[1], " / ")...)
	}
	return tables
}

// TestSQLGuide_NamesListedTables keeps sqlGuideColumns in step with the
// guide: the tables its bullets describe are exactly the listed ones, so a
// table the guide adds without listing it here, which the database test
// would leave unchecked, fails.
func TestSQLGuide_NamesListedTables(t *testing.T) {
	assert.ElementsMatch(t, slices.Collect(maps.Keys(sqlGuideColumns)), guideTables(sqlGuide))
	for _, labels := range sqlGuideEnumLabels {
		for _, label := range labels {
			assert.Contains(t, sqlGuide, "'"+label+"'")
		}
	}
}

func TestGuideTables_ReadsTableBulletsOnly(t *testing.T) {
	guide := "intro\n- Every season column is an int: x.\n- a_one / a_two: cols.\n" +
		"- b_one (c1, c2) names.\n- An error like \"x\" (y).\n- games_played is not a bullet table"
	assert.Equal(t, []string{"a_one", "a_two", "b_one"}, guideTables(guide))
}

func TestSQLGuide_NamesExistingColumns(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	for table, columns := range sqlGuideColumns {
		rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`, table)
		require.NoError(t, err)
		existing := map[string]bool{}
		for rows.Next() {
			var name string
			require.NoError(t, rows.Scan(&name))
			existing[name] = true
		}
		require.NoError(t, rows.Err())
		for _, column := range columns {
			assert.True(t, existing[column], "%s.%s does not exist", table, column)
		}
	}
	for typ, labels := range sqlGuideEnumLabels {
		var got []string
		require.NoError(t, pool.QueryRow(ctx, `SELECT array_agg(e.enumlabel::text) FROM pg_enum e
			JOIN pg_type t ON t.oid = e.enumtypid WHERE t.typname = $1`, typ).Scan(&got))
		for _, label := range labels {
			assert.Contains(t, got, label, "enum %s", typ)
		}
	}
}

// TestSQLGuide_PointsNHLSeasonsAtClubTables pins the guidance the 80-point
// seasons conversation lacked: player_season_totals lags for recent NHL
// seasons, so NHL questions go to the club_* tables.
func TestSQLGuide_PointsNHLSeasonsAtClubTables(t *testing.T) {
	assert.Contains(t, sqlGuide, "use it for other leagues only and club_* tables for the NHL")
	assert.Contains(t, sqlGuide, "SUM over team_id")
}

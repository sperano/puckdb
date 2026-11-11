package projection

// PostgreSQL-backed tests of the season window ListProjectionSkaterSeasons
// and ListProjectionGoalieSeasons read: a projection reads only seasons before
// its target, played by its as-of cutoff; an evaluation reads the target
// season too, at any date. Each fixture game adds a distinct power of two to
// the goals and saves, so a season's sums name exactly the games it counted,
// and some goalie lines miss a shutout by one predicate each.
// The legacy comparison runs the four queries the merged ones replaced on the
// same fixture. Skips unless PUCKDB_TEST_PG_URL names a test database (see
// CLAUDE.md "Database-backed tests"). Fixtures use seasons and IDs no other
// test touches, and results are filtered to them.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	windowBelowFloorSeason = 19941995
	windowFloorSeason      = 19951996
	windowMiddleSeason     = 19961997
	windowPriorSeason      = 19971998
	windowTargetSeason     = 19981999

	windowSkaterID   = 9_300_001
	windowGoalieID   = 9_300_002
	windowOpponentID = 9_300_999
	windowHomeTeamID = 1
	windowAwayTeamID = 2
	windowFirstGame  = 3_900_300_000
	// windowShutoutTOI is a full game, enough for a starter's shutout.
	windowShutoutTOI = DefaultGoalieShutoutMinTOI
)

// windowCutoff falls inside the prior season: one of its games is before
// it, one on it and one after it.
var windowCutoff = time.Date(1998, time.January, 15, 0, 0, 0, 0, time.UTC)

// windowGame is one fixture game; bit is its power of two in the sums.
type windowGame struct {
	bit    int
	season int
	kind   string
	state  string
	date   string
}

// windowGames covers each predicate: a season below the floor, a playoff and
// an unfinished game (never read), a prior-season game on the cutoff (read
// by a projection as of that day, not the day before) and one after it
// (read only by an evaluation), and target-season games, one dated long
// after any as-of (read only by an evaluation).
var windowGames = []windowGame{
	{0, windowBelowFloorSeason, "regular_season", "FINAL", "1994-11-01"},
	{1, windowFloorSeason, "regular_season", "FINAL", "1995-11-01"},
	{2, windowMiddleSeason, "regular_season", "FINAL", "1996-11-01"},
	{3, windowMiddleSeason, "regular_season", "OFF", "1997-03-01"},
	{4, windowMiddleSeason, "playoffs", "FINAL", "1997-04-20"},
	{5, windowMiddleSeason, "regular_season", "FUT", "1997-03-05"},
	{6, windowPriorSeason, "regular_season", "FINAL", "1997-11-01"},
	{7, windowPriorSeason, "regular_season", "FINAL", "1998-03-01"},
	{8, windowTargetSeason, "regular_season", "FINAL", "1998-11-01"},
	{9, windowTargetSeason, "regular_season", "FINAL", "2030-01-01"},
	{10, windowPriorSeason, "regular_season", "FINAL", "1998-01-15"},
}

// windowFaceoffBit is the game where the skater wins one faceoff: after the
// cutoff, so only an evaluation counts it.
const windowFaceoffBit = 7

// windowGoalie is a fixture game's goalie line.
type windowGoalie struct {
	starter      bool
	toiSeconds   int
	goalsAgainst int
}

func (line windowGoalie) shutout() bool {
	return line.starter && line.goalsAgainst == 0 && line.toiSeconds >= DefaultGoalieShutoutMinTOI
}

// windowGoalieVariants make each shutout predicate false once, in seasons
// every window reads: a start one second short of the threshold, a full
// game as the backup and a start with a goal against. Every other game is a
// full start without a goal against.
var windowGoalieVariants = map[int]windowGoalie{
	2: {starter: true, toiSeconds: windowShutoutTOI - 1},
	3: {starter: false, toiSeconds: windowShutoutTOI},
	6: {starter: true, toiSeconds: windowShutoutTOI, goalsAgainst: 1},
}

func windowGoalieLine(bit int) windowGoalie {
	if line, ok := windowGoalieVariants[bit]; ok {
		return line
	}
	return windowGoalie{starter: true, toiSeconds: windowShutoutTOI}
}

func windowGameID(bit int) int64 { return windowFirstGame + int64(bit) }

func seedSeasonWindow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	lastGame := windowGameID(len(windowGames) - 1)
	t.Cleanup(func() {
		execAll(t, pool,
			fmt.Sprintf(`DELETE FROM play_events WHERE game_id BETWEEN %d AND %d`, windowFirstGame, lastGame),
			fmt.Sprintf(`DELETE FROM games WHERE id BETWEEN %d AND %d`, windowFirstGame, lastGame),
			fmt.Sprintf(`DELETE FROM players WHERE id IN (%d, %d)`, windowSkaterID, windowGoalieID))
	})
	statements := []string{fmt.Sprintf(`INSERT INTO players (id, first_name, last_name, position, birth_date) VALUES
		(%d, 'Skater', 'Window', 'C', DATE '1975-02-01'), (%d, 'Goalie', 'Window', 'G', DATE '1974-06-01')`,
		windowSkaterID, windowGoalieID)}
	for _, game := range windowGames {
		statements = append(statements, seedWindowGame(game)...)
	}
	statements = append(statements, fmt.Sprintf(`INSERT INTO play_events
		(game_id, event_id, period, period_type, time_in_period, time_remaining, type_desc_key, sort_order, winning_player_id, losing_player_id)
		VALUES (%d, 1, 1, 'REG', '00:00', '20:00', 'faceoff', 1, %d, %d)`,
		windowGameID(windowFaceoffBit), windowSkaterID, windowOpponentID))
	execAll(t, pool, statements...)
}

func seedWindowGame(game windowGame) []string {
	id, value, goalie := windowGameID(game.bit), 1<<game.bit, windowGoalieLine(game.bit)
	return []string{
		fmt.Sprintf(`INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
			VALUES (%d, %d, '%s', DATE '%s', %d, %d, '%s')`,
			id, game.season, game.kind, game.date, windowHomeTeamID, windowAwayTeamID, game.state),
		fmt.Sprintf(`INSERT INTO game_skater_stats
			(game_id, player_id, team_id, is_home, sweater_number, position, goals, hits, toi_seconds)
			VALUES (%d, %d, %d, true, 9, 'C', %d, %d, 1000)`,
			id, windowSkaterID, windowHomeTeamID, value, value),
		fmt.Sprintf(`INSERT INTO game_goalie_stats
			(game_id, player_id, team_id, is_home, sweater_number, starter, toi_seconds, shots_against, saves, goals_against)
			VALUES (%d, %d, %d, true, 30, %t, %d, %d, %d, %d)`,
			id, windowGoalieID, windowHomeTeamID, goalie.starter, goalie.toiSeconds, value, value, goalie.goalsAgainst),
	}
}

// windowBits maps each season to the bits of the games a window reads.
type windowBits map[int][]int

// windowGoalieTotals is what a window reads of one goalie season.
type windowGoalieTotals struct {
	saves    int64
	shutouts int
}

// goals is each season's skater goals (and goalie saves): its bits' sum.
func (bits windowBits) goals() map[int]int64 {
	sums := make(map[int]int64, len(bits))
	for season, games := range bits {
		for _, bit := range games {
			sums[season] += 1 << bit
		}
	}
	return sums
}

func (bits windowBits) goalieTotals() map[int]windowGoalieTotals {
	totals := make(map[int]windowGoalieTotals, len(bits))
	for season, games := range bits {
		var total windowGoalieTotals
		for _, bit := range games {
			total.saves += 1 << bit
			if windowGoalieLine(bit).shutout() {
				total.shutouts++
			}
		}
		totals[season] = total
	}
	return totals
}

var (
	// windowProjectionBits is what a projection as of windowCutoff reads.
	windowProjectionBits = windowBits{
		windowFloorSeason: {1}, windowMiddleSeason: {2, 3}, windowPriorSeason: {6, 10},
	}
	// windowEvaluationBits is what an evaluation of the target season reads.
	windowEvaluationBits = windowBits{
		windowFloorSeason: {1}, windowMiddleSeason: {2, 3}, windowPriorSeason: {6, 7, 10},
		windowTargetSeason: {8, 9},
	}
)

func fixtureSkaterRows(rows []sqlcdb.ListProjectionSkaterSeasonsRow) []sqlcdb.ListProjectionSkaterSeasonsRow {
	var result []sqlcdb.ListProjectionSkaterSeasonsRow
	for _, row := range rows {
		if row.PlayerID == windowSkaterID {
			result = append(result, row)
		}
	}
	return result
}

func fixtureGoalieRows(rows []sqlcdb.ListProjectionGoalieSeasonsRow) []sqlcdb.ListProjectionGoalieSeasonsRow {
	var result []sqlcdb.ListProjectionGoalieSeasonsRow
	for _, row := range rows {
		if row.PlayerID == windowGoalieID {
			result = append(result, row)
		}
	}
	return result
}

func loadWindow(
	t *testing.T, queries *sqlcdb.Queries, window seasonWindow,
) ([]sqlcdb.ListProjectionSkaterSeasonsRow, []sqlcdb.ListProjectionGoalieSeasonsRow) {
	t.Helper()
	ctx := context.Background()
	skaters, err := queries.ListProjectionSkaterSeasons(ctx, window.skaterParams())
	require.NoError(t, err)
	goalies, err := queries.ListProjectionGoalieSeasons(ctx, window.goalieParams(DefaultGoalieShutoutMinTOI))
	require.NoError(t, err)
	return fixtureSkaterRows(skaters), fixtureGoalieRows(goalies)
}

func requireWindow(
	t *testing.T, want windowBits,
	skaters []sqlcdb.ListProjectionSkaterSeasonsRow, goalies []sqlcdb.ListProjectionGoalieSeasonsRow,
) {
	t.Helper()
	gotGoals := make(map[int]int64, len(skaters))
	for _, row := range skaters {
		gotGoals[int(row.Season)] = row.Goals
	}
	gotGoalies := make(map[int]windowGoalieTotals, len(goalies))
	for _, row := range goalies {
		gotGoalies[int(row.Season)] = windowGoalieTotals{saves: row.Saves, shutouts: int(row.Shutouts)}
	}
	require.Equal(t, want.goals(), gotGoals, "skater goals per season")
	require.Equal(t, want.goalieTotals(), gotGoalies, "goalie saves and shutouts per season")
}

func TestProjectionWindowReadsPriorSeasonsThroughCutoff(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	skaters, goalies := loadWindow(t, sqlcdb.New(pool),
		projectionWindow(windowTargetSeason, windowFloorSeason, dateValue(windowCutoff)))
	requireWindow(t, windowProjectionBits, skaters, goalies)
	for _, row := range skaters {
		require.Zero(t, row.FaceoffsWon, "the faceoff after the cutoff must not count, season %d", row.Season)
	}
}

// A cutoff after every fixture game still leaves the target season out:
// the projection bound is the season, not only the date.
func TestProjectionWindowExcludesTargetSeasonAtAnyCutoff(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	late := time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC)
	skaters, goalies := loadWindow(t, sqlcdb.New(pool),
		projectionWindow(windowTargetSeason, windowFloorSeason, dateValue(late)))
	requireWindow(t, windowBits{
		windowFloorSeason: {1}, windowMiddleSeason: {2, 3}, windowPriorSeason: {6, 7, 10},
	}, skaters, goalies)
}

// The cutoff is inclusive: a game on the as-of day counts, and the day
// before leaves it out.
func TestProjectionWindowCutoffDayIsInclusive(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	skaters, goalies := loadWindow(t, sqlcdb.New(pool),
		projectionWindow(windowTargetSeason, windowFloorSeason, dateValue(windowCutoff.AddDate(0, 0, -1))))
	requireWindow(t, windowBits{
		windowFloorSeason: {1}, windowMiddleSeason: {2, 3}, windowPriorSeason: {6},
	}, skaters, goalies)
}

// A projection without an as-of cutoff reads nothing, as the legacy
// `game_date <= NULL` did; it never falls back to the evaluation window.
func TestProjectionWindowWithoutCutoffReadsNothing(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	skaters, goalies := loadWindow(t, sqlcdb.New(pool),
		projectionWindow(windowTargetSeason, windowFloorSeason, pgtype.Date{}))
	require.Empty(t, skaters)
	require.Empty(t, goalies)
}

func TestEvaluationWindowReadsTargetSeasonAtAnyDate(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	skaters, goalies := loadWindow(t, sqlcdb.New(pool), evaluationWindow(windowTargetSeason, windowFloorSeason))
	requireWindow(t, windowEvaluationBits, skaters, goalies)
	faceoffs := make(map[int]int64, len(skaters))
	for _, row := range skaters {
		faceoffs[int(row.Season)] = row.FaceoffsWon
	}
	require.EqualValues(t, 1, faceoffs[windowPriorSeason], "the evaluation counts the faceoff after the cutoff")
}

// LoadEvaluationInput reads the evaluation window, target season included,
// from the default model's lookback floor.
func TestLoadEvaluationInputReadsEvaluationWindow(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	t.Cleanup(func() {
		execAll(t, pool, fmt.Sprintf(`DELETE FROM seasons WHERE id = %d`, windowTargetSeason))
	})
	execAll(t, pool, fmt.Sprintf(`INSERT INTO seasons (id, standings_start, standings_end)
		VALUES (%d, DATE '1998-10-09', DATE '1999-04-18')`, windowTargetSeason))
	cfg := DefaultConfig()
	require.Equal(t, windowFloorSeason, projectionLoadFloor(cfg, windowTargetSeason), "fixture floor")

	input, err := NewPGStore(pool).LoadEvaluationInput(context.Background(), cfg, windowTargetSeason,
		time.Date(1998, time.October, 1, 0, 0, 0, 0, time.UTC), time.Date(1999, time.June, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	gotGoals := make(map[int]int64)
	for _, season := range input.Skaters {
		if season.PlayerID == windowSkaterID {
			gotGoals[season.Season] = int64(season.Goals)
		}
	}
	gotGoalies := make(map[int]windowGoalieTotals)
	for _, season := range input.Goalies {
		if season.PlayerID == windowGoalieID {
			gotGoalies[season.Season] = windowGoalieTotals{saves: int64(season.Saves), shutouts: season.Shutouts}
		}
	}
	require.Equal(t, windowEvaluationBits.goals(), gotGoals, "skater goals per season")
	require.Equal(t, windowEvaluationBits.goalieTotals(), gotGoalies, "goalie saves and shutouts per season")
}

// legacyWindowCase is one window and the arguments the legacy query for it
// took, before the goalie queries' trailing minimum shutout TOI.
type legacyWindowCase struct {
	name      string
	window    seasonWindow
	skaterSQL string
	goalieSQL string
	args      []any
}

func legacyWindowCases() []legacyWindowCase {
	projectionCase := func(name string, cutoff pgtype.Date) legacyWindowCase {
		return legacyWindowCase{
			name: name, window: projectionWindow(windowTargetSeason, windowFloorSeason, cutoff),
			skaterSQL: legacySkaterHistorySQL, goalieSQL: legacyGoalieHistorySQL,
			args: []any{int32(windowTargetSeason), int32(windowFloorSeason), cutoff},
		}
	}
	return []legacyWindowCase{
		projectionCase("projection mid-season cutoff", dateValue(windowCutoff)),
		projectionCase("projection day before cutoff", dateValue(windowCutoff.AddDate(0, 0, -1))),
		projectionCase("projection late cutoff", dateValue(time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC))),
		projectionCase("projection without cutoff", pgtype.Date{}),
		{
			name: "evaluation", window: evaluationWindow(windowTargetSeason, windowFloorSeason),
			skaterSQL: legacySkaterEvaluationSQL, goalieSQL: legacyGoalieEvaluationSQL,
			args: []any{int32(windowTargetSeason), int32(windowFloorSeason)},
		},
	}
}

func TestSeasonQueriesMatchLegacyQueries(t *testing.T) {
	pool := pgtest.Open(t)
	seedSeasonWindow(t, pool)
	ctx := context.Background()
	queries := sqlcdb.New(pool)
	for _, tc := range legacyWindowCases() {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			skaters, goalies := loadWindow(t, queries, tc.window)

			rows, err := pool.Query(ctx, tc.skaterSQL, tc.args...)
			require.NoError(t, err)
			legacySkaters, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlcdb.ListProjectionSkaterSeasonsRow])
			require.NoError(t, err)
			require.Equal(t, fixtureSkaterRows(legacySkaters), skaters)

			goalieArgs := append(append([]any{}, tc.args...), int32(DefaultGoalieShutoutMinTOI))
			rows, err = pool.Query(ctx, tc.goalieSQL, goalieArgs...)
			require.NoError(t, err)
			legacyGoalies, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlcdb.ListProjectionGoalieSeasonsRow])
			require.NoError(t, err)
			require.Equal(t, fixtureGoalieRows(legacyGoalies), goalies)
		})
	}
}

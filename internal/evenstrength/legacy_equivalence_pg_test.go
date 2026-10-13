package evenstrength

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

// Edge-case game: clubs, players and shifts in seconds of each period.
const (
	edgeHomeTeam = 31
	edgeAwayTeam = 32
	edgeSeason   = 20252026
	edgeDate     = "2025-10-01"

	edgeH1, edgeH2, edgeH3, edgeH4, edgeH5, edgeH6, edgeH7 = 9_970_001, 9_970_002, 9_970_003, 9_970_004, 9_970_005, 9_970_006, 9_970_007
	edgeA1, edgeA2, edgeA3, edgeA4, edgeA5, edgeA6         = 9_970_011, 9_970_012, 9_970_013, 9_970_014, 9_970_015, 9_970_016
	edgeHomeGoalie, edgeAwayGoalie                         = 9_970_020, 9_970_021
	edgeNoStats                                            = 9_970_030 // shifts, no box-score row

	shiftTypeRegular  = 517
	shiftTypeGoalShot = 505

	evenFiveCode    = 1551
	evenFourCode    = 1441
	evenThreeCode   = 1331
	powerPlayCode   = 1541
	extraSkaterCode = 651 // home net empty, 6v5
	shootoutCode    = 1010
)

func TestRebuildMatchesLegacySQL(t *testing.T) {
	pool := openTestDB(t)
	games := append(loadNHLGames(t), edgeCaseGame())
	for _, g := range games {
		t.Run(g.name, func(t *testing.T) {
			seedGame(t, pool, g)
			inserted := rebuildInTx(t, pool, g.testID)

			wantPairs, wantSkaters := legacyTotals(t, pool, g.testID)
			gotPairs := listPairTOI(t, pool, g.testID)
			gotSkaters := listSkaterGames(t, pool, g.testID)
			require.NotEmpty(t, wantPairs)
			require.NotEmpty(t, wantSkaters)
			require.ElementsMatch(t, wantPairs, gotPairs)
			require.ElementsMatch(t, wantSkaters, gotSkaters)
			require.Equal(t, int64(len(wantPairs)+len(wantSkaters)), inserted)
			require.Positive(t, totalPoints(gotSkaters), "the fixture credits even-strength points")

			require.Equal(t, inserted, rebuildInTx(t, pool, g.testID), "rebuild is idempotent")
			require.ElementsMatch(t, wantPairs, listPairTOI(t, pool, g.testID), "rebuild is idempotent")
			require.ElementsMatch(t, wantSkaters, listSkaterGames(t, pool, g.testID), "rebuild is idempotent")
		})
	}
}

func rebuildInTx(t *testing.T, pool *pgxpool.Pool, gameID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var inserted int64
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		inserted, err = Rebuild(ctx, sqlcdb.New(tx), gameID)
		return err
	})
	require.NoError(t, err)
	return inserted
}

// legacyTotals runs the retired SQL derivation. Its result does not depend
// on the plan, so nested loops are turned off for this read-only
// transaction to keep the test fast.
func legacyTotals(t *testing.T, pool *pgxpool.Pool, gameID int64) ([]sqlcdb.EvenStrengthPairToi, []sqlcdb.EvenStrengthSkaterGame) {
	t.Helper()
	ctx := context.Background()
	var pairs []sqlcdb.EvenStrengthPairToi
	var skaters []sqlcdb.EvenStrengthSkaterGame
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL enable_nestloop = off"); err != nil {
			return err
		}
		var err error
		if pairs, err = collect[sqlcdb.EvenStrengthPairToi](ctx, tx, legacyPairTOISQL, gameID); err != nil {
			return err
		}
		skaters, err = collect[sqlcdb.EvenStrengthSkaterGame](ctx, tx, legacySkaterGamesSQL, gameID)
		return err
	})
	require.NoError(t, err)
	return pairs, skaters
}

func listPairTOI(t *testing.T, pool *pgxpool.Pool, gameID int64) []sqlcdb.EvenStrengthPairToi {
	t.Helper()
	rows, err := collect[sqlcdb.EvenStrengthPairToi](context.Background(), pool, `
SELECT game_id, player_id, teammate_id, shared_toi_seconds
FROM even_strength_pair_toi WHERE game_id = $1`, gameID)
	require.NoError(t, err)
	return rows
}

func listSkaterGames(t *testing.T, pool *pgxpool.Pool, gameID int64) []sqlcdb.EvenStrengthSkaterGame {
	t.Helper()
	rows, err := collect[sqlcdb.EvenStrengthSkaterGame](context.Background(), pool, `
SELECT game_id, player_id, team_id, toi_seconds, points
FROM even_strength_skater_games WHERE game_id = $1`, gameID)
	require.NoError(t, err)
	return rows
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func collect[T any](ctx context.Context, q querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

func totalPoints(rows []sqlcdb.EvenStrengthSkaterGame) int32 {
	var sum int32
	for _, r := range rows {
		sum += r.Points
	}
	return sum
}

// edgeCaseGame is a synthetic game for the inputs the real games rarely
// have. Period 1, in seconds: [0,60) 5v5, [60,90) 5v5 after a home change at
// 60, [90,100) 4v4, [100,120) 4v3, [120,150) 3v3, [150,160) 3v3 with the away
// net empty, [160,180) 3v3. Period 2 is 3v3 throughout; period 3 is 6v5
// with the home net empty. Rows that must be ignored: a duplicate shift,
// unparsable, zero-length and reversed clocks, a non-517 shift and a player
// without a box-score row. Goals sit on segment boundaries, carry unknown or
// non-even situation codes, credit one player twice, or omit the scorer.
func edgeCaseGame() fixtureGame {
	return fixtureGame{
		name: "edge-cases", Season: edgeSeason, Date: edgeDate, HomeTeam: edgeHomeTeam, AwayTeam: edgeAwayTeam,
		Skaters: [][2]int64{
			{edgeH1, edgeHomeTeam}, {edgeH2, edgeHomeTeam}, {edgeH3, edgeHomeTeam}, {edgeH4, edgeHomeTeam},
			{edgeH5, edgeHomeTeam}, {edgeH6, edgeHomeTeam}, {edgeH7, edgeHomeTeam},
			{edgeA1, edgeAwayTeam}, {edgeA2, edgeAwayTeam}, {edgeA3, edgeAwayTeam},
			{edgeA4, edgeAwayTeam}, {edgeA5, edgeAwayTeam}, {edgeA6, edgeAwayTeam},
		},
		Goalies: [][2]int64{{edgeHomeGoalie, edgeHomeTeam}, {edgeAwayGoalie, edgeAwayTeam}},
		Shifts:  append(append(edgePeriodOneShifts(), edgePeriodTwoShifts()...), edgePeriodThreeShifts()...),
		Goals:   edgeGoals(),
		testID:  testGameBase,
	}
}

func regular(player, team int64, period int32, start, end string) fixtureShift {
	return fixtureShift{Player: player, Team: team, Period: period, Start: start, End: end, TypeCode: shiftTypeRegular}
}

func edgePeriodOneShifts() []fixtureShift {
	const home, away = edgeHomeTeam, edgeAwayTeam
	return []fixtureShift{
		regular(edgeH1, home, 1, "00:00", "03:00"),
		regular(edgeH1, home, 1, "00:00", "03:00"), // duplicate
		regular(edgeH2, home, 1, "00:00", "03:00"),
		regular(edgeH2, home, 1, "bad", "01:00"),
		regular(edgeH2, home, 1, "", "01:00"),
		regular(edgeH2, home, 1, "1:5", "02:00"),
		regular(edgeH3, home, 1, "00:00", "03:00"),
		regular(edgeH3, home, 1, "01:00", "01:00"), // zero length
		regular(edgeH4, home, 1, "00:00", "01:30"),
		regular(edgeH4, home, 1, "02:00", "01:00"), // reversed
		regular(edgeH5, home, 1, "00:00", "01:00"),
		regular(edgeH6, home, 1, "01:00", "02:00"),
		{Player: edgeH7, Team: home, Period: 1, Start: "00:30", End: "00:45", TypeCode: shiftTypeGoalShot},
		regular(edgeNoStats, home, 1, "00:00", "01:40"),
		regular(edgeHomeGoalie, home, 1, "00:00", "03:00"),
		regular(edgeA1, away, 1, "00:00", "03:00"),
		regular(edgeA2, away, 1, "00:00", "03:00"),
		regular(edgeA3, away, 1, "00:00", "03:00"),
		regular(edgeA4, away, 1, "00:00", "01:30"),
		regular(edgeA5, away, 1, "00:00", "01:00"),
		regular(edgeA6, away, 1, "01:00", "01:40"),
		regular(edgeAwayGoalie, away, 1, "00:00", "02:30"),
		regular(edgeAwayGoalie, away, 1, "02:40", "03:00"),
	}
}

func edgePeriodTwoShifts() []fixtureShift {
	var shifts []fixtureShift
	for _, p := range []int64{edgeH1, edgeH2, edgeH3, edgeHomeGoalie} {
		shifts = append(shifts, regular(p, edgeHomeTeam, 2, "00:00", "05:00"))
	}
	for _, p := range []int64{edgeA1, edgeA2, edgeA3, edgeAwayGoalie} {
		shifts = append(shifts, regular(p, edgeAwayTeam, 2, "00:00", "05:00"))
	}
	return shifts
}

func edgePeriodThreeShifts() []fixtureShift {
	var shifts []fixtureShift
	for _, p := range []int64{edgeH1, edgeH2, edgeH3, edgeH4, edgeH5, edgeH6} {
		shifts = append(shifts, regular(p, edgeHomeTeam, 3, "00:00", "01:00"))
	}
	for _, p := range []int64{edgeA1, edgeA2, edgeA3, edgeA4, edgeA5, edgeAwayGoalie} {
		shifts = append(shifts, regular(p, edgeAwayTeam, 3, "00:00", "01:00"))
	}
	return shifts
}

func edgeGoals() []fixtureGoal {
	situation := func(code int32) *int32 { return &code }
	player := func(id int64) *int64 { return &id }
	return []fixtureGoal{
		{1, "00:30", situation(evenFiveCode), player(edgeH1), player(edgeH2), player(edgeH3)},
		{1, "01:00", situation(evenFiveCode), player(edgeH5), nil, nil}, // ends h5's segment
		{1, "01:00", situation(evenFiveCode), player(edgeH6), nil, nil}, // starts h6's segment
		{1, "01:35", situation(evenFourCode), player(edgeA1), player(edgeA6), nil},
		{1, "01:50", situation(powerPlayCode), player(edgeH1), nil, nil},           // power play
		{1, "02:35", situation(evenFiveCode), player(edgeH2), nil, nil},            // coded even, net empty in shifts
		{1, "00:40", nil, player(edgeH1), nil, nil},                                // unknown situation
		{1, "xx:yy", situation(evenFiveCode), player(edgeH1), nil, nil},            // unparsable clock
		{1, "00:20", situation(evenFiveCode), player(edgeH2), player(edgeH2), nil}, // credited twice
		{1, "00:10", situation(evenFiveCode), nil, player(edgeH3), nil},            // no scorer
		{1, "00:50", situation(evenFiveCode), player(edgeNoStats), player(edgeH4), nil},
		{2, "02:00", situation(evenThreeCode), player(edgeH1), nil, nil},
		{2, "05:00", situation(evenThreeCode), player(edgeA1), player(edgeA2), nil}, // end of period
		{2, "00:00", situation(shootoutCode), player(edgeH2), nil, nil},
		{3, "00:30", situation(extraSkaterCode), player(edgeH1), player(edgeH2), nil},
	}
}

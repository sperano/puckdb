package projection

// A multi-game shift fixture for comparing the precomputed
// even_strength_segments against the legacy query-time segmentation. Every
// game has shifts, so both queries see the same games; only the regular
// season, final, pre-cutoff ones may contribute.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const (
	equivSeasonA    = 20232024
	equivSeasonB    = 20242025
	equivHomeTeam   = 310
	equivAwayTeam   = 320
	equivPlayerBase = 9_950_000
	equivShiftBase  = 3_950_000_000
	equivCutoffDate = "2025-03-01"

	equivGameA         = 3_950_000_001 // season A, FINAL: every strength case
	equivGameB         = 3_950_000_002 // season B, OFF: h1 plays for the away club
	equivGamePreseason = 3_950_000_003
	equivGameLive      = 3_950_000_004
	equivGameLate      = 3_950_000_005 // after the cutoff date

	// Player offsets from equivPlayerBase.
	h1, h2, h3, h4, h5, h6 = 1, 2, 3, 4, 5, 6
	a1, a2, a3, a4, a5, a6 = 11, 12, 13, 14, 15, 16
	homeGoalie             = 20
	awayGoalie             = 21
	noStatsPlayer          = 30 // has shifts but no box-score row
	noAssist               = 0

	evenStrengthCode  = 1551
	fourOnFourCode    = 1441
	powerPlayCode     = 1541
	emptyNetCode      = 551
	shiftTypeRegular  = "517"
	shiftTypeGoalShot = "505"
)

var equivGameIDs = []int64{equivGameA, equivGameB, equivGamePreseason, equivGameLive, equivGameLate}

type fixtureShift struct {
	player, team int64
	period       int
	start, end   string
	typeCode     string
}

type fixtureGoal struct {
	period                   int
	clock                    string
	situation                int
	scorer, assist1, assist2 int64
}

type fixtureGame struct {
	id              int64
	season          int32
	gameType, state string
	date            string
	skaters         map[int64]int64 // player offset -> team
	goalies         map[int64]int64
	shifts          []fixtureShift
	goals           []fixtureGoal
}

func shift(player, team int64, period int, start, end string) fixtureShift {
	return fixtureShift{player: player, team: team, period: period, start: start, end: end, typeCode: shiftTypeRegular}
}

func standardRosters() (skaters, goalies map[int64]int64) {
	skaters = map[int64]int64{}
	for _, p := range []int64{h1, h2, h3, h4, h5, h6} {
		skaters[p] = equivHomeTeam
	}
	for _, p := range []int64{a1, a2, a3, a4, a5, a6} {
		skaters[p] = equivAwayTeam
	}
	return skaters, map[int64]int64{homeGoalie: equivHomeTeam, awayGoalie: equivAwayTeam}
}

// fullStrengthPeriod is one 5v5 period with both goalies, for games whose
// only role is to be excluded by the eligibility filter.
func fullStrengthPeriod(skaters map[int64]int64) []fixtureShift {
	var shifts []fixtureShift
	for _, p := range []int64{h1, h2, h3, h4, h5, a1, a2, a3, a4, a5} {
		shifts = append(shifts, shift(p, skaters[p], 1, "00:00", "02:00"))
	}
	return append(shifts,
		shift(homeGoalie, equivHomeTeam, 1, "00:00", "02:00"),
		shift(awayGoalie, equivAwayTeam, 1, "00:00", "02:00"))
}

func equivFixtureGames() []fixtureGame {
	skaters, goalies := standardRosters()
	tradedSkaters, _ := standardRosters()
	tradedSkaters[h1] = equivAwayTeam
	delete(tradedSkaters, a1)
	excluded := func(id int64, gameType, state, date string) fixtureGame {
		return fixtureGame{
			id: id, season: equivSeasonA, gameType: gameType, state: state, date: date,
			skaters: skaters, goalies: goalies, shifts: fullStrengthPeriod(skaters),
			goals: []fixtureGoal{{1, "01:00", evenStrengthCode, h1, h2, noAssist}},
		}
	}
	return []fixtureGame{
		{
			id: equivGameA, season: equivSeasonA, gameType: "regular_season", state: "FINAL", date: "2024-01-10",
			skaters: skaters, goalies: goalies,
			shifts: append(gameAPeriodOneShifts(), gameAPeriodTwoShifts()...),
			goals:  gameAGoals(),
		},
		{
			id: equivGameB, season: equivSeasonB, gameType: "regular_season", state: "OFF", date: "2025-01-15",
			skaters: tradedSkaters, goalies: goalies, shifts: gameBShifts(tradedSkaters),
			goals: []fixtureGoal{{1, "01:00", evenStrengthCode, h1, a2, noAssist}},
		},
		excluded(equivGamePreseason, "preseason", "FINAL", "2023-10-01"),
		excluded(equivGameLive, "regular_season", "LIVE", "2024-02-01"),
		excluded(equivGameLate, "regular_season", "FINAL", "2025-04-01"),
	}
}

// gameAPeriodOneShifts covers, in seconds: [0,60) 5v5, [60,90) 5v4,
// [90,150) 4v4, [150,180) 3v3, [180,200) 5v5 with the away net empty,
// [200,240) 5v5, and [240,260) six home skaters with the home net empty.
// Rows that must be ignored: a duplicate, an unparsable clock, a
// zero-length shift, a non-517 shift, and a player without box-score rows.
func gameAPeriodOneShifts() []fixtureShift {
	return []fixtureShift{
		shift(h1, equivHomeTeam, 1, "00:00", "04:20"),
		shift(h1, equivHomeTeam, 1, "00:00", "04:20"),
		shift(h2, equivHomeTeam, 1, "00:00", "04:20"),
		shift(h2, equivHomeTeam, 1, "bad", "01:00"),
		shift(h3, equivHomeTeam, 1, "00:00", "04:20"),
		shift(h4, equivHomeTeam, 1, "00:00", "02:30"),
		shift(h4, equivHomeTeam, 1, "03:00", "04:20"),
		shift(h5, equivHomeTeam, 1, "00:00", "01:30"),
		shift(h5, equivHomeTeam, 1, "03:00", "04:20"),
		shift(h6, equivHomeTeam, 1, "04:00", "04:20"),
		shift(h6, equivHomeTeam, 1, "01:00", "01:00"),
		{player: h6, team: equivHomeTeam, period: 1, start: "00:30", end: "00:45", typeCode: shiftTypeGoalShot},
		shift(noStatsPlayer, equivHomeTeam, 1, "00:00", "01:40"),
		shift(homeGoalie, equivHomeTeam, 1, "00:00", "04:00"),
		shift(a1, equivAwayTeam, 1, "00:00", "04:20"),
		shift(a2, equivAwayTeam, 1, "00:00", "04:20"),
		shift(a3, equivAwayTeam, 1, "00:00", "04:20"),
		shift(a4, equivAwayTeam, 1, "00:00", "02:30"),
		shift(a4, equivAwayTeam, 1, "03:00", "04:20"),
		shift(a5, equivAwayTeam, 1, "00:00", "01:00"),
		shift(a5, equivAwayTeam, 1, "03:00", "04:20"),
		shift(awayGoalie, equivAwayTeam, 1, "00:00", "03:00"),
		shift(awayGoalie, equivAwayTeam, 1, "03:20", "04:20"),
	}
}

// gameAPeriodTwoShifts is 5v5 throughout with staggered line changes:
// the home club changes at 45 seconds and the away club at 50.
func gameAPeriodTwoShifts() []fixtureShift {
	shifts := []fixtureShift{
		shift(h5, equivHomeTeam, 2, "00:00", "00:45"),
		shift(h6, equivHomeTeam, 2, "00:45", "01:40"),
		shift(a5, equivAwayTeam, 2, "00:00", "00:50"),
		shift(a6, equivAwayTeam, 2, "00:50", "01:40"),
		shift(homeGoalie, equivHomeTeam, 2, "00:00", "01:40"),
		shift(awayGoalie, equivAwayTeam, 2, "00:00", "01:40"),
	}
	for _, p := range []int64{h1, h2, h3, h4} {
		shifts = append(shifts, shift(p, equivHomeTeam, 2, "00:00", "01:40"))
	}
	for _, p := range []int64{a1, a2, a3, a4} {
		shifts = append(shifts, shift(p, equivAwayTeam, 2, "00:00", "01:40"))
	}
	return shifts
}

// gameAGoals includes goals exactly on segment boundaries: a goal counts
// for the segment it ends, never for the one it starts.
func gameAGoals() []fixtureGoal {
	return []fixtureGoal{
		{1, "00:30", evenStrengthCode, h1, h2, h3},
		{1, "01:00", evenStrengthCode, h2, noAssist, noAssist}, // ends the 5v5 segment
		{1, "01:15", powerPlayCode, h3, h1, noAssist},          // power play
		{1, "01:30", fourOnFourCode, h1, noAssist, noAssist},   // ends 5v4, starts 4v4
		{1, "02:00", fourOnFourCode, a1, a2, noAssist},         // 4v4
		{1, "03:10", emptyNetCode, h2, noAssist, noAssist},     // empty net
		{1, "03:15", evenStrengthCode, h3, noAssist, noAssist}, // coded 5v5, net empty in shifts
		{1, "xx:yy", evenStrengthCode, h1, noAssist, noAssist}, // unparsable clock
		{1, "03:30", evenStrengthCode, h4, noAssist, h5},       // only a second assist
		{2, "00:45", evenStrengthCode, h5, h1, noAssist},       // ends h5's shift
		{2, "00:50", evenStrengthCode, a5, a6, noAssist},       // a6's shift starts here
	}
}

func gameBShifts(skaters map[int64]int64) []fixtureShift {
	var shifts []fixtureShift
	for _, p := range []int64{h1, h2, h3, h4, h5, h6, a2, a3, a4, a5} {
		shifts = append(shifts, shift(p, skaters[p], 1, "00:00", "02:00"))
	}
	return append(shifts,
		shift(homeGoalie, equivHomeTeam, 1, "00:00", "02:00"),
		shift(awayGoalie, equivAwayTeam, 1, "00:00", "02:00"))
}

func seedEquivFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	cleanupEquivFixture(t, pool)
	t.Cleanup(func() { cleanupEquivFixture(t, pool) })
	execFixture(t, pool, `
INSERT INTO players (id, first_name, last_name)
SELECT $1::bigint + id, 'Equiv', id::text FROM generate_series(1, $2::int) AS id`,
		equivPlayerBase, noStatsPlayer)
	shiftID := int64(equivShiftBase)
	for _, game := range equivFixtureGames() {
		seedFixtureGame(t, pool, game)
		for _, s := range game.shifts {
			shiftID++
			seedFixtureShift(t, pool, game.id, shiftID, s)
		}
		for i, goal := range game.goals {
			seedFixtureGoal(t, pool, game.id, int64(i+1), goal)
		}
		rebuildEvenStrengthSegments(t, pool, game.id)
	}
}

func seedFixtureGame(t *testing.T, pool *pgxpool.Pool, game fixtureGame) {
	t.Helper()
	execFixture(t, pool, `
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, $2, $3, $4::date, $5, $6, $7)`,
		game.id, game.season, game.gameType, game.date, game.state, equivHomeTeam, equivAwayTeam)
	for player, team := range game.skaters {
		execFixture(t, pool, `
INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, toi_seconds)
VALUES ($1, $2, $3, $4, $5, 'C', 60)`,
			game.id, equivPlayerBase+player, team, team == equivHomeTeam, player)
	}
	for player, team := range game.goalies {
		execFixture(t, pool, `
INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, starter, toi_seconds)
VALUES ($1, $2, $3, $4, $5, TRUE, 60)`,
			game.id, equivPlayerBase+player, team, team == equivHomeTeam, player)
	}
}

func seedFixtureShift(t *testing.T, pool *pgxpool.Pool, gameID, shiftID int64, s fixtureShift) {
	t.Helper()
	execFixture(t, pool, `
INSERT INTO shifts (
    id, game_id, player_id, team_id, period, start_time, end_time, duration,
    shift_number, type_code, detail_code, event_number
) VALUES ($1, $2, $3, $4, $5, $6, $7, '00:00', 1, $8, '0', 0)`,
		shiftID, gameID, equivPlayerBase+s.player, s.team, s.period, s.start, s.end, s.typeCode)
}

func seedFixtureGoal(t *testing.T, pool *pgxpool.Pool, gameID, eventID int64, goal fixtureGoal) {
	t.Helper()
	execFixture(t, pool, `
INSERT INTO play_events (
    game_id, event_id, period, period_type, time_in_period, time_remaining,
    situation_code, type_desc_key, sort_order,
    scoring_player_id, assist1_player_id, assist2_player_id
) VALUES ($1, $2, $3, 'REG', $4, '00:00', $5, 'goal', $6, $7, $8, $9)`,
		gameID, eventID, goal.period, goal.clock, goal.situation, eventID,
		fixturePlayerID(goal.scorer), fixturePlayerID(goal.assist1), fixturePlayerID(goal.assist2))
}

// fixturePlayerID maps a player offset to its ID, and noAssist to NULL.
func fixturePlayerID(offset int64) *int64 {
	if offset == noAssist {
		return nil
	}
	id := equivPlayerBase + offset
	return &id
}

func cleanupEquivFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM shifts WHERE game_id = ANY($1)`,
		`DELETE FROM play_events WHERE game_id = ANY($1)`,
		`DELETE FROM games WHERE id = ANY($1)`,
	} {
		_, err := pool.Exec(ctx, stmt, equivGameIDs)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `DELETE FROM players WHERE id BETWEEN $1::bigint + 1 AND $1::bigint + $2::bigint`,
		equivPlayerBase, noStatsPlayer)
	require.NoError(t, err)
}

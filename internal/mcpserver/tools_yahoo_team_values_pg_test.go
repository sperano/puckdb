package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PostgreSQL fixture of the team values queries: league pgValuesLeague
// with two teams, a skater and a goalie on team 1 (the goalie then moves
// to team 2), a skater on team 2, a Yahoo-only player on team 1, two final
// games and one future game. Dates are in 3007 so no other fixture's games
// fall in the window.
const (
	pgValuesLeague             int32 = 990781
	pgValuesLeagueKey                = "465.l.990781"
	pgValuesTeam               int32 = 1
	pgValuesOtherTeam          int32 = 2
	pgValuesSkater             int32 = 990781001
	pgValuesGoalie             int32 = 990781002
	pgValuesOtherSkater        int32 = 990781003
	pgValuesYahooOnly          int32 = 990781009
	pgValuesReliefGoalie       int32 = 990781004
	pgValuesBackupGoalie       int32 = 990781005
	pgValuesMinimumGoalie      int32 = 990781006
	pgValuesGamePlayoff        int64 = 990781104
	pgValuesPlayoffGoals             = 7
	pgValuesGameA              int64 = 990781101
	pgValuesGameB              int64 = 990781102
	pgValuesGameFuture         int64 = 990781103
	pgValuesNHLTeam            int64 = 990781
	pgValuesNHLOpponent        int64 = 990782
	pgValuesFrom                     = "3007-10-01"
	pgValuesTo                       = "3007-10-04"
	pgValuesSkaterBench              = "3007-10-03"
	pgValuesGoalieLeaves             = "3007-10-03"
	pgValuesProjectedGP              = 82
	pgValuesProjectedG               = 30
	pgValuesProjectionKey            = "nhl.p.990781001"
	pgValuesProjectionKey2           = "nhl.p.990781001.b"
	pgValuesProjectionGoal2          = 99
	pgValuesProjectionStatsCnt       = 2
)

func cleanupTeamValuesFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM play_events WHERE game_id IN ($1, $2, $3, $4)`,
			[]any{pgValuesGameA, pgValuesGameB, pgValuesGameFuture, pgValuesGamePlayoff}},
		{`DELETE FROM games WHERE id IN ($1, $2, $3, $4)`, []any{pgValuesGameA, pgValuesGameB, pgValuesGameFuture, pgValuesGamePlayoff}},
		{`DELETE FROM yahoo_leagues WHERE id = $1`, []any{pgValuesLeague}},
		{`DELETE FROM yahoo_league_players WHERE league_key = $1`, []any{pgValuesLeagueKey}},
		{`DELETE FROM projection_snapshots WHERE target_season = 30073008`, nil},
		{`DELETE FROM players WHERE id IN ($1, $2, $3, $4, $5, $6)`,
			[]any{pgValuesSkater, pgValuesGoalie, pgValuesOtherSkater, pgValuesReliefGoalie, pgValuesBackupGoalie,
				pgValuesMinimumGoalie}},
	} {
		_, err := pool.Exec(ctx, stmt.sql, stmt.args...)
		require.NoError(t, err)
	}
}

func seedTeamValuesFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(context.Background(), sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'Values test', 3007)`, pgValuesLeague, pgValuesLeagueKey)
	for _, team := range []int32{pgValuesTeam, pgValuesOtherTeam} {
		exec(`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, 'T')`,
			pgValuesLeague, team, fmt.Sprintf("%s.t.%d", pgValuesLeagueKey, team))
	}
	exec(`INSERT INTO players (id, yahoo_id, first_name, last_name, position) VALUES
		($1, $1, 'Sam', 'Skater', 'C'), ($2, $2, 'Gil', 'Goalie', 'G'), ($3, $3, 'Oto', 'Other', 'D')`,
		pgValuesSkater, pgValuesGoalie, pgValuesOtherSkater)
	exec(`INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id, player_key, full_name,
		eligible_positions, fetched_at) VALUES ($1, 3007, $2, 465, $3, '465.p.990781009', 'Yahoo Only', '{LW}', now())`,
		pgValuesLeagueKey, pgValuesLeague, pgValuesYahooOnly)
	for _, r := range []struct {
		team, player int32
		date, slot   string
		positionType *string
	}{
		{pgValuesTeam, pgValuesSkater, "3007-10-02", "C", new(skaterPositionType)},
		{pgValuesTeam, pgValuesSkater, pgValuesSkaterBench, "BN", new(skaterPositionType)},
		{pgValuesTeam, pgValuesSkater, pgValuesTo, "C", new(skaterPositionType)},
		{pgValuesTeam, pgValuesGoalie, "3007-10-02", "G", nil},
		{pgValuesTeam, pgValuesGoalie, pgValuesGoalieLeaves, "IR", nil},
		{pgValuesOtherTeam, pgValuesGoalie, pgValuesTo, "G", nil},
		{pgValuesTeam, pgValuesYahooOnly, pgValuesTo, "BN", new(skaterPositionType)},
		{pgValuesOtherTeam, pgValuesOtherSkater, "3007-10-02", "D", new(skaterPositionType)},
		// Outside the window.
		{pgValuesOtherTeam, pgValuesOtherSkater, "3007-09-30", "D", new(skaterPositionType)},
	} {
		exec(`INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, player_key, selected_position, position_type)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			pgValuesLeague, r.team, r.date, r.player, fmt.Sprintf("465.p.%d", r.player), r.slot, r.positionType)
	}
	for _, g := range []struct {
		id          int64
		date, state string
	}{{pgValuesGameA, "3007-10-02", "FINAL"}, {pgValuesGameB, "3007-10-03", "OFF"}, {pgValuesGameFuture, pgValuesTo, "FUT"}} {
		exec(`INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
			VALUES ($1, 30073008, 'regular_season', $2, $3, $4, $5)`, g.id, g.date, g.state, pgValuesNHLTeam, pgValuesNHLOpponent)
	}
	exec(`INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, goals, assists, points,
		plus_minus, penalty_minutes, power_play_points, shots_on_goal, hits, blocked_shots) VALUES
		($1, $4, $5, TRUE, 9, 'C', 1, 1, 2, 1, 2, 1, 3, 1, 0),
		($2, $4, $5, TRUE, 9, 'C', 0, 2, 2, -1, 0, 0, 4, 2, 1),
		($3, $4, $5, TRUE, 9, 'C', 5, 5, 10, 5, 5, 5, 5, 5, 5),
		($1, $6, $5, TRUE, 4, 'D', 0, 1, 1, 0, 0, 0, 2, 3, 4)`,
		pgValuesGameA, pgValuesGameB, pgValuesGameFuture, pgValuesSkater, pgValuesNHLTeam, pgValuesOtherSkater)
	exec(`INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, decision, starter,
		shots_against, saves, goals_against, toi_seconds) VALUES
		($1, $3, $4, TRUE, 30, 'W', TRUE, 30, 30, 0, 3600),
		($2, $3, $4, TRUE, 30, 'L', TRUE, 25, 22, 3, 3500)`, pgValuesGameA, pgValuesGameB, pgValuesGoalie, pgValuesNHLTeam)
	// The skater wins two faceoffs and loses one in game A.
	exec(`INSERT INTO play_events (game_id, event_id, period, period_type, time_in_period, time_remaining, type_desc_key,
		sort_order, winning_player_id, losing_player_id) VALUES
		($1, 1, 1, 'REG', '00:00', '20:00', 'faceoff', 1, $2, $3),
		($1, 2, 1, 'REG', '05:00', '15:00', 'faceoff', 2, $2, $3),
		($1, 3, 1, 'REG', '10:00', '10:00', 'faceoff', 3, $3, $2)`, pgValuesGameA, pgValuesSkater, pgValuesOtherSkater)
}

func listTeamValueTotals(t *testing.T, q *sqlcdb.Queries, activeOnly bool) map[[2]int32]sqlcdb.ListYahooLeaguePlayerWindowTotalsRow {
	t.Helper()
	rows, err := q.ListYahooLeaguePlayerWindowTotals(context.Background(), sqlcdb.ListYahooLeaguePlayerWindowTotalsParams{
		LeagueKey: pgValuesLeagueKey, InactiveSlots: inactiveRosterSlots, LeagueID: pgValuesLeague,
		FromDate: mustDate(pgValuesFrom), ToDate: mustDate(pgValuesTo), ActiveOnly: activeOnly,
		MinimumShutoutToiSeconds: projection.DefaultGoalieShutoutMinTOI,
	})
	require.NoError(t, err)
	byKey := make(map[[2]int32]sqlcdb.ListYahooLeaguePlayerWindowTotalsRow, len(rows))
	for _, row := range rows {
		byKey[[2]int32{row.TeamID, row.YahooPlayerID}] = row
	}
	require.Len(t, byKey, len(rows), "one row per team and player")
	return byKey
}

func TestListYahooLeaguePlayerWindowTotalsReadsDatabase(t *testing.T) {
	pool := pgtest.Open(t)
	cleanupTeamValuesFixture(t, pool)
	t.Cleanup(func() { cleanupTeamValuesFixture(t, pool) })
	seedTeamValuesFixture(t, pool)
	q := sqlcdb.New(pool)

	rows := listTeamValueTotals(t, q, false)
	require.Len(t, rows, 5)
	skater := rows[[2]int32{pgValuesTeam, pgValuesSkater}]
	assert.Equal(t, sqlcdb.ListYahooLeaguePlayerWindowTotalsRow{
		TeamID: pgValuesTeam, YahooPlayerID: pgValuesSkater, PlayerKey: "465.p.990781001", PositionType: skaterPositionType,
		NhlPlayerID: pgtype.Int8{Int64: int64(pgValuesSkater), Valid: true}, NhlPosition: "C", Name: "Sam Skater",
		Days: 3, ActiveDays: 2, LastDate: mustDate(pgValuesTo),
		// Games A and B; the future game on the last day does not count.
		SkaterGames: 2, Goals: 1, Assists: 3, Points: 4, PlusMinus: 0, PenaltyMinutes: 2, PowerPlayPoints: 1,
		ShotsOnGoal: 7, Hits: 3, BlockedShots: 1, FaceoffsWon: 2, FaceoffsLost: 1,
	}, skater)
	goalie := rows[[2]int32{pgValuesTeam, pgValuesGoalie}]
	assert.Equal(t, sqlcdb.ListYahooLeaguePlayerWindowTotalsRow{
		TeamID: pgValuesTeam, YahooPlayerID: pgValuesGoalie, PlayerKey: "465.p.990781002",
		NhlPlayerID: pgtype.Int8{Int64: int64(pgValuesGoalie), Valid: true}, NhlPosition: "G", Name: "Gil Goalie",
		Days: 2, ActiveDays: 1, LastDate: mustDate(pgValuesGoalieLeaves),
		GoalieGames: 2, Wins: 1, Shutouts: 1, GoalsAgainst: 3, ShotsAgainst: 55, Saves: 52, GoalieTOISeconds: 7100,
	}, goalie)
	moved := rows[[2]int32{pgValuesOtherTeam, pgValuesGoalie}]
	assert.Equal(t, int32(1), moved.Days)
	assert.Equal(t, int64(0), moved.GoalieGames, "no final game on the day after the move")
	yahooOnly := rows[[2]int32{pgValuesTeam, pgValuesYahooOnly}]
	assert.False(t, yahooOnly.NhlPlayerID.Valid)
	assert.Equal(t, "Yahoo Only", yahooOnly.Name)
	assert.Equal(t, int32(0), yahooOnly.ActiveDays)
	other := rows[[2]int32{pgValuesOtherTeam, pgValuesOtherSkater}]
	assert.Equal(t, int32(1), other.Days, "the roster day before the window is left out")
	assert.Equal(t, int64(1), other.FaceoffsWon)
	assert.Equal(t, int64(2), other.FaceoffsLost)

	active := listTeamValueTotals(t, q, true)
	activeSkater := active[[2]int32{pgValuesTeam, pgValuesSkater}]
	assert.Equal(t, int32(3), activeSkater.Days, "active_only keeps every day in days")
	assert.Equal(t, int64(1), activeSkater.SkaterGames, "game B was a bench day")
	assert.Equal(t, int64(1), activeSkater.Goals)
	assert.Equal(t, int64(3), activeSkater.ShotsOnGoal)
	activeGoalie := active[[2]int32{pgValuesTeam, pgValuesGoalie}]
	assert.Equal(t, int64(1), activeGoalie.GoalieGames, "game B was an IR day")
	assert.Equal(t, int64(0), activeGoalie.GoalsAgainst)
}

// seedTeamValuesGameRulesFixture adds to the fixture what the totals must
// leave out: a final playoff game with a skater line on a window day, a
// relief shutout and a starter shutout under the minimum time on ice (both
// for the relief goalie of team 2), and a backup dressed with no ice time;
// and what it must keep: a starter shutout at exactly the minimum.
func seedTeamValuesGameRulesFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(context.Background(), sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO players (id, yahoo_id, first_name, last_name, position) VALUES
		($1, $1, 'Rex', 'Relief', 'G'), ($2, $2, 'Ben', 'Backup', 'G'), ($3, $3, 'Mia', 'Minimum', 'G')`,
		pgValuesReliefGoalie, pgValuesBackupGoalie, pgValuesMinimumGoalie)
	for _, r := range []struct {
		player int32
		date   string
	}{{pgValuesReliefGoalie, "3007-10-02"}, {pgValuesReliefGoalie, "3007-10-03"}, {pgValuesBackupGoalie, "3007-10-02"},
		{pgValuesMinimumGoalie, "3007-10-03"}} {
		exec(`INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, player_key, selected_position)
			VALUES ($1, $2, $3, $4, $5, 'G')`, pgValuesLeague, pgValuesOtherTeam, r.date, r.player, fmt.Sprintf("465.p.%d", r.player))
	}
	exec(`INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
		VALUES ($1, 30073008, 'playoffs', '3007-10-02', 'FINAL', $2, $3)`, pgValuesGamePlayoff, pgValuesNHLTeam, pgValuesNHLOpponent)
	exec(`INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, goals, assists, points,
		plus_minus, penalty_minutes, power_play_points, shots_on_goal, hits, blocked_shots) VALUES
		($1, $2, $3, TRUE, 9, 'C', $4, 0, $4, 0, 0, 0, 9, 0, 0)`,
		pgValuesGamePlayoff, pgValuesSkater, pgValuesNHLTeam, pgValuesPlayoffGoals)
	exec(`INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, decision, starter,
		shots_against, saves, goals_against, toi_seconds) VALUES
		($1, $3, $5, FALSE, 31, NULL, FALSE, 10, 10, 0, 3600),
		($2, $3, $5, FALSE, 31, 'W', TRUE, 20, 20, 0, $6),
		($1, $4, $5, FALSE, 35, NULL, FALSE, 0, 0, 0, 0),
		($2, $7, $5, FALSE, 40, 'W', TRUE, 25, 25, 0, $8)`,
		pgValuesGameA, pgValuesGameB, pgValuesReliefGoalie, pgValuesBackupGoalie, pgValuesNHLOpponent,
		projection.DefaultGoalieShutoutMinTOI-1, pgValuesMinimumGoalie, projection.DefaultGoalieShutoutMinTOI)
}

func TestListYahooLeaguePlayerWindowTotalsCountsOnlyRegularSeasonGamesPlayedAndShutoutsStarted(t *testing.T) {
	pool := pgtest.Open(t)
	cleanupTeamValuesFixture(t, pool)
	t.Cleanup(func() { cleanupTeamValuesFixture(t, pool) })
	seedTeamValuesFixture(t, pool)
	seedTeamValuesGameRulesFixture(t, pool)
	rows := listTeamValueTotals(t, sqlcdb.New(pool), false)

	skater := rows[[2]int32{pgValuesTeam, pgValuesSkater}]
	assert.Equal(t, int64(2), skater.SkaterGames, "the playoff game is left out")
	assert.Equal(t, int64(1), skater.Goals, "the playoff game is left out")
	relief := rows[[2]int32{pgValuesOtherTeam, pgValuesReliefGoalie}]
	assert.Equal(t, int64(2), relief.GoalieGames)
	assert.Equal(t, int64(1), relief.Wins)
	assert.Equal(t, int64(0), relief.Shutouts, "neither a relief shutout nor one under the minimum time on ice counts")
	assert.Equal(t, int64(3600+projection.DefaultGoalieShutoutMinTOI-1), relief.GoalieTOISeconds)
	backup := rows[[2]int32{pgValuesOtherTeam, pgValuesBackupGoalie}]
	assert.Equal(t, int32(1), backup.Days)
	assert.Equal(t, int64(0), backup.GoalieGames, "a game dressed with no time on ice is not played")
	assert.Equal(t, int64(0), backup.Shutouts)
	minimum := rows[[2]int32{pgValuesOtherTeam, pgValuesMinimumGoalie}]
	assert.Equal(t, int64(1), minimum.GoalieGames)
	assert.Equal(t, int64(1), minimum.Shutouts, "a starter shutout at exactly the minimum time on ice counts")
}

func seedProjection(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO projection_snapshots (target_season, as_of, model_version, config_hash,
		source_data_hash, lookback_seasons, season_decay, skater_prior_toi_seconds, goalie_prior_shots, goalie_shutout_min_toi,
		max_games, interval_z, minimum_uncertainty, maximum_uncertainty, minimum_history_games)
		VALUES (30073008, now(), 'test', 'c', 's', 3, 0.5, 1, 1, 3540, 82, 1, 0, 1, 1) RETURNING id`).Scan(&id))
	for _, key := range []string{pgValuesProjectionKey, pgValuesProjectionKey2} {
		_, err := pool.Exec(ctx, `INSERT INTO projection_players (snapshot_id, player_key, player_id, player_kind, position, source,
			source_as_of, history_seasons, history_games, sample_exposure, uncertainty, insufficient_history)
			VALUES ($1, $2, $3, 'skater', 'C', 'internal', now(), 1, 1, 1, 0, false)`, id, key, pgValuesSkater)
		require.NoError(t, err)
	}
	for _, v := range []struct {
		key, stat string
		mean      float64
	}{
		{pgValuesProjectionKey, string(projection.StatGamesPlayed), pgValuesProjectedGP},
		{pgValuesProjectionKey, string(projection.StatGoals), pgValuesProjectedG},
		{pgValuesProjectionKey2, string(projection.StatGoals), pgValuesProjectionGoal2},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO projection_values (snapshot_id, player_key, stat, mean, low, high)
			VALUES ($1, $2, $3, $4, $4, $4)`, id, v.key, v.stat, v.mean)
		require.NoError(t, err)
	}
	return id
}

func TestListProjectionValuesByNHLPlayersReadsDatabase(t *testing.T) {
	pool := pgtest.Open(t)
	cleanupTeamValuesFixture(t, pool)
	t.Cleanup(func() { cleanupTeamValuesFixture(t, pool) })
	seedTeamValuesFixture(t, pool)
	id := seedProjection(t, pool)

	rows, err := sqlcdb.New(pool).ListProjectionValuesByNHLPlayers(context.Background(), sqlcdb.ListProjectionValuesByNHLPlayersParams{
		SnapshotID: pgtype.UUID{Bytes: id, Valid: true}, NhlPlayerIds: []int64{int64(pgValuesSkater), int64(pgValuesGoalie)},
	})
	require.NoError(t, err)
	assert.Equal(t, []sqlcdb.ListProjectionValuesByNHLPlayersRow{
		{NhlPlayerID: int64(pgValuesSkater), PlayerKey: pgValuesProjectionKey, Stat: string(projection.StatGamesPlayed), Mean: pgValuesProjectedGP},
		{NhlPlayerID: int64(pgValuesSkater), PlayerKey: pgValuesProjectionKey, Stat: string(projection.StatGoals), Mean: pgValuesProjectedG},
		{NhlPlayerID: int64(pgValuesSkater), PlayerKey: pgValuesProjectionKey2, Stat: string(projection.StatGoals), Mean: pgValuesProjectionGoal2},
	}, rows)
	totals := projectedTotals(rows)
	assert.Len(t, totals[int64(pgValuesSkater)], pgValuesProjectionStatsCnt, "the second key is ignored")
}

func TestGetLatestYahooLeagueRosterDateOnOrBeforeReadsDatabase(t *testing.T) {
	pool := pgtest.Open(t)
	cleanupTeamValuesFixture(t, pool)
	t.Cleanup(func() { cleanupTeamValuesFixture(t, pool) })
	seedTeamValuesFixture(t, pool)
	q := sqlcdb.New(pool)
	for _, tc := range []struct {
		onOrBefore string
		want       pgtype.Date
	}{
		// The fixture's roster days are 09-30, 10-02, 10-03 and 10-04.
		{pgValuesTo, mustDate(pgValuesTo)},
		{"3007-10-05", mustDate(pgValuesTo)},
		{"3007-10-01", mustDate("3007-09-30")},
		{"3007-09-29", pgtype.Date{}},
	} {
		t.Run(tc.onOrBefore, func(t *testing.T) {
			got, err := q.GetLatestYahooLeagueRosterDateOnOrBefore(context.Background(),
				sqlcdb.GetLatestYahooLeagueRosterDateOnOrBeforeParams{LeagueID: pgValuesLeague, OnOrBefore: mustDate(tc.onOrBefore)})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

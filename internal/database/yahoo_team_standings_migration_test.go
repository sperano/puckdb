package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yahooTeamStandingsVersion adds the standings columns of
// yahoo_team_summaries and yahoo_team_summary_stats.
const yahooTeamStandingsVersion = 26

// standingsColumns are the columns yahooTeamStandingsVersion adds to
// yahoo_team_summaries.
var standingsColumns = []string{
	"standings_captured_at", "standings_rank", "standings_playoff_seed",
	"h2h_wins", "h2h_losses", "h2h_ties", "h2h_percentage",
	"h2h_division_wins", "h2h_division_losses", "h2h_division_ties", "h2h_division_percentage",
}

// SQLSTATE codes of the constraint violations the migration's constraints
// raise.
const (
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
)

// rejection is the constraint violation PostgreSQL must raise.
type rejection struct {
	code       string
	constraint string
}

// checkViolation is a violation of the CHECK constraint named constraint.
func checkViolation(constraint string) rejection {
	return rejection{code: sqlStateCheckViolation, constraint: constraint}
}

// requireRejected runs sql in a savepoint and requires PostgreSQL to refuse
// it with want, so a typo or a missing table cannot pass as a rejection.
func requireRejected(t *testing.T, conn *pgx.Conn, why string, want rejection, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, sql, args...)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "%s: want a PostgreSQL error, got %v", why, err)
	assert.Equal(t, want.code, pgErr.Code, why)
	assert.Equal(t, want.constraint, pgErr.ConstraintName, why)
}

// Up keeps the existing summaries with no standings snapshot, accepts a
// snapshot and arbitrary category rows, refuses inconsistent ones, and
// cascades a summary's deletion to its categories; down removes everything
// it added and keeps the summaries; up runs again.
func TestIntegration_YahooTeamStandingsMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, yahooTeamStandingsVersion-1)
	execSQL(t, conn, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES (7, '465.l.7', 'L', 2026)`)
	execSQL(t, conn, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES (7, 1, '465.l.7.t.1', 'T1'), (7, 2, '465.l.7.t.2', 'T2')`)
	execSQL(t, conn, `INSERT INTO yahoo_team_summaries (league_id, team_id, date, goals, wins) VALUES (7, 1, '2026-11-14', 2, 1)`)

	migrateTo(t, dbURL, yahooTeamStandingsVersion)
	for _, column := range standingsColumns {
		exists, nullable := columnNullable(t, conn, "yahoo_team_summaries", column)
		assert.True(t, exists, column)
		assert.True(t, nullable, column)
	}
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM yahoo_team_summaries WHERE goals = 2 AND wins = 1 AND standings_captured_at IS NULL AND h2h_wins IS NULL`),
		"an existing summary keeps its categories and has no snapshot")

	execSQL(t, conn, `UPDATE yahoo_team_summaries SET standings_captured_at = now(), standings_rank = 1,
		h2h_wins = 3, h2h_losses = 1, h2h_ties = 0, h2h_percentage = 0.75 WHERE team_id = 1`)
	execSQL(t, conn, `INSERT INTO yahoo_team_summaries (league_id, team_id, date, standings_captured_at, h2h_wins, h2h_losses, h2h_ties)
		VALUES (7, 2, '2026-11-14', now(), 0, 0, 0)`)
	execSQL(t, conn, `INSERT INTO yahoo_team_summary_stats (league_id, team_id, date, stat_id, value, raw_value)
		VALUES (7, 1, '2026-11-14', 0, 3, '3'), (7, 1, '2026-11-14', 4242, NULL, '-')`)

	requireRejected(t, conn, "standings without a capture", checkViolation("yahoo_team_summaries_standings_snapshot_check"),
		`UPDATE yahoo_team_summaries SET standings_captured_at = NULL WHERE team_id = 1`)
	requireRejected(t, conn, "a partial record", checkViolation("yahoo_team_summaries_h2h_record_check"),
		`UPDATE yahoo_team_summaries SET h2h_ties = NULL WHERE team_id = 1`)
	requireRejected(t, conn, "a rank of 0", checkViolation("yahoo_team_summaries_standings_rank_check"),
		`UPDATE yahoo_team_summaries SET standings_rank = 0 WHERE team_id = 1`)
	requireRejected(t, conn, "a percentage above 1", checkViolation("yahoo_team_summaries_h2h_percentage_check"),
		`UPDATE yahoo_team_summaries SET h2h_percentage = 1.5 WHERE team_id = 1`)
	requireRejected(t, conn, "a percentage without a record", checkViolation("yahoo_team_summaries_h2h_division_record_check"),
		`UPDATE yahoo_team_summaries SET h2h_division_percentage = 0.5 WHERE team_id = 1`)
	requireRejected(t, conn, "a category without its summary",
		rejection{code: sqlStateForeignKeyViolation, constraint: "yahoo_team_summary_stats_league_id_team_id_date_fkey"},
		`INSERT INTO yahoo_team_summary_stats (league_id, team_id, date, stat_id, raw_value) VALUES (7, 1, '2026-11-15', 1, '1')`)
	requireRejected(t, conn, "a non-finite value", checkViolation("yahoo_team_summary_stats_value_check"),
		`INSERT INTO yahoo_team_summary_stats (league_id, team_id, date, stat_id, value, raw_value) VALUES (7, 2, '2026-11-14', 1, 'NaN', 'NaN')`)

	execSQL(t, conn, `DELETE FROM yahoo_team_summaries WHERE team_id = 1`)
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM yahoo_team_summary_stats`), "categories cascade with their summary")

	migrateTo(t, dbURL, yahooTeamStandingsVersion-1)
	for _, column := range standingsColumns {
		exists, _ := columnNullable(t, conn, "yahoo_team_summaries", column)
		assert.False(t, exists, column)
	}
	assert.False(t, tableExists(t, conn, "yahoo_team_summary_stats"))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM yahoo_team_summaries`), "down keeps the summaries")

	migrateTo(t, dbURL, yahooTeamStandingsVersion)
}

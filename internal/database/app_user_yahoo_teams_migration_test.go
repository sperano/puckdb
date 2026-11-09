package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// appUserYahooTeamsVersion links a Maurice user to the Yahoo teams they
// manage.
const appUserYahooTeamsVersion = 30

const (
	appUserYahooTeamsUser1 = "aaaaaaaa-0000-0000-0000-000000000001"
	appUserYahooTeamsUser2 = "aaaaaaaa-0000-0000-0000-000000000002"
)

// Up creates app_user_yahoo_teams with its source CHECK, primary key and
// cascading foreign keys to app_users and yahoo_teams; down drops it.
func TestIntegration_AppUserYahooTeamsMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, appUserYahooTeamsVersion-1)
	execSQL(t, conn, `INSERT INTO app_users (id, instance, auth_provider, auth_subject, username) VALUES ($1, 'puckdb-test', 'authentik', 'sub-1', 'u1')`, appUserYahooTeamsUser1)
	execSQL(t, conn, `INSERT INTO app_users (id, instance, auth_provider, auth_subject, username) VALUES ($1, 'puckdb-test', 'authentik', 'sub-2', 'u2')`, appUserYahooTeamsUser2)
	execSQL(t, conn, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES (1, '465.l.1', 'L', 2026)`)
	execSQL(t, conn, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES (1, 1, '465.l.1.t.1', 'T1')`)

	migrateTo(t, dbURL, appUserYahooTeamsVersion)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'email')`, appUserYahooTeamsUser1)

	requireRejected(t, conn, "a duplicate (user, league, team) row",
		rejection{code: sqlStateUniqueViolation, constraint: "app_user_yahoo_teams_pkey"},
		`INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'manual')`, appUserYahooTeamsUser1)
	requireRejected(t, conn, "an unknown source",
		checkViolation("app_user_yahoo_teams_source_check"),
		`INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'bogus')`, appUserYahooTeamsUser2)
	requireRejected(t, conn, "an unknown team",
		rejection{code: sqlStateForeignKeyViolation, constraint: "app_user_yahoo_teams_league_id_team_id_fkey"},
		`INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 2, 'manual')`, appUserYahooTeamsUser2)

	execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'manual')`, appUserYahooTeamsUser2)
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_teams`))

	execSQL(t, conn, `DELETE FROM app_users WHERE id = $1`, appUserYahooTeamsUser1)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_teams`), "ON DELETE CASCADE from app_users")

	execSQL(t, conn, `DELETE FROM yahoo_teams WHERE league_id = 1 AND id = 1`)
	assert.Equal(t, 0, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_teams`), "ON DELETE CASCADE from yahoo_teams")

	migrateTo(t, dbURL, appUserYahooTeamsVersion-1)
	assert.Equal(t, 0, countRows(t, conn, `SELECT count(*) FROM pg_tables WHERE tablename = 'app_user_yahoo_teams'`))
}

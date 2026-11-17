package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// appUserTeamPickerVersion adds the 'self' link source, team deselections
// and the team offer flag.
const appUserTeamPickerVersion = 34

// Up accepts a 'self' link and a deselection of a known team only, deleted
// with its team; down turns every 'self' link into a manual one, drops the
// deselections and the offer flag, and refuses 'self' again.
func TestIntegration_AppUserTeamPickerMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, appUserTeamPickerVersion)
	seedManagersFixture(t, conn)

	execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 4, 1, 'self')`, managersUser1)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 3, 1, 'email')`, managersUser1)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, $2, 'self')`, managersUser1, managersGUID)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTWO', 'email')`, managersUser1)
	requireRejected(t, conn, "an unknown team link source", checkViolation("app_user_yahoo_teams_source_check"),
		`INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'bogus')`, managersUser2)
	requireRejected(t, conn, "an unknown guid link source", checkViolation("app_user_yahoo_managers_source_check"),
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTWO', 'bogus')`, managersUser2)

	execSQL(t, conn, `INSERT INTO app_user_yahoo_team_rejections (user_id, league_id, team_id) VALUES ($1, 1, 1), ($1, 2, 1)`, managersUser1)
	requireRejected(t, conn, "a deselection of an unknown team",
		rejection{code: sqlStateForeignKeyViolation, constraint: "app_user_yahoo_team_rejections_league_id_team_id_fkey"},
		`INSERT INTO app_user_yahoo_team_rejections (user_id, league_id, team_id) VALUES ($1, 99, 1)`, managersUser1)
	requireRejected(t, conn, "a duplicate deselection",
		rejection{code: sqlStateUniqueViolation, constraint: "app_user_yahoo_team_rejections_pkey"},
		`INSERT INTO app_user_yahoo_team_rejections (user_id, league_id, team_id) VALUES ($1, 1, 1)`, managersUser1)
	execSQL(t, conn, `DELETE FROM yahoo_teams WHERE league_id = 2`)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_team_rejections`), "a deleted team's deselection goes with it")

	exists, nullable := columnNullable(t, conn, "app_users", "team_offer_seen_at")
	assert.True(t, exists)
	assert.True(t, nullable, "NULL until the offer is seen")
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM app_users WHERE team_offer_seen_at IS NULL`))

	migrateTo(t, dbURL, appUserTeamPickerVersion-1)
	assert.Equal(t, []string{"3:email", "4:manual"}, userTeamLinks(t, conn, managersUser1), "a self team link becomes manual")
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = $2 AND source = 'manual'`, managersUser1, managersGUID),
		"a self guid link becomes manual")
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = 'GUIDTWO' AND source = 'email'`, managersUser1))
	assert.Equal(t, 0, countRows(t, conn, `SELECT count(*) FROM pg_class WHERE relname = 'app_user_yahoo_team_rejections'`))
	exists, _ = columnNullable(t, conn, "app_users", "team_offer_seen_at")
	assert.False(t, exists)
	requireRejected(t, conn, "a self team link below the picker", checkViolation("app_user_yahoo_teams_source_check"),
		`INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 1, 1, 'self')`, managersUser2)
	requireRejected(t, conn, "a self guid link below the picker", checkViolation("app_user_yahoo_managers_source_check"),
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTWO', 'self')`, managersUser2)
}

package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appUserYahooManagersVersion links a Maurice user to a Yahoo account
// (guid) and backfills the account links from the team links.
const appUserYahooManagersVersion = 32

const (
	managersUser1 = "bbbbbbbb-0000-0000-0000-000000000001"
	managersUser2 = "bbbbbbbb-0000-0000-0000-000000000002"
	// managersGUID is the account behind leagues 1, 2 and 5 (league 2's row
	// hides it, under the same email as league 1's in another case).
	managersGUID = "GUIDONE"
)

// seedManagersFixture seeds, below the migration: league 1 (2025) managed
// by managersGUID; league 2 (2026), its hidden-guid row with league 1's
// email; league 3, co-managed by two accounts; league 4, a hidden guid with
// an email no other row has; league 5 (2019), managersGUID under another
// email. Every league has one team, team 1.
func seedManagersFixture(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	for _, u := range []struct{ id, sub string }{{managersUser1, "sub-1"}, {managersUser2, "sub-2"}} {
		execSQL(t, conn, `INSERT INTO app_users (id, instance, auth_provider, auth_subject, username) VALUES ($1, 'puckdb-test', 'authentik', $2, $2)`, u.id, u.sub)
	}
	for league := 1; league <= 5; league++ {
		execSQL(t, conn, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1::integer, '465.l.' || $1::text, 'L', 2026)`, league)
		execSQL(t, conn, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1::integer, 1, '465.l.' || $1::text || '.t.1', 'T')`, league)
	}
	managers := []struct {
		league, id  int
		guid, email string
	}{
		{1, 1, managersGUID, "one@example.com"},
		{2, 1, "--hidden--", "ONE@example.com"},
		{3, 1, "GUIDTWO", ""},
		{3, 2, "GUIDTHREE", ""},
		{4, 1, "--hidden--", "newcomer@example.com"},
		{5, 1, managersGUID, "old@example.com"},
	}
	for _, m := range managers {
		execSQL(t, conn, `INSERT INTO yahoo_team_managers (league_id, team_id, id, guid, email) VALUES ($1, 1, $2, $3, $4)`,
			m.league, m.id, m.guid, m.email)
	}
}

// userTeamLinks returns userID's app_user_yahoo_teams rows as "league:source".
func userTeamLinks(t *testing.T, conn *pgx.Conn, userID string) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		`SELECT league_id::text || ':' || source FROM app_user_yahoo_teams WHERE user_id = $1 ORDER BY league_id`, userID)
	require.NoError(t, err)
	links, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return links
}

// Up resolves hidden guids through the email, turns team links of a
// single-account team into one account link per user (manual winning, the
// earliest linked_at kept) and leaves co-managed and unresolved teams as
// team links; down turns every account link back into team links, promoting
// an existing team link a manual account link reaches.
func TestIntegration_AppUserYahooManagersMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, appUserYahooManagersVersion-1)
	seedManagersFixture(t, conn)
	teamLinks := []struct {
		user           string
		league         int
		source, linked string
	}{
		{managersUser1, 1, "email", "2026-01-01"},
		{managersUser1, 2, "manual", "2026-02-01"},
		{managersUser1, 3, "email", "2026-01-01"},
		{managersUser1, 4, "manual", "2026-01-01"},
		{managersUser2, 2, "email", "2026-03-01"},
	}
	for _, l := range teamLinks {
		execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source, linked_at) VALUES ($1, $2, 1, $3, $4::date)`,
			l.user, l.league, l.source, l.linked)
	}

	migrateTo(t, dbURL, appUserYahooManagersVersion)

	rows, err := conn.Query(context.Background(),
		`SELECT league_id::text || ':' || manager_id::text || ':' || guid FROM yahoo_team_manager_guids ORDER BY league_id, manager_id`)
	require.NoError(t, err)
	resolved, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"1:1:GUIDONE", "2:1:GUIDONE", "3:1:GUIDTWO", "3:2:GUIDTHREE", "5:1:GUIDONE"}, resolved,
		"league 2's hidden guid is recovered by email; league 4's is not")

	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = $2 AND source = 'manual' AND linked_at = '2026-01-01'`,
		managersUser1, managersGUID), "manual wins, earliest linked_at kept")
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = $2 AND source = 'email'`,
		managersUser2, managersGUID))
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_managers`))
	assert.Equal(t, []string{"3:email", "4:manual"}, userTeamLinks(t, conn, managersUser1), "co-managed and unresolved teams stay team links")
	assert.Empty(t, userTeamLinks(t, conn, managersUser2))

	requireRejected(t, conn, "a hidden guid", checkViolation("app_user_yahoo_managers_guid_check"),
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, '--hidden--', 'manual')`, managersUser2)
	requireRejected(t, conn, "an empty guid", checkViolation("app_user_yahoo_managers_guid_check"),
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, '', 'manual')`, managersUser2)
	requireRejected(t, conn, "an unknown source", checkViolation("app_user_yahoo_managers_source_check"),
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTWO', 'bogus')`, managersUser2)
	requireRejected(t, conn, "a duplicate (user, guid) row",
		rejection{code: sqlStateUniqueViolation, constraint: "app_user_yahoo_managers_pkey"},
		`INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, $2, 'manual')`, managersUser2, managersGUID)

	// A guid link reaching a team that already has a team link: user 1's
	// manual GUIDTWO link meets its email team link to league 3, user 2's
	// email GUIDTHREE link an email team link it gets here.
	execSQL(t, conn, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTWO', 'manual')`, managersUser1)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, 'GUIDTHREE', 'email')`, managersUser2)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, 3, 1, 'email')`, managersUser2)

	migrateTo(t, dbURL, appUserYahooManagersVersion-1)
	assert.Equal(t, []string{"1:manual", "2:manual", "3:manual", "4:manual", "5:manual"}, userTeamLinks(t, conn, managersUser1),
		"every team the account reaches comes back as a team link, an existing one promoted by a manual guid link")
	assert.Equal(t, []string{"1:email", "2:email", "3:email", "5:email"}, userTeamLinks(t, conn, managersUser2),
		"an email guid link leaves an existing email team link as it is")
	assert.Equal(t, 0, countRows(t, conn,
		`SELECT count(*) FROM pg_class WHERE relname IN ('app_user_yahoo_managers', 'yahoo_team_manager_guids')`))
}

// Deleting a user deletes its account links.
func TestIntegration_AppUserYahooManagersCascadeFromUsers(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, appUserYahooManagersVersion-1)
	seedManagersFixture(t, conn)
	migrateTo(t, dbURL, appUserYahooManagersVersion)
	execSQL(t, conn, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, $2, 'manual')`, managersUser1, managersGUID)

	execSQL(t, conn, `DELETE FROM app_users WHERE id = $1`, managersUser1)
	assert.Equal(t, 0, countRows(t, conn, `SELECT count(*) FROM app_user_yahoo_managers`))
}

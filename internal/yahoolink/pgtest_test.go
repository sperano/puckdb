package yahoolink

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// PostgreSQL-backed test settings (see CLAUDE.md "Database-backed tests").
const (
	testInstance = "puckdb-test"
	// testSeason is the yahoo_leagues.season seedLeagueTeam stores: Yahoo's
	// start year, as the importer writes it (2026 for 2026-27).
	testSeason = 2026
	// testLeagueName is the yahoo_leagues.name seedLeagueTeam stores.
	testLeagueName = "yahoolink test"
)

// newLeagueID returns a Yahoo league ID no fixture elsewhere in the test
// suite uses: tests share the database and never truncate it.
func newLeagueID() int32 { return 10_000_000 + rand.Int32N(1_000_000_000-10_000_000) }

// seedLeagueTeam inserts a minimal league and team (team ID 1), returning
// the league and team IDs.
func seedLeagueTeam(t *testing.T, pool *pgxpool.Pool, teamName string) (leagueID, teamID int32) {
	t.Helper()
	return seedLeagueTeamInSeason(t, pool, teamName, testLeagueName, testSeason)
}

// seedLeagueTeamInSeason is seedLeagueTeam for a league named leagueName in
// season (a Yahoo start year).
func seedLeagueTeamInSeason(t *testing.T, pool *pgxpool.Pool, teamName, leagueName string, season int32) (leagueID, teamID int32) {
	t.Helper()
	leagueID, teamID = newLeagueID(), 1
	key := fmt.Sprintf("465.l.%d", leagueID)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, $3, $4)`,
		leagueID, key, leagueName, season)
	require.NoError(t, err)
	seedTeam(t, pool, leagueID, teamID, teamName)
	return leagueID, teamID
}

// seedTeam adds another team to an already-seeded league, for tests that
// must tell two teams apart by team_id within one league_id.
func seedTeam(t *testing.T, pool *pgxpool.Pool, leagueID, teamID int32, teamName string) {
	t.Helper()
	key := fmt.Sprintf("465.l.%d", leagueID)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, $4)`,
		leagueID, teamID, fmt.Sprintf("%s.t.%d", key, teamID), teamName)
	require.NoError(t, err)
}

// seedManager gives leagueID/teamID a manager with email.
func seedManager(t *testing.T, pool *pgxpool.Pool, leagueID, teamID, managerID int32, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, email) VALUES ($1, $2, $3, 'Manager', $4)`,
		leagueID, teamID, managerID, email)
	require.NoError(t, err)
}

// insertUser inserts a minimal app_users row on testInstance under username
// (which may repeat, deliberately, in an ambiguity test) and returns its ID.
// Every call gets its own auth_subject, so distinct calls are always
// distinct users regardless of username.
func insertUser(t *testing.T, pool *pgxpool.Pool, username string) pgtype.UUID {
	t.Helper()
	return insertUserOnInstance(t, pool, testInstance, username)
}

// insertUserOnInstance is insertUser for a caller that must name the
// instance, such as a test of instance scoping.
func insertUserOnInstance(t *testing.T, pool *pgxpool.Pool, instance, username string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ($1, 'authentik', $2, $3) RETURNING id`,
		instance, fmt.Sprintf("sub-%d", newLeagueID()), username).Scan(&id)
	require.NoError(t, err)
	return id
}

// uniqueUsername returns a username no earlier test run used: the test
// database is shared and never truncated, so a fixed literal would collide
// with a prior run's leftover row once FindUser matches on it.
func uniqueUsername(label string) string {
	return fmt.Sprintf("%s-%d", label, newLeagueID())
}

// hiddenGUID is the guid Yahoo serves for every current-season manager.
const hiddenGUID = "--hidden--"

// newGUID returns a Yahoo-style guid no other fixture uses: the email bridge
// of yahoo_team_manager_guids spans the whole shared table.
func newGUID() string { return fmt.Sprintf("TESTGUID%d", newLeagueID()) }

// seedManagerGUID gives leagueID/teamID a manager with guid and email.
func seedManagerGUID(t *testing.T, pool *pgxpool.Pool, leagueID, teamID, managerID int32, guid, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, guid, email) VALUES ($1, $2, $3, 'Manager', $4, $5)`,
		leagueID, teamID, managerID, guid, email)
	require.NoError(t, err)
}

// teamKeys returns the (league, team) pairs of links, in order.
func teamKeys(links []Link) []teamKey {
	keys := make([]teamKey, len(links))
	for i, l := range links {
		keys[i] = teamKey{l.LeagueID, l.TeamID}
	}
	return keys
}

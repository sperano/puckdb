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
)

// newLeagueID returns a Yahoo league ID no fixture elsewhere in the test
// suite uses: tests share the database and never truncate it.
func newLeagueID() int32 { return 10_000_000 + rand.Int32N(1_000_000_000-10_000_000) }

// seedLeagueTeam inserts a minimal league and team (team ID 1), returning
// the league and team IDs.
func seedLeagueTeam(t *testing.T, pool *pgxpool.Pool, teamName string) (leagueID, teamID int32) {
	t.Helper()
	leagueID, teamID = newLeagueID(), 1
	key := fmt.Sprintf("465.l.%d", leagueID)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'yahoolink test', 20262027)`,
		leagueID, key)
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

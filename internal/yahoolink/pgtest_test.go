package yahoolink

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/require"
)

// PostgreSQL-backed test settings (see CLAUDE.md "Database-backed tests").
const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
	testInstance     = "puckdb-test"
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker, "%s must name a dedicated test database", envTestPGURL)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// newLeagueID returns a Yahoo league ID no fixture elsewhere in the test
// suite uses: tests share the database and never truncate it.
func newLeagueID() int32 { return 10_000_000 + rand.Int32N(1_000_000_000-10_000_000) }

// seedLeagueTeam inserts a minimal league and team (team ID 1), returning
// the league ID.
func seedLeagueTeam(t *testing.T, pool *pgxpool.Pool, teamName string) (leagueID, teamID int32) {
	t.Helper()
	leagueID, teamID = newLeagueID(), 1
	key := fmt.Sprintf("465.l.%d", leagueID)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'yahoolink test', 20262027)`,
		leagueID, key)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, $4)`,
		leagueID, teamID, key+".t.1", teamName)
	require.NoError(t, err)
	return leagueID, teamID
}

// seedManager gives leagueID/teamID a manager with email.
func seedManager(t *testing.T, pool *pgxpool.Pool, leagueID, teamID, managerID int32, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, email) VALUES ($1, $2, $3, 'Manager', $4)`,
		leagueID, teamID, managerID, email)
	require.NoError(t, err)
}

// insertUser inserts a minimal app_users row under username (which may
// repeat, deliberately, in an ambiguity test) and returns its ID. Every
// call gets its own auth_subject, so distinct calls are always distinct
// users regardless of username.
func insertUser(t *testing.T, pool *pgxpool.Pool, username string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ($1, 'authentik', $2, $3) RETURNING id`,
		testInstance, fmt.Sprintf("sub-%d", newLeagueID()), username).Scan(&id)
	require.NoError(t, err)
	return id
}

// uniqueUsername returns a username no earlier test run used: the test
// database is shared and never truncated, so a fixed literal would collide
// with a prior run's leftover row once FindUser matches on it.
func uniqueUsername(label string) string {
	return fmt.Sprintf("%s-%d", label, newLeagueID())
}

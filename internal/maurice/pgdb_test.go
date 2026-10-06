package maurice

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envTestPGURL names the Postgres URL the PostgreSQL contract tests run
// against. Unset → the tests skip: a missing env var means the developer
// didn't opt into a database-backed run, not that the suite is broken. The
// schema is migrated up (never down) and the maurice tables are truncated
// before each test, so point it at a dedicated test database, e.g.
//
//	PUCKDB_TEST_PG_URL=postgres://puckdb:foo@localhost:5432/puckdb_maurice_test?sslmode=disable
const envTestPGURL = "PUCKDB_TEST_PG_URL"

// testDBNameMarker must appear in the URL so a developer's real database can't
// be migrated and truncated by a mistyped env var.
const testDBNameMarker = "test"

// pgMigrateOnce runs the embedded migrations a single time per test process;
// every openPgTestPool call after the first reuses the result.
var pgMigrateOnce struct {
	sync.Once
	err error
}

// openPgTestURL returns the URL of the migrated test database, or skips the
// test when envTestPGURL is unset.
func openPgTestURL(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL contract tests", envTestPGURL)
	}
	// The harness migrates and truncates whatever database it is pointed at;
	// refuse anything not obviously a test database.
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")
	return dbURL
}

// openPgTestPool returns a pool on a migrated database with empty maurice
// tables, or skips the test when envTestPGURL is unset. app_users is left
// alone (other packages' tests use it concurrently); every test makes its own
// users with newPgUser.
func openPgTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := openPgTestURL(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, "TRUNCATE maurice_conversations CASCADE")
	require.NoError(t, err)
	return pool
}

// newPgUser inserts an app_users row and returns its ID.
func newPgUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO app_users (auth_provider, auth_subject, username) VALUES ('authentik', $1, 'tester') RETURNING id::text`,
		uuid.NewString(),
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestPg_Contract(t *testing.T) {
	runDBContract(t, func(t *testing.T) contractEnv {
		pool := openPgTestPool(t)
		return contractEnv{db: NewPgDB(pool), newUser: func(t *testing.T) string { return newPgUser(t, pool) }}
	})
}

// jsonb rejects malformed JSON at insert time, so the corruption PostgreSQL
// can actually hold is well-formed JSON of the wrong shape. That must surface
// as an error rather than a message silently stripped of its tool calls.
func TestPg_GetMessages_CorruptToolCallsIsError(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	ctx := context.Background()
	user := newPgUser(t, pool)
	start := chat(t, db, user, "", "q", "a")

	_, err := pool.Exec(ctx,
		`INSERT INTO maurice_messages (conversation_id, turn_id, message_number, role, tool_calls)
		 VALUES ($1, $2, 99, 'assistant', '{"not":"a list"}'::jsonb)`,
		start.ConversationID, start.TurnID,
	)
	require.NoError(t, err)

	msgs, err := db.GetMessages(ctx, user, start.ConversationID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode tool calls")
	assert.Nil(t, msgs)
}

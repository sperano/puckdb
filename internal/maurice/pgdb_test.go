package maurice

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openPgTestPool returns a pool on the migrated database PUCKDB_TEST_PG_URL
// names (pgtest), with empty maurice tables, or skips the test when the
// variable is unset. The schema is migrated up, never down, so point it at a
// dedicated test database, e.g.
//
//	PUCKDB_TEST_PG_URL=postgres://puckdb:foo@localhost:5432/puckdb_maurice_test?sslmode=disable
//
// app_users is left alone (other packages' tests use it); every test makes
// its own users with newPgUser.
func openPgTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pgtest.Open(t, "maurice_conversations")
}

// newPgUser inserts an app_users row and returns its ID.
func newPgUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ('puckdb', 'authentik', $1, 'tester') RETURNING id::text`,
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

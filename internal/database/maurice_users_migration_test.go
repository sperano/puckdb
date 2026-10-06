package database

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// mauriceUsersVersion adds Maurice users, sessions, turns and usage.
	mauriceUsersVersion = 22
	// testDatabasePrefix names the throwaway database; it keeps the "test"
	// marker the other harnesses require.
	testDatabasePrefix = "puckdb_test_migrate"
)

// freshDatabase creates an empty database next to the one envTestPGURL names
// and returns its URL. Migration 000001 qualifies its objects with "public.",
// so the schema isolation of isolatedSchema does not apply to it.
func freshDatabase(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	baseURL := os.Getenv(envTestPGURL)
	if baseURL == "" {
		t.Skipf("%s not set", envTestPGURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationDBTimeout)
	t.Cleanup(cancel)
	name := fmt.Sprintf("%s_%d", testDatabasePrefix, time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, baseURL)
	require.NoError(t, err)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Skipf("cannot create a throwaway database (needs CREATEDB): %v", err)
	}
	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	parsed.Path = "/" + name
	conn, err := pgx.Connect(ctx, parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), integrationDBTimeout)
		defer cleanupCancel()
		_ = conn.Close(cleanupCtx)
		_, _ = admin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(cleanupCtx)
	})
	return conn, parsed.String()
}

func migrateTo(t *testing.T, dbURL string, version uint) {
	t.Helper()
	m, err := openMigrator(migrationsFS, dbURL)
	require.NoError(t, err)
	defer func() { _, _ = m.Close() }()
	require.NoError(t, m.Migrate.Migrate(version))
}

func execSQL(t *testing.T, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	_, err := conn.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func countRows(t *testing.T, conn *pgx.Conn, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func columnNullable(t *testing.T, conn *pgx.Conn, table, column string) (exists, nullable bool) {
	t.Helper()
	var isNullable string
	err := conn.QueryRow(context.Background(),
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, table, column).Scan(&isNullable)
	if err == pgx.ErrNoRows {
		return false, false
	}
	require.NoError(t, err)
	return true, strings.EqualFold(isNullable, "YES")
}

// Up deletes conversations that predate ownership and makes ownership and
// turn linkage mandatory; down keeps succeeded transcripts but drops the
// messages of failed turns, which the old schema would replay; up runs again.
func TestIntegration_MauriceUsersMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, mauriceUsersVersion-1)
	execSQL(t, conn, `INSERT INTO maurice_conversations (id, title) VALUES ('11111111-1111-1111-1111-111111111111', 'legacy')`)
	execSQL(t, conn, `INSERT INTO maurice_messages (conversation_id, role, content) VALUES ('11111111-1111-1111-1111-111111111111', 'user', 'old')`)

	migrateTo(t, dbURL, mauriceUsersVersion)
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM maurice_conversations`), "legacy conversations are deleted")
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM maurice_messages`), "their messages cascade")
	for _, col := range [][2]string{{"maurice_conversations", "user_id"}, {"maurice_messages", "turn_id"}, {"maurice_messages", "message_number"}} {
		exists, nullable := columnNullable(t, conn, col[0], col[1])
		assert.True(t, exists, "%s.%s", col[0], col[1])
		assert.False(t, nullable, "%s.%s must be NOT NULL", col[0], col[1])
	}
	for _, table := range []string{"app_users", "app_sessions", "maurice_turns", "maurice_llm_calls", "maurice_llm_call_messages", "maurice_tool_calls", "app_user_usage"} {
		assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM pg_class WHERE relname = $1`, table), table)
	}

	seedOwnedTurns(t, conn)
	migrateTo(t, dbURL, mauriceUsersVersion-1)
	exists, _ := columnNullable(t, conn, "maurice_conversations", "user_id")
	assert.False(t, exists)
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM pg_class WHERE relname = 'maurice_turns'`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_conversations`),
		"transcripts survive a rollback; soft-deleted conversations do not")
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_messages WHERE content = 'kept'`))
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM maurice_messages WHERE content = 'failed prompt'`),
		"failed-turn messages must not become replayable history")

	migrateTo(t, dbURL, mauriceUsersVersion)
}

// seedOwnedTurns writes one conversation with a succeeded and a failed turn,
// and one deleted conversation.
func seedOwnedTurns(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	const (
		user = "22222222-2222-2222-2222-222222222222"
		conv = "33333333-3333-3333-3333-333333333333"
		ok   = "44444444-4444-4444-4444-444444444444"
		bad  = "55555555-5555-5555-5555-555555555555"
	)
	execSQL(t, conn, `INSERT INTO app_users (id, auth_provider, auth_subject, username) VALUES ($1, 'authentik', 'sub', 'u')`, user)
	execSQL(t, conn, `INSERT INTO maurice_conversations (id, user_id) VALUES ($1, $2)`, conv, user)
	execSQL(t, conn, `INSERT INTO maurice_conversations (user_id, deleted_at) VALUES ($1, now())`, user)
	execSQL(t, conn, `INSERT INTO maurice_turns (id, conversation_id, user_id, turn_number, idempotency_key, request_hash, status, error_class, started_at, completed_at)
		VALUES ($1, $3, $4, 0, 'k0', '\x00', 'succeeded', NULL, now(), now()),
		       ($2, $3, $4, 1, 'k1', '\x00', 'failed', 'provider_error', now(), now())`, ok, bad, conv, user)
	execSQL(t, conn, `INSERT INTO maurice_messages (conversation_id, turn_id, message_number, role, content)
		VALUES ($1, $2, 0, 'user', 'kept'), ($1, $3, 0, 'user', 'failed prompt')`, conv, ok, bad)
}

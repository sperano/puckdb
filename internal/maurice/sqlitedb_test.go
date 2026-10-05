package maurice

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	db, err := NewSQLiteDB(sqlDB)
	require.NoError(t, err)
	return db
}

// TestSQLite_Contract runs the backend-neutral persistence contract
// (dbcontract_test.go) against the SQLite adapter. Any string is a user.
func TestSQLite_Contract(t *testing.T) {
	runDBContract(t, func(t *testing.T) contractEnv {
		return contractEnv{db: openTestDB(t), newUser: func(*testing.T) string { return uuid.NewString() }}
	})
}

// The stored timestamp strings must sort in time order, since ListConversations
// and GetMessages ORDER BY a TEXT column. time.RFC3339Nano fails this exact
// case: "…11.12Z" > "…11.123456Z" as strings because 'Z' > '3'.
func TestSQLiteTimeLayout_SortsChronologically(t *testing.T) {
	earlier := time.Date(2026, 9, 17, 11, 0, 11, 120_000_000, time.UTC)
	later := earlier.Add(3_456 * time.Microsecond)

	assert.Less(t, earlier.Format(sqliteTimeLayout), later.Format(sqliteTimeLayout))
	assert.Greater(t, earlier.Format(time.RFC3339Nano), later.Format(time.RFC3339Nano),
		"RFC3339Nano is expected to misorder this pair; if it stops doing so the layout constant may be redundant")

	parsed, err := time.Parse(time.RFC3339Nano, earlier.Format(sqliteTimeLayout))
	require.NoError(t, err)
	assert.True(t, parsed.Equal(earlier), "padded values must still parse with RFC3339Nano")
}

// --- Corrupt-row tests: decode failures must surface, not be swallowed ---

// seedSQLiteRows returns a store plus its raw handle and one conversation,
// for tests that need to plant rows the adapter itself would never write.
func seedSQLiteRows(t *testing.T) (DB, *sql.DB, *Conversation) {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { raw.Close() })
	db, err := NewSQLiteDB(raw)
	require.NoError(t, err)
	start := chat(t, db, LocalUserID, "", "seed", "row")
	conv, err := db.GetConversation(context.Background(), LocalUserID, start.ConversationID)
	require.NoError(t, err)
	return db, raw, conv
}

func TestSQLite_GetMessages_CorruptToolCallsIsError(t *testing.T) {
	db, raw, conv := seedSQLiteRows(t)
	_, err := raw.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"corrupt", conv.ID, "assistant", "", "not json", time.Now().UTC().Format(sqliteTimeLayout),
	)
	require.NoError(t, err)

	msgs, err := db.GetMessages(context.Background(), LocalUserID, conv.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message corrupt: decode tool calls")
	assert.Nil(t, msgs)
}

func TestSQLite_GetMessages_CorruptTimestampIsError(t *testing.T) {
	db, raw, conv := seedSQLiteRows(t)
	_, err := raw.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)`,
		"corrupt", conv.ID, "user", "hi", "yesterday",
	)
	require.NoError(t, err)

	_, err = db.GetMessages(context.Background(), LocalUserID, conv.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `message corrupt: parse created_at "yesterday"`)
}

func TestSQLite_GetConversation_CorruptTimestampIsError(t *testing.T) {
	db, raw, conv := seedSQLiteRows(t)
	_, err := raw.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, "never", conv.ID)
	require.NoError(t, err)

	_, err = db.GetConversation(context.Background(), LocalUserID, conv.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `parse updated_at "never"`)

	_, err = db.ListConversations(context.Background(), LocalUserID, 10)
	require.Error(t, err, "a corrupt row must fail the listing rather than be silently zeroed")
}

// --- Error-path tests using a closed *sql.DB ---

// closedDB returns a *sql.DB whose underlying connection is already
// closed, so any subsequent Exec/Query call returns
// "sql: database is closed". This is the simplest deterministic way to
// exercise the SQL error branches without injecting a fake driver.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, d.Close())
	return d
}

// alreadyMigratedDB returns a sqliteDB wrapping a real :memory: DB whose
// schema has already been created. Used by tests that want to bypass
// the constructor and then close the underlying DB to force errors on
// subsequent operations.
func alreadyMigratedDB(t *testing.T) (*sqliteDB, *sql.DB) {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	_, err = NewSQLiteDB(d)
	require.NoError(t, err)
	return &sqliteDB{db: d}, d
}

func TestNewSQLiteDB_PragmaErrorOnClosedDB(t *testing.T) {
	db, err := NewSQLiteDB(closedDB(t))
	require.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "enable foreign keys")
}

func TestSQLite_BeginTurn_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	start, err := s.BeginTurn(context.Background(), turnParams(LocalUserID, "", "key", "hi"))
	require.Error(t, err)
	assert.Nil(t, start)
}

func TestSQLite_ListConversations_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	convs, err := s.ListConversations(context.Background(), LocalUserID, 10)
	require.Error(t, err)
	assert.Nil(t, convs)
}

func TestSQLite_GetMessages_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	msgs, err := s.GetMessages(context.Background(), LocalUserID, "any-conv-id")
	require.Error(t, err)
	assert.Nil(t, msgs)
}

// Error path: a closed DB fails the turn commit at BeginTx.
func TestSQLite_FinishTurn_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	ids, err := s.FinishTurn(context.Background(), TurnRecord{
		TurnID: "any", Status: TurnSucceeded, Messages: []TurnMessage{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	assert.Nil(t, ids)
}

// legacySQLiteSchema is the schema before owners and turns existed.
const legacySQLiteSchema = `
CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE messages (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	role TEXT NOT NULL, content TEXT NOT NULL DEFAULT '', tool_calls TEXT, tool_call_id TEXT, created_at TEXT NOT NULL
);
CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at);
CREATE INDEX idx_conversations_updated ON conversations(updated_at DESC);
`

// A REPL database from before owners and turns opens with its history intact:
// legacy conversations belong to LocalUserID and their messages stay in the
// transcript, ahead of new turns.
func TestNewSQLiteDB_UpgradesLegacySchema(t *testing.T) {
	ctx := context.Background()
	raw, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { raw.Close() })
	_, err = raw.Exec(legacySQLiteSchema)
	require.NoError(t, err)
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(sqliteTimeLayout)
	_, err = raw.Exec(`INSERT INTO conversations (id, created_at, updated_at) VALUES ('legacy', ?, ?)`, old, old)
	require.NoError(t, err)
	_, err = raw.Exec(`INSERT INTO messages (id, conversation_id, role, content, created_at) VALUES
		('m1', 'legacy', 'user', 'old question', ?), ('m2', 'legacy', 'assistant', 'old answer', ?)`, old, old)
	require.NoError(t, err)

	db, err := NewSQLiteDB(raw)
	require.NoError(t, err)
	_, err = NewSQLiteDB(raw)
	require.NoError(t, err, "opening an upgraded database again is a no-op")

	chat(t, db, LocalUserID, "legacy", "new question", "new answer")
	msgs, err := db.GetMessages(ctx, LocalUserID, "legacy")
	require.NoError(t, err)
	var contents []string
	for _, m := range msgs {
		contents = append(contents, m.Content)
	}
	assert.Equal(t, []string{"old question", "old answer", "new question", "new answer"}, contents)
}

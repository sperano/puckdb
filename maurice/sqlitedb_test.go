package maurice

import (
	"context"
	"database/sql"
	"testing"

	"github.com/sperano/puckdb/llm"
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

func TestSQLite_CreateAndGetConversation(t *testing.T) {
	db := openTestDB(t)

	conv, err := db.CreateConversation(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, conv.ID)
	assert.Nil(t, conv.Title)
	assert.False(t, conv.CreatedAt.IsZero())

	got, err := db.GetConversation(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, got.ID)
}

func TestSQLite_UpdateTitle(t *testing.T) {
	db := openTestDB(t)

	conv, _ := db.CreateConversation(context.Background())
	err := db.UpdateConversationTitle(context.Background(), conv.ID, "Hockey Chat")
	require.NoError(t, err)

	got, err := db.GetConversation(context.Background(), conv.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Title)
	assert.Equal(t, "Hockey Chat", *got.Title)
}

func TestSQLite_ListConversations(t *testing.T) {
	db := openTestDB(t)

	db.CreateConversation(context.Background())
	db.CreateConversation(context.Background())

	convs, err := db.ListConversations(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, convs, 2)
}

func TestSQLite_DeleteConversation(t *testing.T) {
	db := openTestDB(t)

	conv, _ := db.CreateConversation(context.Background())
	err := db.DeleteConversation(context.Background(), conv.ID)
	require.NoError(t, err)

	_, err = db.GetConversation(context.Background(), conv.ID)
	require.Error(t, err)
}

func TestSQLite_CreateAndGetMessages(t *testing.T) {
	db := openTestDB(t)

	conv, _ := db.CreateConversation(context.Background())

	// User message
	msg1, err := db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "Who scored the most goals?",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, msg1.ID)
	assert.Equal(t, "user", msg1.Role)

	// Assistant message with tool calls
	msg2, err := db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID,
		Role:           "assistant",
		ToolCalls: []llm.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "pg_read_query",
				Arguments: `{"sql":"SELECT 1"}`,
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, msg2.ToolCalls, 1)
	assert.Equal(t, "pg_read_query", msg2.ToolCalls[0].Function.Name)

	// Tool result
	_, err = db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID,
		Role:           "tool",
		Content:        `[{"count":894}]`,
		ToolCallID:     "call_1",
	})
	require.NoError(t, err)

	// Retrieve all messages
	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Equal(t, "call_1", msgs[2].ToolCallID)

	// Verify tool calls survived round-trip
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "call_1", msgs[1].ToolCalls[0].ID)
}

func TestSQLite_CascadeDelete(t *testing.T) {
	db := openTestDB(t)

	conv, _ := db.CreateConversation(context.Background())
	db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID, Role: "user", Content: "test",
	})

	// Delete conversation should cascade to messages
	err := db.DeleteConversation(context.Background(), conv.ID)
	require.NoError(t, err)

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestSQLite_MessageUpdatesConversationTimestamp(t *testing.T) {
	db := openTestDB(t)

	conv, _ := db.CreateConversation(context.Background())
	originalUpdated := conv.UpdatedAt

	db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID, Role: "user", Content: "test",
	})

	updated, _ := db.GetConversation(context.Background(), conv.ID)
	assert.True(t, !updated.UpdatedAt.Before(originalUpdated))
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

func TestSQLite_CreateConversation_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	conv, err := s.CreateConversation(context.Background())
	require.Error(t, err)
	assert.Nil(t, conv)
}

func TestSQLite_ListConversations_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	convs, err := s.ListConversations(context.Background(), 10)
	require.Error(t, err)
	assert.Nil(t, convs)
}

func TestSQLite_CreateMessage_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	msg, err := s.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: "missing-conv-id", Role: "user", Content: "hi",
	})
	require.Error(t, err)
	assert.Nil(t, msg)
}

func TestSQLite_GetMessages_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	msgs, err := s.GetMessages(context.Background(), "any-conv-id")
	require.Error(t, err)
	assert.Nil(t, msgs)
}

// --- ToolCalls JSON round-trip tests ---

// CreateMessage and GetMessages (via scanMessage) exercise a JSON
// round-trip when ToolCalls is non-empty. Pin both branches: empty
// (NULL in DB) and non-empty (JSON-encoded TEXT).

func TestSQLite_CreateMessage_WithToolCalls(t *testing.T) {
	db := openTestDB(t)
	conv, err := db.CreateConversation(context.Background())
	require.NoError(t, err)

	toolCalls := []llm.ToolCall{
		{
			ID:       "call-1",
			Type:     "function",
			Function: llm.ToolCallFunction{Name: "get_player", Arguments: `{"id":42}`},
		},
	}
	created, err := db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        "looking up player",
		ToolCalls:      toolCalls,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, "call-1", msgs[0].ToolCalls[0].ID)
	assert.Equal(t, "get_player", msgs[0].ToolCalls[0].Function.Name)
}

// --- CreateMessages: atomic turn persistence ---

// A failing write mid-batch must roll the whole turn back: the earlier,
// individually-valid inserts must NOT survive. Here the second message
// references a nonexistent conversation, tripping the FK constraint
// (PRAGMA foreign_keys is ON), after the first message already inserted.
func TestSQLite_CreateMessages_RollsBackOnMidBatchFailure(t *testing.T) {
	db := openTestDB(t)
	conv, err := db.CreateConversation(context.Background())
	require.NoError(t, err)

	params := []CreateMessageParams{
		{ConversationID: conv.ID, Role: "user", Content: "first (valid)"},
		{ConversationID: "does-not-exist", Role: "assistant", Content: "second (FK violation)"},
	}
	created, err := db.CreateMessages(context.Background(), params)
	require.Error(t, err, "FK violation on the second insert must fail the batch")
	assert.Nil(t, created)

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "the first insert must roll back with the failed turn")
}

// Happy path: every message is persisted in order, the returned slice
// matches params order, and the conversation's updated_at is bumped.
func TestSQLite_CreateMessages_PersistsWholeTurnInOrder(t *testing.T) {
	db := openTestDB(t)
	conv, err := db.CreateConversation(context.Background())
	require.NoError(t, err)
	originalUpdated := conv.UpdatedAt

	params := []CreateMessageParams{
		{ConversationID: conv.ID, Role: "user", Content: "q"},
		{ConversationID: conv.ID, Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: llm.ToolCallFunction{Name: "pg_read_query", Arguments: `{"sql":"SELECT 1"}`},
		}}},
		{ConversationID: conv.ID, Role: "tool", Content: "rows", ToolCallID: "call_1"},
		{ConversationID: conv.ID, Role: "assistant", Content: "final"},
	}
	created, err := db.CreateMessages(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, created, 4)
	assert.Equal(t, "final", created[3].Content)

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Equal(t, "call_1", msgs[2].ToolCallID)
	assert.Equal(t, "assistant", msgs[3].Role)
	assert.Equal(t, "final", msgs[3].Content)

	updated, err := db.GetConversation(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.True(t, !updated.UpdatedAt.Before(originalUpdated), "updated_at must be bumped for the turn")
}

// Empty batch is a no-op that returns no messages and no error.
func TestSQLite_CreateMessages_EmptyIsNoOp(t *testing.T) {
	db := openTestDB(t)
	created, err := db.CreateMessages(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, created)
}

// Error path: a closed DB fails the batch at BeginTx.
func TestSQLite_CreateMessages_ErrorOnClosedDB(t *testing.T) {
	s, raw := alreadyMigratedDB(t)
	require.NoError(t, raw.Close())

	created, err := s.CreateMessages(context.Background(), []CreateMessageParams{
		{ConversationID: "any", Role: "user", Content: "hi"},
	})
	require.Error(t, err)
	assert.Nil(t, created)
}

// CASCADE delete: deleting a conversation must remove its messages.
// The schema declares ON DELETE CASCADE; the constructor enables
// PRAGMA foreign_keys=ON. This test pins both halves of that contract.
func TestSQLite_DeleteConversation_CascadesMessages(t *testing.T) {
	db := openTestDB(t)
	conv, err := db.CreateConversation(context.Background())
	require.NoError(t, err)
	_, err = db.CreateMessage(context.Background(), CreateMessageParams{
		ConversationID: conv.ID, Role: "user", Content: "hello",
	})
	require.NoError(t, err)

	require.NoError(t, db.DeleteConversation(context.Background(), conv.ID))

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "messages must cascade-delete with their conversation")
}

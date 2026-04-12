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

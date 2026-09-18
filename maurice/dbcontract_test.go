package maurice

// Shared persistence contract for every DB implementation. Both the SQLite
// adapter (sqlitedb_test.go) and the PostgreSQL adapter (pgdb_test.go) run
// runDBContract against a fresh store, so observable behaviour — ordering,
// tool-call round-trips, cascade deletes, atomic turn writes — cannot drift
// between backends without one of them failing here.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbFactory returns a freshly initialised, empty store for one subtest.
type dbFactory func(t *testing.T) DB

// runDBContract runs every contract test against the given backend.
func runDBContract(t *testing.T, open dbFactory) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, db DB)
	}{
		{"CreateAndGetConversation", contractCreateAndGetConversation},
		{"UpdateTitle", contractUpdateTitle},
		{"ListConversationsNewestFirst", contractListConversationsNewestFirst},
		{"AppendedTurnMovesConversationFirst", contractAppendedTurnMovesConversationFirst},
		{"OneMessageTurnMovesConversationFirst", contractOneMessageTurnMovesConversationFirst},
		{"MessagesReturnedInTurnOrder", contractMessagesReturnedInTurnOrder},
		{"ToolCallsRoundTrip", contractToolCallsRoundTrip},
		{"CascadeDelete", contractCascadeDelete},
		{"MidBatchFailureRollsBackWholeTurn", contractMidBatchFailureRollsBackWholeTurn},
		{"MixedConversationBatchIsRejected", contractMixedConversationBatchIsRejected},
		{"EmptyBatchIsNoOp", contractEmptyBatchIsNoOp},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, open(t))
		})
	}
}

// createOne writes a one-message turn and returns the stored message.
func createOne(t *testing.T, db DB, p CreateMessageParams) (*Message, error) {
	t.Helper()
	msgs, err := db.CreateMessages(context.Background(), []CreateMessageParams{p})
	if err != nil {
		return nil, err
	}
	return msgs[0], nil
}

func contractCreateAndGetConversation(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, conv.ID)
	assert.Nil(t, conv.Title)
	assert.False(t, conv.CreatedAt.IsZero())
	assert.False(t, conv.UpdatedAt.IsZero())

	got, err := db.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, got.ID)
	assert.Nil(t, got.Title)

	_, err = db.GetConversation(ctx, uuid.NewString())
	require.Error(t, err, "unknown conversation must be an error, not an empty value")
}

func contractUpdateTitle(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	require.NoError(t, db.UpdateConversationTitle(ctx, conv.ID, "Hockey Chat"))

	got, err := db.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Title)
	assert.Equal(t, "Hockey Chat", *got.Title)
}

func contractListConversationsNewestFirst(t *testing.T, db DB) {
	ctx := context.Background()
	older, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	newer, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	convs, err := db.ListConversations(ctx, 10)
	require.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, newer.ID, convs[0].ID)
	assert.Equal(t, older.ID, convs[1].ID)

	limited, err := db.ListConversations(ctx, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, newer.ID, limited[0].ID, "limit keeps the newest")
}

// Resuming an older conversation must move it to the top of the recent list:
// the turn write bumps updated_at, which ListConversations orders by.
func contractAppendedTurnMovesConversationFirst(t *testing.T, db DB) {
	ctx := context.Background()
	older, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	newer, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	created, err := db.CreateMessages(ctx, []CreateMessageParams{
		{ConversationID: older.ID, Role: "user", Content: "resume"},
		{ConversationID: older.ID, Role: "assistant", Content: "welcome back"},
	})
	require.NoError(t, err)

	convs, err := db.ListConversations(ctx, 10)
	require.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, older.ID, convs[0].ID, "conversation with the newest turn must list first")
	assert.Equal(t, newer.ID, convs[1].ID)

	got, err := db.GetConversation(ctx, older.ID)
	require.NoError(t, err)
	assert.True(t, got.UpdatedAt.After(older.UpdatedAt), "updated_at must advance past creation")
	// Message created_at may come from the client clock and updated_at from
	// the server clock (PostgreSQL); the harness runs against a same-host
	// database so this holds as long as the touch is stamped at write time
	// rather than at transaction start.
	lastMsg := created[len(created)-1]
	assert.False(t, got.UpdatedAt.Before(lastMsg.CreatedAt), "updated_at must not predate the turn it records")
}

// A one-message turn must move the conversation exactly like a longer one.
func contractOneMessageTurnMovesConversationFirst(t *testing.T, db DB) {
	ctx := context.Background()
	older, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	_, err = db.CreateConversation(ctx)
	require.NoError(t, err)

	_, err = createOne(t, db, CreateMessageParams{
		ConversationID: older.ID, Role: "user", Content: "resume",
	})
	require.NoError(t, err)

	convs, err := db.ListConversations(ctx, 10)
	require.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, older.ID, convs[0].ID)
}

// Messages come back in the order they were written, both within a turn and
// across turns, and the returned slice of CreateMessages matches params order.
func contractMessagesReturnedInTurnOrder(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	turn1 := []CreateMessageParams{
		{ConversationID: conv.ID, Role: "user", Content: "q1"},
		{ConversationID: conv.ID, Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_1", Type: "function",
			Function: llm.ToolCallFunction{Name: "pg_read_query", Arguments: `{"sql":"SELECT 1"}`},
		}}},
		{ConversationID: conv.ID, Role: "tool", Content: "rows", ToolCallID: "call_1"},
		{ConversationID: conv.ID, Role: "assistant", Content: "a1"},
	}
	created, err := db.CreateMessages(ctx, turn1)
	require.NoError(t, err)
	require.Len(t, created, len(turn1))
	for i, p := range turn1 {
		assert.NotEmpty(t, created[i].ID)
		assert.Equal(t, p.Role, created[i].Role, "returned slice must match params order")
	}

	_, err = db.CreateMessages(ctx, []CreateMessageParams{
		{ConversationID: conv.ID, Role: "user", Content: "q2"},
		{ConversationID: conv.ID, Role: "assistant", Content: "a2"},
	})
	require.NoError(t, err)

	msgs, err := db.GetMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 6)
	wantRoles := []string{"user", "assistant", "tool", "assistant", "user", "assistant"}
	wantContent := []string{"q1", "", "rows", "a1", "q2", "a2"}
	for i := range msgs {
		assert.Equal(t, wantRoles[i], msgs[i].Role, "message %d role", i)
		assert.Equal(t, wantContent[i], msgs[i].Content, "message %d content", i)
	}
	assert.Equal(t, "call_1", msgs[2].ToolCallID)
	for i := 1; i < len(msgs); i++ {
		assert.False(t, msgs[i].CreatedAt.Before(msgs[i-1].CreatedAt), "created_at must be non-decreasing")
	}
}

func contractToolCallsRoundTrip(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	toolCalls := []llm.ToolCall{
		{ID: "call-1", Type: "function", Function: llm.ToolCallFunction{Name: "get_player", Arguments: `{"id":42}`}},
		{ID: "call-2", Type: "function", Function: llm.ToolCallFunction{Name: "find_team", Arguments: `{"abbrev":"MTL"}`}},
	}
	created, err := createOne(t, db, CreateMessageParams{
		ConversationID: conv.ID, Role: "assistant", Content: "looking up", ToolCalls: toolCalls,
	})
	require.NoError(t, err)
	assert.Equal(t, toolCalls, created.ToolCalls, "returned message carries the tool calls")

	plain, err := createOne(t, db, CreateMessageParams{
		ConversationID: conv.ID, Role: "user", Content: "no tools",
	})
	require.NoError(t, err)
	assert.Empty(t, plain.ToolCalls)

	msgs, err := db.GetMessages(ctx, conv.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, toolCalls, msgs[0].ToolCalls, "tool calls must survive the round-trip")
	assert.Empty(t, msgs[0].ToolCallID)
	assert.Empty(t, msgs[1].ToolCalls, "absent tool calls must read back empty, not as a decoding error")
}

func contractCascadeDelete(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	_, err = createOne(t, db, CreateMessageParams{ConversationID: conv.ID, Role: "user", Content: "hello"})
	require.NoError(t, err)

	require.NoError(t, db.DeleteConversation(ctx, conv.ID))

	_, err = db.GetConversation(ctx, conv.ID)
	require.Error(t, err)
	msgs, err := db.GetMessages(ctx, conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "messages must cascade-delete with their conversation")
}

// A failing write mid-batch must roll the whole turn back: the earlier,
// individually-valid inserts must not survive. The second message references
// a nonexistent conversation, tripping the FK constraint after the first
// message already inserted. Both adapters issue the updated_at bump after the
// inserts, so on this path it never runs; the timestamp assertion guards
// against a future adapter bumping it up front or outside the transaction.
func contractMidBatchFailureRollsBackWholeTurn(t *testing.T, db DB) {
	ctx := context.Background()
	conv, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	created, err := db.CreateMessages(ctx, []CreateMessageParams{
		{ConversationID: conv.ID, Role: "user", Content: "first (valid)"},
		{ConversationID: uuid.NewString(), Role: "assistant", Content: "second (FK violation)"},
	})
	require.Error(t, err, "FK violation on the second insert must fail the batch")
	assert.Nil(t, created)

	msgs, err := db.GetMessages(ctx, conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "the first insert must roll back with the failed turn")

	got, err := db.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	assert.True(t, got.UpdatedAt.Equal(conv.UpdatedAt), "updated_at must roll back with the failed turn")
}

// A batch spanning two conversations is rejected up front: nothing is written
// to either conversation and neither activity timestamp moves.
func contractMixedConversationBatchIsRejected(t *testing.T, db DB) {
	ctx := context.Background()
	a, err := db.CreateConversation(ctx)
	require.NoError(t, err)
	b, err := db.CreateConversation(ctx)
	require.NoError(t, err)

	created, err := db.CreateMessages(ctx, []CreateMessageParams{
		{ConversationID: a.ID, Role: "user", Content: "for a"},
		{ConversationID: b.ID, Role: "user", Content: "for b"},
	})
	require.ErrorIs(t, err, ErrMixedConversations)
	assert.Nil(t, created)

	for _, conv := range []*Conversation{a, b} {
		msgs, err := db.GetMessages(ctx, conv.ID)
		require.NoError(t, err)
		assert.Empty(t, msgs)
		got, err := db.GetConversation(ctx, conv.ID)
		require.NoError(t, err)
		assert.True(t, got.UpdatedAt.Equal(conv.UpdatedAt), "updated_at must not move")
	}
}

func contractEmptyBatchIsNoOp(t *testing.T, db DB) {
	created, err := db.CreateMessages(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, created)
}

package maurice

// Shared persistence contract for every DB implementation. Both the SQLite
// adapter (sqlitedb_test.go) and the PostgreSQL adapter (pgdb_test.go) run
// runDBContract against a fresh store, so observable behaviour — owner
// scoping, idempotent turns, one running turn per conversation, the
// transcript excluding failed turns, ordering, deletion — cannot drift
// between backends without one of them failing here.

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contractStaleAfter keeps contract turns from going stale unless a test
// asks for it.
const contractStaleAfter = time.Hour

// contractEnv is a freshly initialised, empty store plus a way to make users
// it accepts.
type contractEnv struct {
	db      DB
	newUser func(t *testing.T) string
}

type envFactory func(t *testing.T) contractEnv

// runDBContract runs every contract test against the given backend.
func runDBContract(t *testing.T, open envFactory) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, env contractEnv)
	}{
		{"NewConversationTurn", contractNewConversationTurn},
		{"OwnerScoping", contractOwnerScoping},
		{"ListConversationsNewestFirst", contractListConversationsNewestFirst},
		{"FinishedTurnMovesConversationFirst", contractFinishedTurnMovesConversationFirst},
		{"MessagesReturnedInTurnOrder", contractMessagesReturnedInTurnOrder},
		{"IdempotentReplay", contractIdempotentReplay},
		{"RetriedFirstPromptKeepsOneConversation", contractRetriedFirstPromptKeepsOneConversation},
		{"KeyReuseWithDifferentRequestIsRejected", contractKeyReuseWithDifferentRequestIsRejected},
		{"RunningTurnBlocksAnother", contractRunningTurnBlocksAnother},
		{"StaleTurnIsAbandoned", contractStaleTurnIsAbandoned},
		{"StaleTurnReplayIsAbandoned", contractStaleTurnReplayIsAbandoned},
		{"FailedTurnStaysOutOfTranscript", contractFailedTurnStaysOutOfTranscript},
		{"FinishTwiceIsRejected", contractFinishTwiceIsRejected},
		{"DeleteHidesConversation", contractDeleteHidesConversation},
		{"RecordTitle", contractRecordTitle},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, open(t))
		})
	}
}

func turnParams(userID, convID, key, prompt string) BeginTurnParams {
	hash := sha256.Sum256([]byte(prompt))
	return BeginTurnParams{
		UserID: userID, ConversationID: convID, IdempotencyKey: key,
		RequestHash: hash[:], StaleAfter: contractStaleAfter,
	}
}

func beginTurn(t *testing.T, db DB, userID, convID, prompt string) *TurnStart {
	t.Helper()
	start, err := db.BeginTurn(context.Background(), turnParams(userID, convID, uuid.NewString(), prompt))
	require.NoError(t, err)
	require.Nil(t, start.Replay)
	return start
}

// finishTurn commits a succeeded turn: the prompt, any middle messages, and
// the answer.
func finishTurn(t *testing.T, db DB, start *TurnStart, prompt string, rest ...TurnMessage) []string {
	t.Helper()
	msgs := append([]TurnMessage{{Role: "user", Content: prompt}}, rest...)
	ids, err := db.FinishTurn(context.Background(), TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded, Messages: msgs,
	})
	require.NoError(t, err)
	require.Len(t, ids, len(msgs))
	return ids
}

// chat runs one succeeded prompt/answer turn and returns its start.
func chat(t *testing.T, db DB, userID, convID, prompt, answer string) *TurnStart {
	t.Helper()
	start := beginTurn(t, db, userID, convID, prompt)
	finishTurn(t, db, start, prompt, TurnMessage{Role: "assistant", Content: answer})
	return start
}

func contractNewConversationTurn(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "hello")
	assert.True(t, start.NewConversation)
	assert.Equal(t, 0, start.TurnNumber)
	require.NotEmpty(t, start.ConversationID)

	conv, err := env.db.GetConversation(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Nil(t, conv.Title)
	msgs, err := env.db.GetMessages(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "a running turn is not transcript")

	ids := finishTurn(t, env.db, start, "hello", TurnMessage{Role: "assistant", Content: "hi"})
	msgs, err = env.db.GetMessages(ctx, user, start.ConversationID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, ids[0], msgs[0].ID)
	assert.Equal(t, ids[1], msgs[1].ID)

	next := beginTurn(t, env.db, user, start.ConversationID, "again")
	assert.False(t, next.NewConversation)
	assert.Equal(t, 1, next.TurnNumber)
}

func contractOwnerScoping(t *testing.T, env contractEnv) {
	ctx := context.Background()
	owner, other := env.newUser(t), env.newUser(t)
	start := chat(t, env.db, owner, "", "mine", "yours")
	convID := start.ConversationID

	_, err := env.db.GetConversation(ctx, other, convID)
	require.ErrorIs(t, err, ErrConversationNotFound)
	msgs, err := env.db.GetMessages(ctx, other, convID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "another user's transcript must not leak")
	convs, err := env.db.ListConversations(ctx, other, 10)
	require.NoError(t, err)
	assert.Empty(t, convs)
	_, err = env.db.BeginTurn(ctx, turnParams(other, convID, uuid.NewString(), "hijack"))
	require.ErrorIs(t, err, ErrConversationNotFound)
	require.ErrorIs(t, env.db.RecordTitle(ctx, TitleRecord{UserID: other, ConversationID: convID, Title: "x"}), ErrConversationNotFound)
	require.ErrorIs(t, env.db.DeleteConversation(ctx, other, convID), ErrConversationNotFound)

	conv, err := env.db.GetConversation(ctx, owner, convID)
	require.NoError(t, err, "the owner's conversation survives another user's delete")
	assert.Nil(t, conv.Title)
	msgs, err = env.db.GetMessages(ctx, owner, convID)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
}

func contractListConversationsNewestFirst(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	older := chat(t, env.db, user, "", "q1", "a1")
	newer := chat(t, env.db, user, "", "q2", "a2")

	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, newer.ConversationID, convs[0].ID)
	assert.Equal(t, older.ConversationID, convs[1].ID)

	limited, err := env.db.ListConversations(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, newer.ConversationID, limited[0].ID, "limit keeps the newest")
}

// Resuming an older conversation must move it to the top of the recent list:
// the turn commit bumps updated_at, which ListConversations orders by.
func contractFinishedTurnMovesConversationFirst(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	older := chat(t, env.db, user, "", "q1", "a1")
	newer := chat(t, env.db, user, "", "q2", "a2")
	before, err := env.db.GetConversation(ctx, user, older.ConversationID)
	require.NoError(t, err)

	chat(t, env.db, user, older.ConversationID, "resume", "welcome back")

	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, older.ConversationID, convs[0].ID, "conversation with the newest turn must list first")
	assert.Equal(t, newer.ConversationID, convs[1].ID)
	after, err := env.db.GetConversation(ctx, user, older.ConversationID)
	require.NoError(t, err)
	assert.True(t, after.UpdatedAt.After(before.UpdatedAt), "updated_at must advance")
}

// Messages come back in the order they were written, within a turn and across
// turns, and tool calls survive the round-trip.
func contractMessagesReturnedInTurnOrder(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	toolCalls := []llm.ToolCall{
		{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "pg_read_query", Arguments: `{"sql":"SELECT 1"}`}},
		{ID: "call_2", Type: "function", Function: llm.ToolCallFunction{Name: "find_team", Arguments: `{"abbrev":"MTL"}`}},
	}
	first := beginTurn(t, env.db, user, "", "q1")
	finishTurn(t, env.db, first, "q1",
		TurnMessage{Role: "assistant", ToolCalls: toolCalls},
		TurnMessage{Role: "tool", Content: "rows", ToolCallID: "call_1"},
		TurnMessage{Role: "tool", Content: "team", ToolCallID: "call_2"},
		TurnMessage{Role: "assistant", Content: "a1"},
	)
	chat(t, env.db, user, first.ConversationID, "q2", "a2")

	msgs, err := env.db.GetMessages(ctx, user, first.ConversationID)
	require.NoError(t, err)
	wantRoles := []string{"user", "assistant", "tool", "tool", "assistant", "user", "assistant"}
	wantContent := []string{"q1", "", "rows", "team", "a1", "q2", "a2"}
	require.Len(t, msgs, len(wantRoles))
	for i := range msgs {
		assert.Equal(t, wantRoles[i], msgs[i].Role, "message %d role", i)
		assert.Equal(t, wantContent[i], msgs[i].Content, "message %d content", i)
	}
	assert.Equal(t, toolCalls, msgs[1].ToolCalls, "tool calls must survive the round-trip")
	assert.Empty(t, msgs[0].ToolCalls, "absent tool calls read back empty")
	assert.Equal(t, "call_1", msgs[2].ToolCallID)
	for i := 1; i < len(msgs); i++ {
		assert.False(t, msgs[i].CreatedAt.Before(msgs[i-1].CreatedAt), "created_at must be non-decreasing")
	}
}

func contractIdempotentReplay(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "key-1", "how many goals?")
	start, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	warnings := []string{"one data source is unavailable"}
	messages := []TurnMessage{{Role: "user", Content: "how many goals?"},
		TurnMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "goals"}}}},
		TurnMessage{Role: "tool", Content: "42", ToolCallID: "c1"},
		TurnMessage{Role: "assistant", Content: "42 goals"},
	}
	ids, err := env.db.FinishTurn(ctx, TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded,
		Messages: messages, Warnings: warnings,
	})
	require.NoError(t, err)

	params.ConversationID = start.ConversationID
	replay, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, replay.Replay)
	assert.Equal(t, TurnSucceeded, replay.Replay.Status)
	assert.Equal(t, start.TurnID, replay.TurnID)
	assert.Equal(t, start.ConversationID, replay.ConversationID)
	assert.Equal(t, ids[len(ids)-1], replay.Replay.MessageID)
	assert.Equal(t, "42 goals", replay.Replay.Content)
	assert.Equal(t, []string{"goals"}, replay.Replay.ToolsUsed)
	assert.Equal(t, warnings, replay.Replay.Warnings)

	msgs, err := env.db.GetMessages(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Len(t, msgs, 4, "a replay writes nothing")
}

func contractRetriedFirstPromptKeepsOneConversation(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "first-prompt", "hello")
	start, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	finishTurn(t, env.db, start, "hello", TurnMessage{Role: "assistant", Content: "hi"})

	replay, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, replay.Replay)
	assert.Equal(t, start.ConversationID, replay.ConversationID)
	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	assert.Len(t, convs, 1, "a retried first prompt must not open a second conversation")
}

func contractKeyReuseWithDifferentRequestIsRejected(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start, err := env.db.BeginTurn(ctx, turnParams(user, "", "shared", "prompt A"))
	require.NoError(t, err)
	finishTurn(t, env.db, start, "prompt A", TurnMessage{Role: "assistant", Content: "A"})
	elsewhere := chat(t, env.db, user, "", "other", "conversation")

	_, err = env.db.BeginTurn(ctx, turnParams(user, start.ConversationID, "shared", "prompt B"))
	require.ErrorIs(t, err, ErrIdempotencyKeyReused, "same key, different prompt")
	_, err = env.db.BeginTurn(ctx, turnParams(user, elsewhere.ConversationID, "shared", "prompt A"))
	require.ErrorIs(t, err, ErrIdempotencyKeyReused, "same key, different conversation")

	// Keys belong to one user: another user may use the same key freely.
	other := env.newUser(t)
	_, err = env.db.BeginTurn(ctx, turnParams(other, "", "shared", "prompt B"))
	require.NoError(t, err)
}

func contractRunningTurnBlocksAnother(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "running", "slow question")
	first, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)

	_, err = env.db.BeginTurn(ctx, turnParams(user, first.ConversationID, uuid.NewString(), "impatient"))
	require.ErrorIs(t, err, ErrTurnInProgress)
	params.ConversationID = first.ConversationID
	_, err = env.db.BeginTurn(ctx, params)
	require.ErrorIs(t, err, ErrTurnInProgress, "retrying a running turn's key must not run it twice")

	// Other conversations are not blocked.
	chat(t, env.db, user, "", "elsewhere", "fine")

	finishTurn(t, env.db, first, "slow question", TurnMessage{Role: "assistant", Content: "done"})
	next := beginTurn(t, env.db, user, first.ConversationID, "now")
	assert.Equal(t, 1, next.TurnNumber)
}

func contractStaleTurnIsAbandoned(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	dead := beginTurn(t, env.db, user, "", "crashed mid-turn")

	params := turnParams(user, dead.ConversationID, uuid.NewString(), "after the crash")
	params.StaleAfter = 0
	next, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err, "a stale running turn must not block the conversation")
	assert.Equal(t, 1, next.TurnNumber)

	_, err = env.db.FinishTurn(ctx, TurnRecord{TurnID: dead.TurnID, ConversationID: dead.ConversationID, Status: TurnSucceeded,
		Messages: []TurnMessage{{Role: "user", Content: "crashed mid-turn"}}})
	require.ErrorIs(t, err, ErrTurnNotRunning, "an abandoned turn cannot be committed late")
}

func contractStaleTurnReplayIsAbandoned(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "dead-key", "crashed mid-turn")
	dead, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)

	params.ConversationID, params.StaleAfter = dead.ConversationID, 0
	replay, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, replay.Replay)
	assert.Equal(t, TurnFailed, replay.Replay.Status)
	assert.Equal(t, ErrorClassAbandoned, replay.Replay.ErrorClass)

	beginTurn(t, env.db, user, dead.ConversationID, "conversation is free again")
}

func contractFailedTurnStaysOutOfTranscript(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	ok := chat(t, env.db, user, "", "q1", "a1")

	for _, status := range []TurnStatus{TurnFailed, TurnCancelled} {
		params := turnParams(user, ok.ConversationID, uuid.NewString(), "doomed")
		start, err := env.db.BeginTurn(ctx, params)
		require.NoError(t, err)
		class := ErrorClassProvider
		if status == TurnCancelled {
			class = ErrorClassCancelled
		}
		ids, err := env.db.FinishTurn(ctx, TurnRecord{
			TurnID: start.TurnID, ConversationID: start.ConversationID, Status: status, ErrorClass: class,
			Messages: []TurnMessage{
				{Role: "user", Content: "doomed"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "t"}}}},
				{Role: "tool", Content: "partial", ToolCallID: "c1"},
			},
		})
		require.NoError(t, err)
		assert.Len(t, ids, 3, "a failed turn keeps its messages as exact prompts")

		replay, err := env.db.BeginTurn(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, replay.Replay)
		assert.Equal(t, status, replay.Replay.Status)
		assert.Equal(t, class, replay.Replay.ErrorClass)
	}

	msgs, err := env.db.GetMessages(ctx, user, ok.ConversationID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "failed and cancelled turns must never be replayed as history")
	assert.Equal(t, "q1", msgs[0].Content)
	assert.Equal(t, "a1", msgs[1].Content)
}

func contractFinishTwiceIsRejected(t *testing.T, env contractEnv) {
	user := env.newUser(t)
	start := chat(t, env.db, user, "", "q", "a")
	_, err := env.db.FinishTurn(context.Background(), TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded,
		Messages: []TurnMessage{{Role: "user", Content: "q"}},
	})
	require.ErrorIs(t, err, ErrTurnNotRunning)
}

// Deleting hides the conversation from its owner for good: it cannot be read,
// listed, continued or replayed by key, and deleting it again finds nothing.
// Its rows stay (prompts and usage are kept forever; see the PostgreSQL test).
func contractDeleteHidesConversation(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "deleted-key", "hello")
	start, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	ids := finishTurn(t, env.db, start, "hello", TurnMessage{Role: "assistant", Content: "hi"})
	kept := chat(t, env.db, user, "", "other", "conversation")

	require.NoError(t, env.db.DeleteConversation(ctx, user, start.ConversationID))

	_, err = env.db.GetConversation(ctx, user, start.ConversationID)
	require.ErrorIs(t, err, ErrConversationNotFound)
	msgs, err := env.db.GetMessages(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Empty(t, msgs)
	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, convs, 1)
	assert.Equal(t, kept.ConversationID, convs[0].ID)
	_, err = env.db.BeginTurn(ctx, turnParams(user, start.ConversationID, uuid.NewString(), "more"))
	require.ErrorIs(t, err, ErrConversationNotFound)
	_, err = env.db.BeginTurn(ctx, params)
	require.ErrorIs(t, err, ErrConversationNotFound, "a key of a deleted conversation replays nothing")
	require.ErrorIs(t, env.db.DeleteConversation(ctx, user, start.ConversationID), ErrConversationNotFound)
	require.NoError(t, env.db.RecordTitle(ctx, TitleRecord{UserID: user, ConversationID: start.ConversationID,
		Title: "late", Call: titleCallRecord(ids)}), "a title finishing after the delete still records its call")
}

func contractRecordTitle(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "hello")
	ids := finishTurn(t, env.db, start, "hello", TurnMessage{Role: "assistant", Content: "hi"})
	call := titleCallRecord(ids)

	require.NoError(t, env.db.RecordTitle(ctx, TitleRecord{UserID: user, ConversationID: start.ConversationID, Call: call}))
	conv, err := env.db.GetConversation(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Nil(t, conv.Title, "an empty title leaves the conversation untitled")

	require.NoError(t, env.db.RecordTitle(ctx, TitleRecord{UserID: user, ConversationID: start.ConversationID, Title: "Hockey Chat", Call: call}))
	conv, err = env.db.GetConversation(ctx, user, start.ConversationID)
	require.NoError(t, err)
	require.NotNil(t, conv.Title)
	assert.Equal(t, "Hockey Chat", *conv.Title)
}

// titleCallRecord is a succeeded title-generation call over a turn's prompt
// and answer.
func titleCallRecord(turnIDs []string) LLMCallRecord {
	instruction := titleGenerationHint
	now := time.Now()
	return LLMCallRecord{
		Kind: CallTitleGeneration, Provider: "anthropic", Model: "test-model", Status: CallSucceeded,
		StartedAt: now, CompletedAt: now.Add(time.Millisecond), Instruction: &instruction,
		Inputs: []MessageRef{{MessageID: turnIDs[0]}, {MessageID: turnIDs[len(turnIDs)-1]}},
	}
}

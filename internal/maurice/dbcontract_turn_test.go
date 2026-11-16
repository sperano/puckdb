package maurice

// Contract cases for reading turns (GetTurn) and replaying a running turn
// (BeginTurnParams.ReplayRunning); runDBContract registers them.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contractRunningTurnReplayedWhenAsked(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "start-key", "slow question")
	params.ReplayRunning = true
	first, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.Nil(t, first.Replay)

	replay, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, replay.Replay)
	assert.Equal(t, first.TurnID, replay.TurnID)
	assert.Equal(t, first.ConversationID, replay.ConversationID)
	assert.Equal(t, TurnRunning, replay.Replay.Status)
	assert.Empty(t, replay.Replay.Content)

	_, err = env.db.BeginTurn(ctx, turnParams(user, first.ConversationID, uuid.NewString(), "impatient"))
	require.ErrorIs(t, err, ErrTurnInProgress, "another key still waits for the running turn")
	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	assert.Len(t, convs, 1, "the replay opened no conversation")
}

func contractGetTurnFollowsTheTurn(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "how many goals?")

	running, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, &Turn{ID: start.TurnID, ConversationID: start.ConversationID,
		TurnReplay: TurnReplay{Status: TurnRunning, Warnings: []string{}}}, running)

	warnings := []string{"one data source is unavailable"}
	ids, err := env.db.FinishTurn(ctx, TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded, Warnings: warnings,
		Messages: []TurnMessage{{Role: "user", Content: "how many goals?"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "goals"}}}},
			{Role: "tool", Content: "42", ToolCallID: "c1"},
			{Role: "assistant", Content: "42 goals"},
		},
	})
	require.NoError(t, err)

	done, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, &Turn{ID: start.TurnID, ConversationID: start.ConversationID, TurnReplay: TurnReplay{
		Status: TurnSucceeded, MessageID: ids[len(ids)-1], Content: "42 goals",
		ToolsUsed: []string{"goals"}, Warnings: warnings,
	}}, done)
}

func contractGetTurnReportsFailure(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "doomed")
	_, err := env.db.FinishTurn(ctx, TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnFailed, ErrorClass: ErrorClassTimeout,
		Messages: []TurnMessage{{Role: "user", Content: "doomed"}},
	})
	require.NoError(t, err)

	turn, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, TurnFailed, turn.Status)
	assert.Equal(t, ErrorClassTimeout, turn.ErrorClass)
	assert.Empty(t, turn.MessageID, "a failed turn has no answer")
	assert.Empty(t, turn.Content)
}

// A running turn whose process died reads as failed and abandoned, so a
// polling client stops; the stored row changes on the next BeginTurn.
func contractGetTurnReportsStaleTurnAbandoned(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	dead := beginTurn(t, env.db, user, "", "crashed mid-turn")

	turn, err := env.db.GetTurn(ctx, user, dead.TurnID, 0)
	require.NoError(t, err)
	assert.Equal(t, TurnFailed, turn.Status)
	assert.Equal(t, ErrorClassAbandoned, turn.ErrorClass)

	turn, err = env.db.GetTurn(ctx, user, dead.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, TurnRunning, turn.Status, "reading the turn wrote nothing")
}

// Another user's turn, an unknown or malformed ID and a turn of a deleted
// conversation are all not found.
func contractGetTurnIsOwnerScoped(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := chat(t, env.db, user, "", "q", "a")

	for name, read := range map[string]struct{ user, turn string }{
		"other user": {env.newUser(t), start.TurnID},
		"unknown id": {user, uuid.NewString()},
		"malformed":  {user, "not-a-turn"},
	} {
		_, err := env.db.GetTurn(ctx, read.user, read.turn, contractStaleAfter)
		require.ErrorIs(t, err, ErrTurnNotFound, name)
	}

	require.NoError(t, env.db.DeleteConversation(ctx, user, start.ConversationID))
	_, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.ErrorIs(t, err, ErrTurnNotFound, "deleted conversation")
}

package maurice

// Contract cases for reading turns (GetTurn), replaying a running turn
// (BeginTurnParams.ReplayRunning) and replaying without beginning
// (BeginTurnParams.ReplayOnly); runDBContract registers them.

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

// A stale running turn is abandoned before ReplayRunning is considered: a
// client resending the key of a turn whose process died learns it failed.
func contractStaleRunningTurnReplayedAsAbandoned(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "dead-start-key", "crashed mid-turn")
	params.ReplayRunning = true
	dead, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)

	params.StaleAfter = 0
	replay, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, replay.Replay)
	assert.Equal(t, dead.TurnID, replay.TurnID)
	assert.Equal(t, TurnFailed, replay.Replay.Status)
	assert.Equal(t, ErrorClassAbandoned, replay.Replay.ErrorClass)
	turn, err := env.db.GetTurn(ctx, user, dead.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, TurnFailed, turn.Status, "the abandoned status is stored")
}

// ReplayOnly replays a turn found under the key, running or finished, and
// begins nothing for an unknown key.
func contractReplayOnlyBeginsNothing(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	params := turnParams(user, "", "only-key", "q")
	params.ReplayRunning, params.ReplayOnly = true, true
	_, err := env.db.BeginTurn(ctx, params)
	require.ErrorIs(t, err, ErrNoTurnUnderKey)
	convs, err := env.db.ListConversations(ctx, user, 10)
	require.NoError(t, err)
	assert.Empty(t, convs, "no conversation was opened")

	params.ReplayOnly = false
	first, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	params.ReplayOnly = true
	running, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, running.Replay)
	assert.Equal(t, first.TurnID, running.TurnID)
	assert.Equal(t, TurnRunning, running.Replay.Status)

	finishTurn(t, env.db, first, "q", TurnMessage{Role: "assistant", Content: "a"})
	done, err := env.db.BeginTurn(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, done.Replay)
	assert.Equal(t, TurnSucceeded, done.Replay.Status)
	assert.Equal(t, "a", done.Replay.Content)
}

func contractGetTurnFollowsTheTurn(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "how many goals?")

	running, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, &Turn{ID: start.TurnID, ConversationID: start.ConversationID,
		TurnReplay: TurnReplay{Status: TurnRunning, ToolsUsed: []string{}, Warnings: []string{}}}, running)

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

// A running turn lists each tool as its call starts, in call order with
// duplicates; once succeeded, the list comes from its messages instead.
func contractGetTurnListsRunningTools(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "goals and assists?")
	for _, name := range []string{"goals", "assists", "goals"} {
		require.NoError(t, env.db.AppendTurnToolName(ctx, start.TurnID, name))
	}

	running, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, TurnRunning, running.Status)
	assert.Equal(t, []string{"goals", "assists", "goals"}, running.ToolsUsed)

	finishTurn(t, env.db, start, "goals and assists?",
		TurnMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "points"}}}},
		TurnMessage{Role: "tool", Content: "7", ToolCallID: "c1"},
		TurnMessage{Role: "assistant", Content: "7 points"},
	)
	done, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, TurnSucceeded, done.Status)
	assert.Equal(t, []string{"points"}, done.ToolsUsed, "a succeeded turn lists the tools of its messages")
}

// A turn that did not succeed keeps the tools it started: failed, cancelled
// and abandoned alike.
func contractGetTurnKeepsToolsOfUnfinishedTurn(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	for _, status := range []TurnStatus{TurnFailed, TurnCancelled} {
		start := beginTurn(t, env.db, user, "", "doomed "+string(status))
		require.NoError(t, env.db.AppendTurnToolName(ctx, start.TurnID, "goals"))
		_, err := env.db.FinishTurn(ctx, TurnRecord{
			TurnID: start.TurnID, ConversationID: start.ConversationID, Status: status, ErrorClass: ErrorClassTimeout,
			Messages: []TurnMessage{{Role: "user", Content: "doomed"}},
		})
		require.NoError(t, err)
		turn, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
		require.NoError(t, err)
		assert.Equal(t, status, turn.Status)
		assert.Equal(t, []string{"goals"}, turn.ToolsUsed, string(status))
	}

	dead := beginTurn(t, env.db, user, "", "crashed mid-tool")
	require.NoError(t, env.db.AppendTurnToolName(ctx, dead.TurnID, "standings"))
	turn, err := env.db.GetTurn(ctx, user, dead.TurnID, 0)
	require.NoError(t, err)
	assert.Equal(t, ErrorClassAbandoned, turn.ErrorClass)
	assert.Equal(t, []string{"standings"}, turn.ToolsUsed)
}

// Only a running turn takes tool names: a finished, unknown or malformed one
// is ErrTurnNotRunning, and a finished one keeps its list.
func contractAppendToolNameNeedsRunningTurn(t *testing.T, env contractEnv) {
	ctx := context.Background()
	user := env.newUser(t)
	start := beginTurn(t, env.db, user, "", "q")
	require.NoError(t, env.db.AppendTurnToolName(ctx, start.TurnID, "goals"))
	_, err := env.db.FinishTurn(ctx, TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnFailed, ErrorClass: ErrorClassProvider,
		Messages: []TurnMessage{{Role: "user", Content: "q"}},
	})
	require.NoError(t, err)

	require.ErrorIs(t, env.db.AppendTurnToolName(ctx, start.TurnID, "late"), ErrTurnNotRunning)
	turn, err := env.db.GetTurn(ctx, user, start.TurnID, contractStaleAfter)
	require.NoError(t, err)
	assert.Equal(t, []string{"goals"}, turn.ToolsUsed, "the finished turn's list is unchanged")
	require.ErrorIs(t, env.db.AppendTurnToolName(ctx, uuid.NewString(), "goals"), ErrTurnNotRunning)
	require.ErrorIs(t, env.db.AppendTurnToolName(ctx, "not-a-turn", "goals"), ErrTurnNotRunning)
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

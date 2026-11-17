package maurice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolTurnLLM requests goals and assists in one response, goals again in the
// next, then answers.
func toolTurnLLM() *mockLLMClient {
	twoCalls := toolCallResponse("goals", "c1")
	twoCalls.ToolCalls = append(twoCalls.ToolCalls, toolCallResponse("assists", "c2").ToolCalls...)
	return &mockLLMClient{responses: []*llm.Response{twoCalls, toolCallResponse("goals", "c3"), {Content: "done"}}}
}

// Each tool shows on the running turn as its call starts, in call order with
// duplicates, read back through GetTurn as a poll would.
func TestChat_RunningTurnListsToolsAsTheyStart(t *testing.T) {
	db := newTestDB(t)
	mcpMock := newMockMCP()
	var seen []*Turn
	mcpMock.onCall = func(string) {
		turn, err := db.GetTurn(context.Background(), testUser, db.currentTurnID(), time.Hour)
		require.NoError(t, err)
		seen = append(seen, turn)
	}
	svc := NewService(toolTurnLLM(), mcpMock, db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("goals and assists?"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	require.Len(t, seen, 3)
	want := [][]string{{"goals"}, {"goals", "assists"}, {"goals", "assists", "goals"}}
	for i, turn := range seen {
		assert.Equal(t, TurnRunning, turn.Status, "call %d", i)
		assert.Equal(t, want[i], turn.ToolsUsed, "call %d", i)
	}
	assert.Equal(t, []string{"goals", "assists", "goals"}, resp.ToolsUsed)
}

// Recording a starting tool only feeds the progress display: when the write
// fails, the tool still runs and the turn succeeds.
func TestChat_ToolNameWriteFailureKeepsTurn(t *testing.T) {
	db := newTestDB(t)
	db.appendToolErr = errors.New("database unavailable")
	mcpMock := newMockMCP()
	var called []string
	mcpMock.onCall = func(name string) { called = append(called, name) }
	svc := NewService(toolTurnLLM(), mcpMock, db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("goals and assists?"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, []string{"goals", "assists", "goals"}, called)
	assert.Equal(t, "done", resp.Content)
	assert.Equal(t, []string{"goals", "assists", "goals"}, resp.ToolsUsed)
}

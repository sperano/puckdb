// Package maurice orchestrates the Maurice AI chat: Service runs the
// prompt/tool-call loop (internal/llm for completions, internal/mcp for
// tool discovery and calls) and persists conversations, turns and messages
// through the DB interface, implemented by a SQLite store (the REPL,
// single local user) and a PostgreSQL store (the API, one user per login
// provider identity and API instance via internal/appuser). LLM-call and
// tool-call usage is persisted only by the PostgreSQL store; SQLite keeps
// conversations, turns and messages alone. RunREPL (repl*.go) is the
// `maurice` CLI command's REPL loop, its streams injectable through
// REPLConfig.
package maurice

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolRound returns an assistant message requesting n tool calls and their
// results, with call IDs prefix-1 … prefix-n.
func toolRound(prefix string, n int) []TurnMessage {
	calls := make([]llm.ToolCall, n)
	msgs := make([]TurnMessage, 0, n+1)
	for i := range n {
		calls[i] = llm.ToolCall{ID: fmt.Sprintf("%s-%d", prefix, i+1), Type: "function", Function: llm.ToolCallFunction{Name: "pg_read_query", Arguments: "{}"}}
	}
	msgs = append(msgs, TurnMessage{Role: "assistant", ToolCalls: calls})
	for _, c := range calls {
		msgs = append(msgs, TurnMessage{Role: "tool", Content: "rows " + c.ID, ToolCallID: c.ID})
	}
	return msgs
}

// Regression: the history cap fell inside a turn's tool results, the replay
// started on the next assistant tool round, and Anthropic refused every later
// prompt of the conversation. The replay now starts at the next turn's prompt.
func TestChat_AnthropicAcceptsHistoryCapInsideToolResults(t *testing.T) {
	server := httptest.NewServer(anthropicRules(t))
	t.Cleanup(server.Close)
	db := newTestDB(t)
	// q0, assistant(4 calls), 4 results, assistant(1 call), 1 result, a0, q1, a1.
	seed := []TurnMessage{{Role: "user", Content: "q0"}}
	seed = append(seed, toolRound("first", 4)...)
	seed = append(seed, toolRound("second", 1)...)
	seed = append(seed, TurnMessage{Role: "assistant", Content: "a0"}, TurnMessage{Role: "user", Content: "q1"}, TurnMessage{Role: "assistant", Content: "a1"})
	convID := seedConversation(t, db, seed...)
	// 13 messages with the prompt; keeping 10 drops q0, the 4-call assistant
	// and two of its results.
	const maxHistory = 10

	client := llm.NewAnthropicClient(server.URL, "test-key", "test-model")
	cfg := testConfig
	cfg.MaxHistory = maxHistory
	svc := NewService(client, newMockMCP(), db, cfg)
	resp, err := svc.Chat(context.Background(), askIn(convID, "q2"))
	require.NoError(t, svc.Close())

	require.NoError(t, err)
	assert.Equal(t, "a1", resp.Content)
}

// The same shape through the mock client: the replay is exactly the last
// whole turn and the prompt.
func TestLoadHistory_CapInsideToolResultsStartsAtNextTurn(t *testing.T) {
	db := newTestDB(t)
	seed := []TurnMessage{{Role: "user", Content: "q0"}}
	seed = append(seed, toolRound("first", 4)...)
	seed = append(seed, toolRound("second", 1)...)
	seed = append(seed, TurnMessage{Role: "assistant", Content: "a0"}, TurnMessage{Role: "user", Content: "q1"}, TurnMessage{Role: "assistant", Content: "a1"})
	convID := seedConversation(t, db, seed...)
	const maxHistory = 10

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "a2"}}}
	cfg := testConfig
	cfg.MaxHistory = maxHistory
	svc := NewService(llmMock, newMockMCP(), db, cfg)
	_, err := svc.Chat(context.Background(), askIn(convID, "q2"))
	require.NoError(t, err)

	sent := llmMock.requests[0].Messages
	require.Len(t, sent, 4)
	assert.Equal(t, "system", sent[0].Role)
	assert.Equal(t, []llm.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
	}, sent[1:])
}

// Whatever the cap, the kept history starts on a user message, is the
// newest whole turns that fit, and ends with the prompt, alone when no whole
// turn fits.
func TestCapHistory_StartsOnUserAtEveryCap(t *testing.T) {
	t.Parallel()
	var seed []TurnMessage
	seed = append(seed, TurnMessage{Role: "user", Content: "q0"})
	seed = append(seed, toolRound("first", 3)...)
	seed = append(seed, TurnMessage{Role: "assistant", Content: "a0"}, TurnMessage{Role: "user", Content: "q1"})
	seed = append(seed, toolRound("second", 2)...)
	seed = append(seed, TurnMessage{Role: "assistant", Content: "a1"})
	msgs := make([]*Message, 0, len(seed)+1)
	for _, m := range seed {
		msgs = append(msgs, &Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ToolCalls: m.ToolCalls})
	}
	prompt := &Message{Role: "user", Content: "q2"}
	msgs = append(msgs, prompt)
	// Turn starts: q0 at 0, q1 at 6, the prompt at 11 (12 messages).
	const q1Index, promptIndex = 6, 11

	for maxHistory := 1; maxHistory <= len(msgs)+1; maxHistory++ {
		got := capHistory(slices.Clone(msgs), maxHistory)
		var want []*Message
		switch {
		case maxHistory >= len(msgs):
			want = msgs
		case maxHistory >= len(msgs)-q1Index:
			want = msgs[q1Index:]
		default:
			want = msgs[promptIndex:]
		}
		assert.Equal(t, want, got, "maxHistory=%d", maxHistory)
		assert.Equal(t, "user", got[0].Role, "maxHistory=%d", maxHistory)
		assert.Same(t, prompt, got[len(got)-1], "maxHistory=%d", maxHistory)
	}
}

package maurice

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/mcp"
	"github.com/stretchr/testify/require"
)

// Test doubles shared by the service tests.

// --- Mock LLM Client ---

// mockLLMClient is safe for concurrent Complete calls, which matters because
// background title generation calls Complete from a detached goroutine while
// the test reads the recorded state. responses/errors are set once at
// construction and read without the lock.
type mockLLMClient struct {
	mu        sync.Mutex
	responses []*llm.Response
	errors    []error
	calls     int
	requests  []*llm.Request
}

func (m *mockLLMClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	idx := m.calls
	m.calls++
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &llm.Response{Content: "default"}, nil
}

func (m *mockLLMClient) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// --- Test DB: the real SQLite store with fault injection and capture ---

// testDB wraps the in-memory SQLite store, so turns, idempotency and owner
// scoping behave as in production. It injects errors per method and captures
// every TurnRecord and TitleRecord the service commits (SQLite does not store
// LLM calls itself).
type testDB struct {
	DB
	mu            sync.Mutex
	beginErr      error
	finishErrOnce error
	// finishBlockOnce makes the next FinishTurn wait for its context to end,
	// like a commit stuck on a slow database.
	finishBlockOnce    bool
	getMessagesErr     error
	getConversationErr error
	listErr            error
	titleErr           error
	finished           []TurnRecord
	titles             []TitleRecord
}

func newTestDB(t *testing.T) *testDB {
	return &testDB{DB: openTestDB(t)}
}

func (d *testDB) BeginTurn(ctx context.Context, params BeginTurnParams) (*TurnStart, error) {
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	return d.DB.BeginTurn(ctx, params)
}

func (d *testDB) FinishTurn(ctx context.Context, record TurnRecord) ([]string, error) {
	d.mu.Lock()
	err, block := d.finishErrOnce, d.finishBlockOnce
	d.finishErrOnce, d.finishBlockOnce = nil, false
	d.finished = append(d.finished, record)
	d.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return d.DB.FinishTurn(ctx, record)
}

func (d *testDB) GetMessages(ctx context.Context, userID, conversationID string) ([]*Message, error) {
	if d.getMessagesErr != nil {
		return nil, d.getMessagesErr
	}
	return d.DB.GetMessages(ctx, userID, conversationID)
}

func (d *testDB) GetConversation(ctx context.Context, userID, id string) (*Conversation, error) {
	if d.getConversationErr != nil {
		return nil, d.getConversationErr
	}
	return d.DB.GetConversation(ctx, userID, id)
}

func (d *testDB) ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error) {
	if d.listErr != nil {
		return nil, d.listErr
	}
	return d.DB.ListConversations(ctx, userID, limit)
}

func (d *testDB) RecordTitle(ctx context.Context, record TitleRecord) error {
	d.mu.Lock()
	d.titles = append(d.titles, record)
	d.mu.Unlock()
	if d.titleErr != nil {
		return d.titleErr
	}
	return d.DB.RecordTitle(ctx, record)
}

func (d *testDB) finishedTurns() []TurnRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]TurnRecord(nil), d.finished...)
}

func (d *testDB) titleRecords() []TitleRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]TitleRecord(nil), d.titles...)
}

// seedConversation commits one succeeded turn holding msgs and returns the
// conversation ID. It writes to the underlying store, so the seed turn is not
// among a testDB's captured records.
func seedConversation(t *testing.T, db DB, msgs ...TurnMessage) string {
	t.Helper()
	if td, ok := db.(*testDB); ok {
		db = td.DB
	}
	start := beginTurn(t, db, testUser, "", "seed")
	_, err := db.FinishTurn(context.Background(), TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded, Messages: msgs,
	})
	require.NoError(t, err)
	return start.ConversationID
}

// --- Mock MCP Client ---

type mockMCPClient struct {
	callResults  map[string]string
	callErrors   map[string]error
	isError      map[string]bool
	listTools    []mcpgo.Tool // returned by ListTools when err is nil
	listToolsErr error
}

func newMockMCP() *mockMCPClient {
	return &mockMCPClient{
		callResults: make(map[string]string),
		callErrors:  make(map[string]error),
		isError:     make(map[string]bool),
	}
}

func (m *mockMCPClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	if m.listToolsErr != nil {
		return nil, m.listToolsErr
	}
	return m.listTools, nil
}

func (m *mockMCPClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*mcp.ToolResult, error) {
	if err, ok := m.callErrors[name]; ok {
		return nil, err
	}
	content := "tool result"
	if c, ok := m.callResults[name]; ok {
		content = c
	}
	return &mcp.ToolResult{Content: content, IsError: m.isError[name]}, nil
}

func (m *mockMCPClient) Close() error { return nil }

func ask(message string) ChatRequest {
	return ChatRequest{UserID: testUser, Message: message}
}

func askIn(convID, message string) ChatRequest {
	return ChatRequest{UserID: testUser, ConversationID: &convID, Message: message}
}

// toolCallResponse builds a single-tool-call LLM response. Used by the
// max-rounds tests that need every round to keep requesting tools.
func toolCallResponse(toolName, callID string) *llm.Response {
	return &llm.Response{
		ToolCalls: []llm.ToolCall{{
			ID:       callID,
			Type:     "function",
			Function: llm.ToolCallFunction{Name: toolName, Arguments: "{}"},
		}},
	}
}

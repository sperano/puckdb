package maurice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock LLM Client ---

type mockLLMClient struct {
	responses []*llm.Response
	errors    []error
	calls     int
	requests  []*llm.Request
}

func (m *mockLLMClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	idx := m.calls
	m.calls++
	m.requests = append(m.requests, req)
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &llm.Response{Content: "default"}, nil
}

// --- Mock DB ---

type mockDB struct {
	conversations map[string]*Conversation
	messages      map[string][]*Message
	nextID        int
	createErr     error
}

func newMockDB() *mockDB {
	return &mockDB{
		conversations: make(map[string]*Conversation),
		messages:      make(map[string][]*Message),
	}
}

func (m *mockDB) nextUUID() string {
	m.nextID++
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", m.nextID)
}

func (m *mockDB) CreateConversation(ctx context.Context) (*Conversation, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	now := time.Now()
	conv := &Conversation{
		ID:        m.nextUUID(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.conversations[conv.ID] = conv
	return conv, nil
}

func (m *mockDB) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	conv, ok := m.conversations[id]
	if !ok {
		return nil, errors.New("conversation not found")
	}
	return conv, nil
}

func (m *mockDB) UpdateConversationTitle(ctx context.Context, id, title string) error {
	if conv, ok := m.conversations[id]; ok {
		conv.Title = &title
	}
	return nil
}

func (m *mockDB) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	var result []*Conversation
	for _, c := range m.conversations {
		result = append(result, c)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockDB) DeleteConversation(ctx context.Context, id string) error {
	delete(m.conversations, id)
	delete(m.messages, id)
	return nil
}

func (m *mockDB) CreateMessage(ctx context.Context, p CreateMessageParams) (*Message, error) {
	msg := &Message{
		ID:         m.nextUUID(),
		Role:       p.Role,
		Content:    p.Content,
		ToolCalls:  p.ToolCalls,
		ToolCallID: p.ToolCallID,
		CreatedAt:  time.Now(),
	}
	m.messages[p.ConversationID] = append(m.messages[p.ConversationID], msg)
	return msg, nil
}

func (m *mockDB) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) {
	return m.messages[conversationID], nil
}

// --- Mock MCP Client ---

type mockMCPClient struct {
	callResults map[string]string
	callErrors  map[string]error
}

func newMockMCP() *mockMCPClient {
	return &mockMCPClient{
		callResults: make(map[string]string),
		callErrors:  make(map[string]error),
	}
}

func (m *mockMCPClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	return nil, nil
}

func (m *mockMCPClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*mcp.ToolResult, error) {
	if err, ok := m.callErrors[name]; ok {
		return nil, err
	}
	content := "tool result"
	if c, ok := m.callResults[name]; ok {
		content = c
	}
	return &mcp.ToolResult{Content: content}, nil
}

func (m *mockMCPClient) Close() error { return nil }

// --- Tests ---

func TestChat_SimpleQA(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "Wayne Gretzky holds the record with 894 goals."},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most NHL goals?")

	require.NoError(t, err)
	assert.Equal(t, "Wayne Gretzky holds the record with 894 goals.", resp.Content)
	assert.NotEmpty(t, resp.ConversationID)
	assert.Empty(t, resp.ToolsUsed)
	// 1 main call + possibly 1 async title generation (instant mock races)
	assert.GreaterOrEqual(t, llmMock.calls, 1)

	// Verify system prompt was included in the first (main) request
	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	assert.Equal(t, "system", llmMock.requests[0].Messages[0].Role)
	assert.Contains(t, llmMock.requests[0].Messages[0].Content, "Maurice")
}

func TestChat_WithToolCalls(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{
				ToolCalls: []llm.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "pg_read_query",
						Arguments: `{"sql":"SELECT name FROM players WHERE goals > 800"}`,
					},
				}},
			},
			{Content: "Based on the data, Wayne Gretzky has the most goals."},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = `[{"name":"Wayne Gretzky","goals":894}]`

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most goals?")

	require.NoError(t, err)
	assert.Equal(t, "Based on the data, Wayne Gretzky has the most goals.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, resp.ToolsUsed)
	assert.Equal(t, 2, llmMock.calls)
}

func TestChat_ExistingConversation(t *testing.T) {
	db := newMockDB()

	// Pre-create a conversation
	conv, _ := db.CreateConversation(context.Background())
	convID := conv.ID

	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "Follow-up answer."},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), &convID, "Follow-up question")

	require.NoError(t, err)
	assert.Equal(t, convID, resp.ConversationID)
	assert.Equal(t, "Follow-up answer.", resp.Content)
}

func TestChat_LLMError(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		errors: []error{errors.New("model unavailable")},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	_, err := svc.Chat(context.Background(), nil, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "model unavailable")
}

func TestChat_ToolCallError(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{
				ToolCalls: []llm.ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: llm.ToolCallFunction{Name: "bad_tool", Arguments: "{}"},
				}},
			},
			{Content: "Sorry, I couldn't fetch that data."},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callErrors["bad_tool"] = errors.New("tool not found")

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), nil, "test")

	require.NoError(t, err)
	assert.Contains(t, resp.Content, "Sorry")
	assert.Equal(t, []string{"bad_tool"}, resp.ToolsUsed)
}

func TestGetConversation(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "Answer"},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)

	resp, err := svc.Chat(context.Background(), nil, "Hello")
	require.NoError(t, err)

	conv, msgs, err := svc.GetConversation(context.Background(), resp.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, resp.ConversationID, conv.ID)
	assert.Len(t, msgs, 2) // user + assistant
}

func TestListConversations(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "A1"},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	svc.Chat(context.Background(), nil, "Q1")

	convs, err := svc.ListConversations(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, convs, 1)
}

func TestDeleteConversation(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "ok"},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, _ := svc.Chat(context.Background(), nil, "test")

	err := svc.DeleteConversation(context.Background(), resp.ConversationID)
	require.NoError(t, err)

	_, _, err = svc.GetConversation(context.Background(), resp.ConversationID)
	require.Error(t, err)
}

func TestNewService_DefaultValues(t *testing.T) {
	svc := NewService(&mockLLMClient{}, newMockMCP(), newMockDB(), 0, 0).(*service)
	assert.Equal(t, DefaultMaxHistory, svc.maxHistory)
	assert.Equal(t, DefaultMaxTokens, svc.maxTokens)
}

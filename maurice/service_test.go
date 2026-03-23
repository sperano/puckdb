package maurice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/mcp"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock LLM Client ---

type mockLLMClient struct {
	responses []*llm.ChatCompletionResponse
	errors    []error
	calls     int
	requests  []*llm.ChatCompletionRequest
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, req *llm.ChatCompletionRequest) (*llm.ChatCompletionResponse, error) {
	idx := m.calls
	m.calls++
	m.requests = append(m.requests, req)
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &llm.ChatCompletionResponse{
		Choices: []llm.Choice{{Message: llm.Message{Content: "default"}}},
	}, nil
}

// --- Mock MCP Client ---

// --- Mock DB ---

type mockDB struct {
	conversations map[string]sqlcdb.MauriceConversation
	messages      map[string][]sqlcdb.MauriceMessage
	nextConvID    pgtype.UUID
	nextMsgID     pgtype.UUID
	createErr     error
	msgCount      int
}

func newMockDB() *mockDB {
	return &mockDB{
		conversations: make(map[string]sqlcdb.MauriceConversation),
		messages:      make(map[string][]sqlcdb.MauriceMessage),
		nextConvID:    testUUID("11111111-1111-1111-1111-111111111111"),
		nextMsgID:     testUUID("22222222-2222-2222-2222-222222222222"),
	}
}

func testUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	u.Scan(s)
	return u
}

func testTime() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now(), Valid: true}
}

func (m *mockDB) CreateConversation(ctx context.Context, title pgtype.Text) (sqlcdb.MauriceConversation, error) {
	if m.createErr != nil {
		return sqlcdb.MauriceConversation{}, m.createErr
	}
	conv := sqlcdb.MauriceConversation{
		ID:        m.nextConvID,
		Title:     title,
		CreatedAt: testTime(),
		UpdatedAt: testTime(),
	}
	m.conversations[uuidToString(conv.ID)] = conv
	return conv, nil
}

func (m *mockDB) GetConversation(ctx context.Context, id pgtype.UUID) (sqlcdb.MauriceConversation, error) {
	key := uuidToString(id)
	conv, ok := m.conversations[key]
	if !ok {
		return sqlcdb.MauriceConversation{}, errors.New("conversation not found")
	}
	return conv, nil
}

func (m *mockDB) UpdateConversationTitle(ctx context.Context, arg sqlcdb.UpdateConversationTitleParams) error {
	key := uuidToString(arg.ID)
	if conv, ok := m.conversations[key]; ok {
		conv.Title = arg.Title
		m.conversations[key] = conv
	}
	return nil
}

func (m *mockDB) ListConversations(ctx context.Context, limit int32) ([]sqlcdb.MauriceConversation, error) {
	var result []sqlcdb.MauriceConversation
	for _, c := range m.conversations {
		result = append(result, c)
		if int32(len(result)) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockDB) DeleteConversation(ctx context.Context, id pgtype.UUID) error {
	key := uuidToString(id)
	delete(m.conversations, key)
	delete(m.messages, key)
	return nil
}

func (m *mockDB) CreateMessage(ctx context.Context, arg sqlcdb.CreateMessageParams) (sqlcdb.MauriceMessage, error) {
	m.msgCount++
	msg := sqlcdb.MauriceMessage{
		ID:             m.nextMsgID,
		ConversationID: arg.ConversationID,
		Role:           arg.Role,
		Content:        arg.Content,
		ToolCalls:      arg.ToolCalls,
		ToolCallID:     arg.ToolCallID,
		CreatedAt:      testTime(),
	}
	key := uuidToString(arg.ConversationID)
	m.messages[key] = append(m.messages[key], msg)
	return msg, nil
}

func (m *mockDB) GetMessagesByConversation(ctx context.Context, conversationID pgtype.UUID) ([]sqlcdb.MauriceMessage, error) {
	key := uuidToString(conversationID)
	return m.messages[key], nil
}

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
		responses: []*llm.ChatCompletionResponse{
			{Choices: []llm.Choice{{Message: llm.Message{Content: "Wayne Gretzky holds the record with 894 goals."}}}},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most NHL goals?")

	require.NoError(t, err)
	assert.Equal(t, "Wayne Gretzky holds the record with 894 goals.", resp.Content)
	assert.NotEmpty(t, resp.ConversationID)
	assert.Empty(t, resp.ToolsUsed)
	assert.Equal(t, 1, llmMock.calls) // 1 main call; title gen is async

	// Verify system prompt was included
	require.Len(t, llmMock.requests, 1)
	assert.Equal(t, "system", llmMock.requests[0].Messages[0].Role)
	assert.Contains(t, llmMock.requests[0].Messages[0].Content, "Maurice")
}

func TestChat_WithToolCalls(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.ChatCompletionResponse{
			// First response: tool call
			{Choices: []llm.Choice{{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: llm.ToolCallFunction{
							Name:      "pg_read_query",
							Arguments: `{"sql":"SELECT name FROM players WHERE goals > 800"}`,
						},
					}},
				},
			}}},
			// Second response: final answer
			{Choices: []llm.Choice{{
				Message: llm.Message{Content: "Based on the data, Wayne Gretzky has the most goals."},
			}}},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = `[{"name":"Wayne Gretzky","goals":894}]`

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most goals?")

	require.NoError(t, err)
	assert.Equal(t, "Based on the data, Wayne Gretzky has the most goals.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, resp.ToolsUsed)
	assert.Equal(t, 2, llmMock.calls) // tool call + final
}

func TestChat_ExistingConversation(t *testing.T) {
	db := newMockDB()

	// Pre-create a conversation
	conv, _ := db.CreateConversation(context.Background(), pgtype.Text{String: "Test", Valid: true})
	convID := uuidToString(conv.ID)

	llmMock := &mockLLMClient{
		responses: []*llm.ChatCompletionResponse{
			{Choices: []llm.Choice{{Message: llm.Message{Content: "Follow-up answer."}}}},
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
		responses: []*llm.ChatCompletionResponse{
			// Tool call
			{Choices: []llm.Choice{{
				Message: llm.Message{
					ToolCalls: []llm.ToolCall{{
						ID:       "call_1",
						Type:     "function",
						Function: llm.ToolCallFunction{Name: "bad_tool", Arguments: "{}"},
					}},
				},
			}}},
			// Final answer after tool error
			{Choices: []llm.Choice{{Message: llm.Message{Content: "Sorry, I couldn't fetch that data."}}}},
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

func TestChat_InvalidConversationID(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)
	badID := "not-a-uuid"
	_, err := svc.Chat(context.Background(), &badID, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestGetConversation(t *testing.T) {
	db := newMockDB()
	llmMock := &mockLLMClient{
		responses: []*llm.ChatCompletionResponse{
			{Choices: []llm.Choice{{Message: llm.Message{Content: "Answer"}}}},
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
		responses: []*llm.ChatCompletionResponse{
			{Choices: []llm.Choice{{Message: llm.Message{Content: "A1"}}}},
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
		responses: []*llm.ChatCompletionResponse{
			{Choices: []llm.Choice{{Message: llm.Message{Content: "ok"}}}},
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

func TestDeleteConversation_InvalidID(t *testing.T) {
	db := newMockDB()
	mcpMock := newMockMCP()
	svc := NewService(&mockLLMClient{}, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens)

	err := svc.DeleteConversation(context.Background(), "bad-id")
	require.Error(t, err)
}

func TestUUIDToString(t *testing.T) {
	u := testUUID("12345678-1234-1234-1234-123456789abc")
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", uuidToString(u))
}

func TestUUIDToString_Invalid(t *testing.T) {
	assert.Empty(t, uuidToString(pgtype.UUID{}))
}

func TestNewService_DefaultValues(t *testing.T) {
	svc := NewService(&mockLLMClient{}, newMockMCP(), newMockDB(), 0, 0).(*service)
	assert.Equal(t, DefaultMaxHistory, svc.maxHistory)
	assert.Equal(t, DefaultMaxTokens, svc.maxTokens)
}

package maurice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// --- Mock DB ---

type mockDB struct {
	mu            sync.Mutex // guards all fields; background title generation writes concurrently
	conversations map[string]*Conversation
	messages      map[string][]*Message
	nextID        int
	createErr     error // injected on CreateConversation
	// Each of these err fields is checked on its corresponding method.
	// The pre-existing createErr stays for backwards compatibility with
	// the older tests that set it directly.
	createMessageErr     error
	getMessagesErr       error
	getConversationErr   error
	listConversationsErr error
	updateTitleErr       error
}

func newMockDB() *mockDB {
	return &mockDB{
		conversations: make(map[string]*Conversation),
		messages:      make(map[string][]*Message),
	}
}

// nextUUID must be called while holding m.mu.
func (m *mockDB) nextUUID() string {
	m.nextID++
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", m.nextID)
}

func (m *mockDB) CreateConversation(ctx context.Context) (*Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getConversationErr != nil {
		return nil, m.getConversationErr
	}
	conv, ok := m.conversations[id]
	if !ok {
		return nil, errors.New("conversation not found")
	}
	return conv, nil
}

func (m *mockDB) UpdateConversationTitle(ctx context.Context, id, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateTitleErr != nil {
		return m.updateTitleErr
	}
	if conv, ok := m.conversations[id]; ok {
		conv.Title = &title
	}
	return nil
}

func (m *mockDB) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listConversationsErr != nil {
		return nil, m.listConversationsErr
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conversations, id)
	delete(m.messages, id)
	return nil
}

// CreateMessages mimics the real backends' atomic contract: if createMessageErr
// is armed the whole batch fails and nothing is appended; otherwise every
// message is appended in order.
func (m *mockDB) CreateMessages(ctx context.Context, params []CreateMessageParams) ([]*Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createMessageErr != nil {
		return nil, m.createMessageErr
	}
	created := make([]*Message, len(params))
	for i, p := range params {
		created[i] = &Message{
			ID:         m.nextUUID(),
			Role:       p.Role,
			Content:    p.Content,
			ToolCalls:  p.ToolCalls,
			ToolCallID: p.ToolCallID,
			CreatedAt:  time.Now(),
		}
	}
	for i, p := range params {
		m.messages[p.ConversationID] = append(m.messages[p.ConversationID], created[i])
	}
	return created, nil
}

func (m *mockDB) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getMessagesErr != nil {
		return nil, m.getMessagesErr
	}
	return m.messages[conversationID], nil
}

// --- Mock MCP Client ---

type mockMCPClient struct {
	callResults  map[string]string
	callErrors   map[string]error
	listTools    []mcpgo.Tool // returned by ListTools when err is nil
	listToolsErr error
}

func newMockMCP() *mockMCPClient {
	return &mockMCPClient{
		callResults: make(map[string]string),
		callErrors:  make(map[string]error),
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most NHL goals?")

	require.NoError(t, err)
	// Await the detached title generation so recorded LLM state is stable.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Wayne Gretzky holds the record with 894 goals.", resp.Content)
	assert.NotEmpty(t, resp.ConversationID)
	assert.Empty(t, resp.ToolsUsed)
	// 1 main call + 1 title generation (awaited via Close).
	assert.Equal(t, 2, llmMock.calls)

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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), nil, "Who has the most goals?")

	require.NoError(t, err)
	// Await the detached title generation so the call count is deterministic.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Based on the data, Wayne Gretzky has the most goals.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, resp.ToolsUsed)
	// 2 tool-loop rounds + 1 background title generation.
	assert.Equal(t, 3, llmMock.calls)
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)

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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
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

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, _ := svc.Chat(context.Background(), nil, "test")

	err := svc.DeleteConversation(context.Background(), resp.ConversationID)
	require.NoError(t, err)

	_, _, err = svc.GetConversation(context.Background(), resp.ConversationID)
	require.Error(t, err)
}

func TestNewService_DefaultValues(t *testing.T) {
	svc := NewService(&mockLLMClient{}, newMockMCP(), newMockDB(), 0, 0, 0).(*service)
	assert.Equal(t, DefaultMaxHistory, svc.maxHistory)
	assert.Equal(t, DefaultMaxTokens, svc.maxTokens)
}

// --- Error-path coverage for Chat (lines 75% -> closer to 100%) ---

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

func TestChat_ResolveConversationError(t *testing.T) {
	db := newMockDB()
	db.createErr = errors.New("db down")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), nil, "hi")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "create conversation")
}

func TestChat_StoreUserMessageError(t *testing.T) {
	db := newMockDB()
	db.createMessageErr = errors.New("disk full")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), nil, "hi")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "persist turn")
}

func TestChat_LoadHistoryError(t *testing.T) {
	db := newMockDB()
	// Pre-create the conversation so ResolveConversation succeeds; then
	// arm the error so the LATER GetMessages call (inside loadHistory)
	// trips. CreateMessage itself doesn't fail because we don't arm
	// createMessageErr.
	conv, _ := db.CreateConversation(context.Background())
	db.getMessagesErr = errors.New("redis lost")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), &conv.ID, "hi")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "load history")
}

// When MCP ListTools errors, Chat must continue without tools (logs a
// warning, sets llmTools to nil) — pin that behavior so a refactor
// can't accidentally turn it into a hard failure.
func TestChat_ToolCacheListError_ContinuesWithoutTools(t *testing.T) {
	db := newMockDB()
	mcpMock := newMockMCP()
	mcpMock.listToolsErr = errors.New("mcp unreachable")
	llmMock := &mockLLMClient{
		responses: []*llm.Response{{Content: "answer"}},
	}

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), nil, "hi")

	require.NoError(t, err)
	// Await the detached title generation so recorded requests are stable.
	require.NoError(t, svc.Close())

	assert.Equal(t, "answer", resp.Content)
	// Tools must be nil on the request (omitted, not empty slice).
	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	assert.Nil(t, llmMock.requests[0].Tools)
}

// Forced final-completion: if the LLM keeps requesting tools for all
// maxToolRounds without ever producing a text answer, Chat must make
// one final call without tools and use that text.
func TestChat_MaxToolRoundsExhausted_ForcesFinalCompletion(t *testing.T) {
	db := newMockDB()
	const maxRounds = 3
	// First maxRounds calls are tool requests; then forced-final returns text.
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("pg_read_query", "c1"),
			toolCallResponse("pg_read_query", "c2"),
			toolCallResponse("pg_read_query", "c3"),
			{Content: "Forced final answer."},
		},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, maxRounds)
	resp, err := svc.Chat(context.Background(), nil, "hi")

	require.NoError(t, err)
	// Await the detached title generation so recorded requests are stable.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Forced final answer.", resp.Content)
	// maxRounds tool-call rounds + 1 forced final = 4 calls.
	assert.GreaterOrEqual(t, llmMock.calls, maxRounds+1)
	// The forced-final request must NOT carry Tools (the whole point of
	// the forced-final branch is to coerce a text reply).
	finalReq := llmMock.requests[maxRounds]
	assert.Nil(t, finalReq.Tools, "forced-final request must omit Tools")
}

func TestChat_MaxToolRoundsExhausted_ForcedFinalLLMError(t *testing.T) {
	db := newMockDB()
	const maxRounds = 2
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("pg_read_query", "c1"),
			toolCallResponse("pg_read_query", "c2"),
			nil, // final call, errors below
		},
		errors: []error{nil, nil, errors.New("model overloaded")},
	}

	svc := NewService(llmMock, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, maxRounds)
	resp, err := svc.Chat(context.Background(), nil, "hi")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "forced final")
}

// --- GetConversation: GetMessages error branch ---

func TestGetConversation_GetMessagesError(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())
	db.getMessagesErr = errors.New("redis down")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	gotConv, msgs, err := svc.GetConversation(context.Background(), conv.ID)

	require.Error(t, err)
	assert.Nil(t, gotConv)
	assert.Nil(t, msgs)
	assert.Contains(t, err.Error(), "get messages")
}

// --- ListConversations: limit fallback to maxHistory + DB error ---

func TestListConversations_ZeroLimitFallsBackToMaxHistory(t *testing.T) {
	db := newMockDB()
	const customMaxHistory = 7
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, customMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)

	// Seed >customMaxHistory conversations so the cap matters.
	for range customMaxHistory + 5 {
		_, _ = db.CreateConversation(context.Background())
	}

	got, err := svc.ListConversations(context.Background(), 0)
	require.NoError(t, err)
	assert.Len(t, got, customMaxHistory, "limit=0 must fall back to maxHistory")
}

func TestListConversations_DBError(t *testing.T) {
	db := newMockDB()
	db.listConversationsErr = errors.New("query failed")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	got, err := svc.ListConversations(context.Background(), 5)

	require.Error(t, err)
	assert.Nil(t, got)
}

// --- loadHistory: truncation and leading-tool-skip branches ---

func TestLoadHistory_TruncatesToMaxHistory(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	// Seed many messages; only the last maxHistory should survive into
	// the LLM request.
	const seeded = 30
	const maxHistory = 5
	seed := make([]CreateMessageParams, seeded)
	for i := range seeded {
		seed[i] = CreateMessageParams{ConversationID: conv.ID, Role: "user", Content: fmt.Sprintf("m%d", i)}
	}
	_, err := db.CreateMessages(context.Background(), seed)
	require.NoError(t, err)

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "ok"}}}
	svc := NewService(llmMock, newMockMCP(), db, maxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	_, err = svc.Chat(context.Background(), &conv.ID, "follow-up")
	require.NoError(t, err)

	// Request[0] = system prompt, then up to maxHistory previous + the
	// stored user message of "follow-up". Cap from the maxHistory slice.
	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	first := llmMock.requests[0]
	// system + at most maxHistory historical + 0 tool-id pairs.
	assert.LessOrEqual(t, len(first.Messages), 1+maxHistory)
}

// loadHistory drops leading "tool" messages whose paired assistant
// tool_use block was sliced off by the maxHistory cap. Pin that
// invariant with a synthetic conversation: pad with user messages,
// then end with a leading tool-result that gets stranded.
func TestLoadHistory_SkipsLeadingToolAfterTruncation(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	const maxHistory = 3
	// Seed [user, user, user, tool-result, user-2nd] — slicing to last
	// maxHistory=3 yields [user, tool-result, user-2nd]; loadHistory
	// then drops nothing (the LEADING entry is a "user", not "tool").
	// To trigger the skip, seed [user, user, tool-result, user-final]:
	// last 3 = [tool-result, user, user-final] — wait, that doesn't
	// orphan either. The skip triggers when the SLICED window starts
	// with a tool message. Construct: 4 messages where last 3 starts
	// with tool.
	roles := []string{"user", "assistant", "tool", "user"} // oldest -> newest
	seed := make([]CreateMessageParams, len(roles))
	for i, r := range roles {
		seed[i] = CreateMessageParams{ConversationID: conv.ID, Role: r, Content: r + "-content"}
	}
	_, err := db.CreateMessages(context.Background(), seed)
	require.NoError(t, err)

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "ok"}}}
	svc := NewService(llmMock, newMockMCP(), db, maxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	_, err = svc.Chat(context.Background(), &conv.ID, "follow-up")
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	// First message is always "system"; check no "tool" appears as the
	// second message (which would mean the leading-tool-skip didn't
	// fire — the tool result would have been preserved without its
	// assistant precursor).
	first := llmMock.requests[0]
	require.GreaterOrEqual(t, len(first.Messages), 2)
	assert.Equal(t, "system", first.Messages[0].Role)
	assert.NotEqual(t, "tool", first.Messages[1].Role, "leading tool message must be skipped after truncation")
}

// --- generateTitle: empty response and DB error branches ---

// generateTitle is fired in a goroutine from Chat for new conversations.
// To exercise the error/empty branches deterministically, call it
// directly on the service struct.

func TestGenerateTitle_EmptyResponse(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: ""}}}
	svc := NewService(llmMock, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds).(*service)
	svc.generateTitle(context.Background(), conv.ID, "user message", "assistant message")

	got, _ := db.GetConversation(context.Background(), conv.ID)
	assert.Nil(t, got.Title, "empty title must not overwrite the conversation")
}

func TestGenerateTitle_LLMError(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	llmMock := &mockLLMClient{errors: []error{errors.New("llm down")}}
	svc := NewService(llmMock, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds).(*service)
	svc.generateTitle(context.Background(), conv.ID, "u", "a")

	got, _ := db.GetConversation(context.Background(), conv.ID)
	assert.Nil(t, got.Title, "LLM error must leave title unset")
}

func TestGenerateTitle_DBUpdateError(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())
	db.updateTitleErr = errors.New("write conflict")

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "Hockey Talk"}}}
	svc := NewService(llmMock, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds).(*service)
	// Doesn't return an error — the function logs and swallows. The
	// branch is exercised; the test simply confirms no panic and no
	// cross-talk to other state.
	svc.generateTitle(context.Background(), conv.ID, "u", "a")

	got, _ := db.GetConversation(context.Background(), conv.ID)
	assert.Nil(t, got.Title, "DB error must not leave the title in the local mock")
}

// --- logLLMResponse: covers all conditional branches ---

func TestLogLLMResponse_NilUsageAndEmptyFinishReason(t *testing.T) {
	// Function only writes to a logger — there's nothing to assert except
	// "no panic". The branches matter because each one toggles a different
	// key/value pair into the log event.
	logLLMResponse("conv-1", 0, &llm.Response{Model: "llama"})
	logLLMResponse("conv-1", 0, &llm.Response{
		Model:        "llama",
		FinishReason: "stop",
	})
	logLLMResponse("conv-1", 0, &llm.Response{
		Model: "llama",
		Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	logLLMResponse("conv-1", 0, &llm.Response{
		Model:        "llama",
		FinishReason: "length",
		Usage:        &llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	})
}

// --- truncateLog: pure helper; pin both branches ---

func TestTruncateLog(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than max", "hi", 10, "hi"},
		{"exactly at max", "abcde", 5, "abcde"},
		{"longer than max", "abcdefghij", 5, "abcde..."},
		{"empty", "", 5, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, truncateLog(tc.in, tc.max))
		})
	}
}

// --- B17: atomic turn persistence ---

// A crash mid-turn (here, the LLM failing on the second round after a tool
// call) must leave NO persisted messages for the turn — not the user message,
// not the partial tool result — so loadHistory on resume is not poisoned.
func TestChat_MidTurnFailure_PersistsNothing(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	llmMock := &mockLLMClient{
		responses: []*llm.Response{toolCallResponse("pg_read_query", "c1"), nil},
		errors:    []error{nil, errors.New("model crash mid-turn")},
	}
	mcpMock := newMockMCP()

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	resp, err := svc.Chat(context.Background(), &conv.ID, "question")
	require.NoError(t, svc.Close())

	require.Error(t, err)
	assert.Nil(t, resp)

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs, "mid-turn failure must not persist a partial turn")
}

// A successful tool-using turn persists the whole turn — user, assistant
// tool_calls, tool result, final assistant — in order, in one post-loop batch.
func TestChat_SuccessfulTurn_PersistsWholeTurnInOrder(t *testing.T) {
	db := newMockDB()
	conv, _ := db.CreateConversation(context.Background())

	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("pg_read_query", "c1"),
			{Content: "final"},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = "rows"

	svc := NewService(llmMock, mcpMock, db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)
	_, err := svc.Chat(context.Background(), &conv.ID, "q")
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	msgs, err := db.GetMessages(context.Background(), conv.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
	assert.Equal(t, "assistant", msgs[3].Role)
	assert.Equal(t, "final", msgs[3].Content)
}

// --- T1-H: detached, bounded, awaited title generation ---

// gatedLLM lets a test hold the title-generation Complete call until the test
// releases it, and records the context error observed at that moment. Requests
// with MaxTokens == titleGenMaxTokens are treated as the title call.
type gatedLLM struct {
	proceed     chan struct{}
	observedErr chan error
}

const titleGenMaxTokens = 20

func (g *gatedLLM) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req.MaxTokens != titleGenMaxTokens {
		return &llm.Response{Content: "answer"}, nil
	}
	<-g.proceed
	err := ctx.Err()
	g.observedErr <- err
	if err != nil {
		return nil, err
	}
	return &llm.Response{Content: "My Title"}, nil
}

// Title generation must survive cancellation of the request context (it derives
// from context.WithoutCancel), and Close must await it.
func TestChat_TitleGeneration_DetachedFromRequestContext(t *testing.T) {
	db := newMockDB()
	g := &gatedLLM{proceed: make(chan struct{}), observedErr: make(chan error, 1)}
	svc := NewService(g, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)

	ctx, cancel := context.WithCancel(context.Background())
	resp, err := svc.Chat(ctx, nil, "hi") // returns immediately; title goroutine now blocked on proceed
	require.NoError(t, err)

	// Cancel the request context, then let the title call observe its own
	// context. WithoutCancel means the title context is still live.
	cancel()
	close(g.proceed)
	gotErr := <-g.observedErr // read before Close so shutdown can't be the cause
	assert.NoError(t, gotErr, "title context must survive request-context cancellation")

	require.NoError(t, svc.Close()) // awaits the in-flight title goroutine

	convDetail, err := db.GetConversation(context.Background(), resp.ConversationID)
	require.NoError(t, err)
	require.NotNil(t, convDetail.Title)
	assert.Equal(t, "My Title", *convDetail.Title)
}

// Many concurrent Chats must not race or deadlock; title generation is bounded
// and Close drains cleanly. Run with -race to exercise the concurrency.
func TestChat_ConcurrentChats_BoundedAndClosable(t *testing.T) {
	db := newMockDB()
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, DefaultMaxHistory, DefaultMaxTokens, DefaultMaxToolRounds)

	const n = 25
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, err := svc.Chat(context.Background(), nil, "q")
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	require.NoError(t, svc.Close())

	convs, err := db.ListConversations(context.Background(), n*2)
	require.NoError(t, err)
	assert.Len(t, convs, n)
}

package maurice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testUser owns the conversations of the service tests.
const testUser = "user-a"

// testConfig leaves every limit at its default.
var testConfig = ServiceConfig{Provider: "anthropic", Model: "test-model"}

// --- Tests ---

func TestChat_SimpleQA(t *testing.T) {
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "Wayne Gretzky holds the record with 894 goals."},
		},
	}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)
	resp, err := svc.Chat(context.Background(), ask("Who has the most NHL goals?"))

	require.NoError(t, err)
	// Await the detached title generation so recorded LLM state is stable.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Wayne Gretzky holds the record with 894 goals.", resp.Content)
	assert.NotEmpty(t, resp.ConversationID)
	assert.NotEmpty(t, resp.MessageID)
	assert.Empty(t, resp.ToolsUsed)
	// 1 main call + 1 title generation (awaited via Close).
	assert.Equal(t, 2, llmMock.callCount())

	// Verify system prompt was included in the first (main) request
	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	assert.Equal(t, "system", llmMock.requests[0].Messages[0].Role)
	assert.Contains(t, llmMock.requests[0].Messages[0].Content, "Maurice")
}

func TestChat_WithToolCalls(t *testing.T) {
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

	svc := NewService(llmMock, mcpMock, newTestDB(t), testConfig)
	resp, err := svc.Chat(context.Background(), ask("Who has the most goals?"))

	require.NoError(t, err)
	// Await the detached title generation so the call count is deterministic.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Based on the data, Wayne Gretzky has the most goals.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, resp.ToolsUsed)
	// 2 tool-loop rounds + 1 background title generation.
	assert.Equal(t, 3, llmMock.callCount())
}

func TestChat_ExistingConversation(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q"}, TurnMessage{Role: "assistant", Content: "a"})
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "Follow-up answer."}}}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), askIn(convID, "Follow-up question"))

	require.NoError(t, err)
	require.NoError(t, svc.Close())
	assert.Equal(t, convID, resp.ConversationID)
	assert.Equal(t, "Follow-up answer.", resp.Content)
	assert.Equal(t, 1, llmMock.callCount(), "no title for an existing conversation")
}

func TestChat_LLMError(t *testing.T) {
	llmMock := &mockLLMClient{errors: []error{errors.New("model unavailable")}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)
	_, err := svc.Chat(context.Background(), ask("test"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "model unavailable")
}

func TestChat_ToolCallError(t *testing.T) {
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("bad_tool", "call_1"),
			{Content: "Sorry, I couldn't fetch that data."},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callErrors["bad_tool"] = errors.New("tool not found")

	svc := NewService(llmMock, mcpMock, newTestDB(t), testConfig)
	resp, err := svc.Chat(context.Background(), ask("test"))

	require.NoError(t, err)
	assert.Contains(t, resp.Content, "Sorry")
	assert.Equal(t, []string{"bad_tool"}, resp.ToolsUsed)
}

func TestGetConversation(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "Answer"}}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)

	resp, err := svc.Chat(context.Background(), ask("Hello"))
	require.NoError(t, err)

	conv, msgs, err := svc.GetConversation(context.Background(), testUser, resp.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, resp.ConversationID, conv.ID)
	require.Len(t, msgs, 2) // user + assistant
	assert.Equal(t, resp.MessageID, msgs[1].ID)
}

func TestListConversations(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "A1"}}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)
	_, err := svc.Chat(context.Background(), ask("Q1"))
	require.NoError(t, err)

	convs, err := svc.ListConversations(context.Background(), testUser, 10)
	require.NoError(t, err)
	assert.Len(t, convs, 1)
}

func TestDeleteConversation(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "ok"}}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)
	resp, err := svc.Chat(context.Background(), ask("test"))
	require.NoError(t, err)

	require.NoError(t, svc.DeleteConversation(context.Background(), testUser, resp.ConversationID))

	_, _, err = svc.GetConversation(context.Background(), testUser, resp.ConversationID)
	require.ErrorIs(t, err, ErrConversationNotFound)
}

func TestNewService_DefaultValues(t *testing.T) {
	svc := NewService(&mockLLMClient{}, newMockMCP(), newTestDB(t), ServiceConfig{}).(*service)
	assert.Equal(t, DefaultMaxHistory, svc.maxHistory)
	assert.Equal(t, DefaultMaxTokens, svc.maxTokens)
	assert.Equal(t, DefaultMaxToolRounds, svc.maxToolRounds)
}

// --- Error paths ---

func TestChat_BeginTurnError(t *testing.T) {
	db := newTestDB(t)
	db.beginErr = errors.New("db down")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("hi"))

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "begin turn")
}

// A failed commit returns the error and releases the turn (status-only
// failure), so the conversation is not blocked until the turn goes stale.
func TestChat_PersistTurnError_ReleasesConversation(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q"})
	db.finishErrOnce = errors.New("disk full")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), askIn(convID, "hi"))
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "persist turn")

	finished := db.finishedTurns()
	require.Len(t, finished, 2, "the failed commit is followed by a status-only release")
	assert.Equal(t, TurnFailed, finished[1].Status)
	assert.Equal(t, ErrorClassInternal, finished[1].ErrorClass)
	assert.Empty(t, finished[1].Messages)

	_, err = svc.Chat(context.Background(), askIn(convID, "again"))
	require.NoError(t, err, "the conversation must be free after a failed commit")
}

// testCommitTimeout stands in for turnPersistTimeout so a stuck commit times
// out quickly.
const testCommitTimeout = 10 * time.Millisecond

// A commit that times out has used up its deadline; the release still gets
// its own and frees the conversation.
func TestChat_TimedOutCommit_StillReleasesConversation(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q"})
	db.finishBlockOnce = true
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	svc.(*service).persistTimeout = testCommitTimeout

	_, err := svc.Chat(context.Background(), askIn(convID, "hi"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	svc.(*service).persistTimeout = turnPersistTimeout
	_, err = svc.Chat(context.Background(), askIn(convID, "again"))
	require.NoError(t, err, "the release must not reuse the expired commit deadline")
}

func TestChat_LoadHistoryError(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q"})
	db.getMessagesErr = errors.New("redis lost")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), askIn(convID, "hi"))

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "load history")
	finished := db.finishedTurns()
	require.Len(t, finished, 1)
	assert.Equal(t, TurnFailed, finished[0].Status)
	assert.Equal(t, ErrorClassInternal, finished[0].ErrorClass)
	assert.Equal(t, []TurnMessage{{Role: "user", Content: "hi"}}, finished[0].Messages, "the prompt is kept")
}

// When MCP ListTools errors, Chat must continue without tools (logs a
// warning, sets llmTools to nil) — pin that behavior so a refactor
// can't accidentally turn it into a hard failure.
func TestChat_ToolCacheListError_ContinuesWithoutTools(t *testing.T) {
	mcpMock := newMockMCP()
	mcpMock.listToolsErr = errors.New("mcp unreachable")
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "answer"}}}

	svc := NewService(llmMock, mcpMock, newTestDB(t), testConfig)
	resp, err := svc.Chat(context.Background(), ask("hi"))

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
	db := newTestDB(t)
	cfg := testConfig
	cfg.MaxToolRounds = maxRounds
	svc := NewService(llmMock, newMockMCP(), db, cfg)
	resp, err := svc.Chat(context.Background(), ask("hi"))

	require.NoError(t, err)
	// Await the detached title generation so recorded requests are stable.
	require.NoError(t, svc.Close())

	assert.Equal(t, "Forced final answer.", resp.Content)
	// maxRounds tool-call rounds + 1 forced final + 1 title.
	assert.Equal(t, maxRounds+2, llmMock.callCount())
	// The forced-final request must NOT carry Tools (the whole point of
	// the forced-final branch is to coerce a text reply).
	finalReq := llmMock.requests[maxRounds]
	assert.Nil(t, finalReq.Tools, "forced-final request must omit Tools")

	calls := db.finishedTurns()[0].Calls
	require.Len(t, calls, maxRounds+1)
	forced := calls[maxRounds]
	assert.Equal(t, CallForcedFinal, forced.Kind)
	require.NotNil(t, forced.Round)
	assert.Equal(t, maxRounds, *forced.Round)
	assert.Nil(t, forced.ToolDefinitions)
}

func TestChat_MaxToolRoundsExhausted_ForcedFinalLLMError(t *testing.T) {
	const maxRounds = 2
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("pg_read_query", "c1"),
			toolCallResponse("pg_read_query", "c2"),
			nil, // final call, errors below
		},
		errors: []error{nil, nil, errors.New("model overloaded")},
	}
	cfg := testConfig
	cfg.MaxToolRounds = maxRounds
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), cfg)
	resp, err := svc.Chat(context.Background(), ask("hi"))

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "forced final")
}

func TestGetConversation_GetMessagesError(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q"})
	db.getMessagesErr = errors.New("redis down")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	gotConv, msgs, err := svc.GetConversation(context.Background(), testUser, convID)

	require.Error(t, err)
	assert.Nil(t, gotConv)
	assert.Nil(t, msgs)
	assert.Contains(t, err.Error(), "get messages")
}

func TestListConversations_ZeroLimitFallsBackToMaxHistory(t *testing.T) {
	db := newTestDB(t)
	const customMaxHistory = 7
	cfg := testConfig
	cfg.MaxHistory = customMaxHistory
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, cfg)

	// Seed >customMaxHistory conversations so the cap matters.
	for range customMaxHistory + 5 {
		seedConversation(t, db, TurnMessage{Role: "user", Content: "q"})
	}

	got, err := svc.ListConversations(context.Background(), testUser, 0)
	require.NoError(t, err)
	assert.Len(t, got, customMaxHistory, "limit=0 must fall back to maxHistory")
}

func TestListConversations_DBError(t *testing.T) {
	db := newTestDB(t)
	db.listErr = errors.New("query failed")

	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	got, err := svc.ListConversations(context.Background(), testUser, 5)

	require.Error(t, err)
	assert.Nil(t, got)
}

// --- loadHistory: truncation and leading-tool-skip branches ---

func TestLoadHistory_TruncatesToMaxHistory(t *testing.T) {
	db := newTestDB(t)
	// Seed many messages; only the last maxHistory should survive into
	// the LLM request.
	const seeded = 30
	const maxHistory = 5
	seed := make([]TurnMessage, seeded)
	for i := range seeded {
		seed[i] = TurnMessage{Role: "user", Content: fmt.Sprintf("m%d", i)}
	}
	convID := seedConversation(t, db, seed...)

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "ok"}}}
	cfg := testConfig
	cfg.MaxHistory = maxHistory
	svc := NewService(llmMock, newMockMCP(), db, cfg)
	_, err := svc.Chat(context.Background(), askIn(convID, "follow-up"))
	require.NoError(t, err)

	// system + the last maxHistory messages, the prompt among them.
	first := llmMock.requests[0]
	require.Len(t, first.Messages, 1+maxHistory)
	assert.Equal(t, "m26", first.Messages[1].Content)
	assert.Equal(t, "follow-up", first.Messages[maxHistory].Content)
}

// loadHistory drops leading "tool" messages whose paired assistant
// tool_use block was sliced off by the maxHistory cap.
func TestLoadHistory_SkipsLeadingToolAfterTruncation(t *testing.T) {
	db := newTestDB(t)
	const maxHistory = 3
	// [user, assistant, tool, user] + prompt; the last 3 start with the tool
	// result whose assistant was cut off.
	roles := []string{"user", "assistant", "tool", "user"} // oldest -> newest
	seed := make([]TurnMessage, len(roles))
	for i, r := range roles {
		seed[i] = TurnMessage{Role: r, Content: r + "-content"}
	}
	convID := seedConversation(t, db, seed...)

	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "ok"}}}
	cfg := testConfig
	cfg.MaxHistory = maxHistory
	svc := NewService(llmMock, newMockMCP(), db, cfg)
	_, err := svc.Chat(context.Background(), askIn(convID, "follow-up"))
	require.NoError(t, err)

	first := llmMock.requests[0]
	require.GreaterOrEqual(t, len(first.Messages), 2)
	assert.Equal(t, "system", first.Messages[0].Role)
	assert.NotEqual(t, "tool", first.Messages[1].Role, "leading tool message must be skipped after truncation")
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

// --- Atomic turn persistence ---

// A failure mid-turn (here, the LLM failing on the second round after a tool
// call) must leave nothing in the transcript — not the user message, not the
// partial tool result — so loadHistory on resume is not poisoned.
func TestChat_MidTurnFailure_LeavesTranscriptUntouched(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"}, TurnMessage{Role: "assistant", Content: "a0"})
	llmMock := &mockLLMClient{
		responses: []*llm.Response{toolCallResponse("pg_read_query", "c1"), nil},
		errors:    []error{nil, errors.New("model crash mid-turn")},
	}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), askIn(convID, "question"))
	require.NoError(t, svc.Close())

	require.Error(t, err)
	assert.Nil(t, resp)
	msgs, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)
	assert.Len(t, msgs, 2, "mid-turn failure must not add to the transcript")
}

// A successful tool-using turn persists the whole turn — user, assistant
// tool_calls, tool result, final assistant — in order, in one commit.
func TestChat_SuccessfulTurn_PersistsWholeTurnInOrder(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db)
	llmMock := &mockLLMClient{
		responses: []*llm.Response{
			toolCallResponse("pg_read_query", "c1"),
			{Content: "final"},
		},
	}
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = "rows"

	svc := NewService(llmMock, mcpMock, db, testConfig)
	_, err := svc.Chat(context.Background(), askIn(convID, "q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	msgs, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)
	require.Len(t, msgs, 4)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
	assert.Equal(t, "rows", msgs[2].Content)
	assert.Equal(t, "assistant", msgs[3].Role)
	assert.Equal(t, "final", msgs[3].Content)
}

// Many concurrent Chats must not race or deadlock; title generation is bounded
// and Close drains cleanly. Run with -race to exercise the concurrency.
func TestChat_ConcurrentChats_BoundedAndClosable(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)

	const n = 25
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, err := svc.Chat(context.Background(), ask("q"))
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	require.NoError(t, svc.Close())

	convs, err := db.ListConversations(context.Background(), testUser, n*2)
	require.NoError(t, err)
	assert.Len(t, convs, n)
}

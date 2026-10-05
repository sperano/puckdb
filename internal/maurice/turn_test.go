package maurice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every provider call of a turn is recorded with its kind, round, exact
// request (system prompt, tool definitions, message references), response
// metadata, nullable usage, and the tool calls it requested.
func TestChat_RecordsEveryCallOfTheTurn(t *testing.T) {
	db := newTestDB(t)
	mcpMock := newMockMCP()
	mcpMock.listTools = []mcpgo.Tool{{Name: "pg_read_query", Description: "query"}}
	mcpMock.callResults["pg_read_query"] = "rows"
	first := toolCallResponse("pg_read_query", "c1")
	first.ID, first.Model, first.FinishReason = "req-1", "claude-test-20260101", "tool_use"
	first.Usage = &llm.Usage{PromptTokens: 100, CompletionTokens: 7, CacheCreationInputTokens: 50, CacheReadInputTokens: 0, CacheReported: true}
	llmMock := &mockLLMClient{responses: []*llm.Response{first, {Content: "final"}}}

	svc := NewService(llmMock, mcpMock, db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	record := db.finishedTurns()[0]
	assert.Equal(t, TurnSucceeded, record.Status)
	assert.Empty(t, record.ErrorClass)
	require.Len(t, record.Messages, 4, "prompt, tool request, tool result, answer")
	require.Len(t, record.Calls, 2)

	c0 := record.Calls[0]
	assert.Equal(t, CallChatRound, c0.Kind)
	require.NotNil(t, c0.Round)
	assert.Equal(t, 0, *c0.Round)
	assert.Equal(t, CallSucceeded, c0.Status)
	assert.Equal(t, "anthropic", c0.Provider)
	assert.Equal(t, "claude-test-20260101", c0.Model, "the provider-reported model wins")
	assert.Equal(t, "req-1", c0.ProviderRequestID)
	assert.Equal(t, "tool_use", c0.FinishReason)
	require.NotNil(t, c0.SystemPrompt)
	assert.Contains(t, *c0.SystemPrompt, "Maurice")
	require.Len(t, c0.ToolDefinitions, 1)
	assert.Equal(t, "pg_read_query", c0.ToolDefinitions[0].Function.Name)
	assert.Equal(t, []MessageRef{{TurnIndex: 0}}, c0.Inputs)
	assert.Equal(t, int64(100), *c0.Usage.Input)
	assert.Equal(t, int64(7), *c0.Usage.Output)
	assert.Equal(t, int64(50), *c0.Usage.CacheCreation)
	assert.Equal(t, int64(0), *c0.Usage.CacheRead, "a reported zero stays zero")
	assert.False(t, c0.CompletedAt.Before(c0.StartedAt))

	require.Len(t, c0.ToolCalls, 1)
	tc := c0.ToolCalls[0]
	assert.Equal(t, "c1", tc.ProviderToolCallID)
	assert.Equal(t, "pg_read_query", tc.Name)
	assert.Equal(t, "{}", tc.ArgumentsRaw)
	assert.Equal(t, ToolSucceeded, tc.Status)
	require.NotNil(t, tc.Result)
	assert.Equal(t, "rows", *tc.Result)
	require.NotNil(t, tc.StartedAt)
	require.NotNil(t, tc.CompletedAt)

	c1 := record.Calls[1]
	assert.Equal(t, 1, *c1.Round)
	assert.Equal(t, "test-model", c1.Model, "the configured model when the provider reports none")
	assert.Equal(t, []MessageRef{{TurnIndex: 0}, {TurnIndex: 1}, {TurnIndex: 2}}, c1.Inputs)
	assert.Equal(t, TokenUsage{}, c1.Usage, "no usage reported means every count is nil")
	assert.Empty(t, c1.ToolCalls)
	assert.Equal(t, resp.MessageID, lastMessageID(t, db, resp.ConversationID))
}

func lastMessageID(t *testing.T, db DB, convID string) string {
	t.Helper()
	msgs, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	return msgs[len(msgs)-1].ID
}

// Usage without cache accounting (OpenAI-compatible providers) leaves the
// cache counts nil rather than zero.
func TestTokenUsage_CacheCountsOnlyWhenReported(t *testing.T) {
	usage := tokenUsage(&llm.Usage{PromptTokens: 3, CompletionTokens: 2})
	require.NotNil(t, usage.Input)
	require.NotNil(t, usage.Output)
	assert.Nil(t, usage.CacheCreation)
	assert.Nil(t, usage.CacheRead)
	assert.Equal(t, TokenUsage{}, tokenUsage(nil))
}

// Prior turns are referenced by their stored message IDs, so each provider
// prompt can be rebuilt without copying content.
func TestChat_HistoryInputsReferenceStoredMessages(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"}, TurnMessage{Role: "assistant", Content: "a0"})
	stored, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)

	svc := NewService(&mockLLMClient{responses: []*llm.Response{{Content: "a1"}}}, newMockMCP(), db, testConfig)
	_, err = svc.Chat(context.Background(), askIn(convID, "q1"))
	require.NoError(t, err)

	inputs := db.finishedTurns()[0].Calls[0].Inputs
	assert.Equal(t, []MessageRef{{MessageID: stored[0].ID}, {MessageID: stored[1].ID}, {TurnIndex: 0}}, inputs)
}

// A failed turn keeps the exact prompts of every call, including the call that
// failed, and its usage, without touching the transcript.
func TestChat_FailedTurnKeepsExactPromptsAndUsage(t *testing.T) {
	db := newTestDB(t)
	first := toolCallResponse("pg_read_query", "c1")
	first.Usage = &llm.Usage{PromptTokens: 11, CompletionTokens: 4}
	llmMock := &mockLLMClient{
		responses: []*llm.Response{first, nil},
		errors:    []error{nil, errors.New("upstream 500")},
	}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	_, err := svc.Chat(context.Background(), ask("doomed"))
	require.Error(t, err)

	record := db.finishedTurns()[0]
	assert.Equal(t, TurnFailed, record.Status)
	assert.Equal(t, ErrorClassProvider, record.ErrorClass)
	require.Len(t, record.Messages, 3, "prompt, tool request and tool result were all sent")
	assert.Equal(t, "doomed", record.Messages[0].Content)
	require.Len(t, record.Calls, 2)
	assert.Equal(t, int64(11), *record.Calls[0].Usage.Input, "usage incurred before the failure is kept")
	failed := record.Calls[1]
	assert.Equal(t, CallFailed, failed.Status)
	assert.Equal(t, ErrorClassProvider, failed.ErrorClass)
	assert.Equal(t, TokenUsage{}, failed.Usage)
	assert.Equal(t, []MessageRef{{TurnIndex: 0}, {TurnIndex: 1}, {TurnIndex: 2}}, failed.Inputs)
	assert.NotContains(t, string(failed.ErrorClass), "500", "no provider error text is stored")
}

func TestToolStatus(t *testing.T) {
	assert.Equal(t, ToolSucceeded, toolStatus(`{"a":1}`, false))
	assert.Equal(t, ToolSucceeded, toolStatus("", false), "no arguments")
	assert.Equal(t, ToolError, toolStatus(`{}`, true))
	assert.Equal(t, ToolParseError, toolStatus(`{"a":`, false))
	assert.Equal(t, ToolParseError, toolStatus(`{"a":`, true))
}

// A tool error is recorded as tool_error with the text the model saw; a tool
// that reports IsError is a tool_error too.
func TestChat_RecordsToolFailures(t *testing.T) {
	db := newTestDB(t)
	mcpMock := newMockMCP()
	mcpMock.callErrors["broken"] = errors.New("connection refused")
	mcpMock.isError["grumpy"] = true
	both := &llm.Response{ToolCalls: []llm.ToolCall{
		{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "broken", Arguments: "{}"}},
		{ID: "c2", Type: "function", Function: llm.ToolCallFunction{Name: "grumpy", Arguments: "{"}},
	}}
	svc := NewService(&mockLLMClient{responses: []*llm.Response{both, {Content: "sorry"}}}, mcpMock, db, testConfig)
	_, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)

	tools := db.finishedTurns()[0].Calls[0].ToolCalls
	require.Len(t, tools, 2)
	assert.Equal(t, ToolError, tools[0].Status)
	assert.Contains(t, *tools[0].Result, "connection refused")
	assert.Equal(t, ToolParseError, tools[1].Status)
}

// A requested tool call that never ran stays cancelled with no result.
func TestCompleteCallRecord_RequestedToolsStartCancelled(t *testing.T) {
	call := newCallRecord(CallForcedFinal, nil, "p", "m")
	completeCallRecord(&call, toolCallResponse("t", "c9"), nil)
	require.Len(t, call.ToolCalls, 1)
	assert.Equal(t, ToolCancelled, call.ToolCalls[0].Status)
	assert.Nil(t, call.ToolCalls[0].Result)
	assert.Nil(t, call.ToolCalls[0].StartedAt)
}

func TestChat_IdempotentRetryReturnsOriginalAnswer(t *testing.T) {
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{toolCallResponse("pg_read_query", "c1"), {Content: "answer"}}}
	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	req := ask("q")
	req.IdempotencyKey = "client-key-1"

	first, err := svc.Chat(context.Background(), req)
	require.NoError(t, err)
	require.NoError(t, svc.Close())
	calls := llmMock.callCount()

	retry, err := svc.Chat(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, first, retry, "a retry returns the original turn")
	assert.Equal(t, calls, llmMock.callCount(), "a retry must not call the LLM again")
	assert.Len(t, db.finishedTurns(), 1, "a retry must not write a second turn")

	req.Message = "a different prompt"
	_, err = svc.Chat(context.Background(), req)
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)
}

func TestChat_RetryOfFailedTurnIsRejected(t *testing.T) {
	db := newTestDB(t)
	llmMock := &mockLLMClient{errors: []error{errors.New("boom")}}
	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	req := ask("q")
	req.IdempotencyKey = "client-key-2"

	_, err := svc.Chat(context.Background(), req)
	require.Error(t, err)
	_, err = svc.Chat(context.Background(), req)
	require.ErrorIs(t, err, ErrTurnNotCompleted)
	assert.Contains(t, err.Error(), "provider_error")
	assert.Equal(t, 1, llmMock.callCount())
}

func TestChat_RejectsOverlongIdempotencyKey(t *testing.T) {
	svc := NewService(&mockLLMClient{}, newMockMCP(), newTestDB(t), testConfig)
	req := ask("q")
	req.IdempotencyKey = strings.Repeat("k", maxIdempotencyKeyLen+1)
	_, err := svc.Chat(context.Background(), req)
	require.ErrorIs(t, err, ErrInvalidIdempotencyKey)
}

func TestChat_OwnerScoping(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("mine"))
	require.NoError(t, err)

	const intruder = "user-b"
	_, err = svc.Chat(context.Background(), ChatRequest{UserID: intruder, ConversationID: &resp.ConversationID, Message: "hi"})
	require.ErrorIs(t, err, ErrConversationNotFound)
	_, _, err = svc.GetConversation(context.Background(), intruder, resp.ConversationID)
	require.ErrorIs(t, err, ErrConversationNotFound)
	require.ErrorIs(t, svc.DeleteConversation(context.Background(), intruder, resp.ConversationID), ErrConversationNotFound)
	convs, err := svc.ListConversations(context.Background(), intruder, 10)
	require.NoError(t, err)
	assert.Empty(t, convs)
}

// blockingLLM holds every non-title completion until release is closed or
// the context ends, signalling entered first.
type blockingLLM struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingLLM() *blockingLLM {
	return &blockingLLM{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (b *blockingLLM) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req.MaxTokens == titleMaxTokens {
		return &llm.Response{Content: "title"}, nil
	}
	b.entered <- struct{}{}
	select {
	case <-b.release:
		return &llm.Response{Content: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// While a turn runs, a second prompt to the same conversation is rejected
// explicitly instead of forking the history.
func TestChat_SecondPromptWhileRunningIsRejected(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"})
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	var wg sync.WaitGroup
	var firstErr error
	wg.Go(func() { _, firstErr = svc.Chat(context.Background(), askIn(convID, "slow")) })
	<-blocker.entered

	_, err := svc.Chat(context.Background(), askIn(convID, "impatient"))
	require.ErrorIs(t, err, ErrTurnInProgress)

	close(blocker.release)
	wg.Wait()
	require.NoError(t, firstErr)
	msgs, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)
	assert.Len(t, msgs, 3, "only the first prompt's turn is in the transcript")
}

// A cancelled request still commits its turn as cancelled, which releases the
// conversation for the next prompt.
func TestChat_CancelledRequestReleasesConversation(t *testing.T) {
	db := newTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"})
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var chatErr error
	wg.Go(func() { _, chatErr = svc.Chat(ctx, askIn(convID, "abandoned")) })
	<-blocker.entered
	cancel()
	wg.Wait()
	require.ErrorIs(t, chatErr, context.Canceled)

	record := db.finishedTurns()[0]
	assert.Equal(t, TurnCancelled, record.Status)
	assert.Equal(t, ErrorClassCancelled, record.ErrorClass)
	assert.Equal(t, ErrorClassCancelled, record.Calls[0].ErrorClass)

	close(blocker.release)
	_, err := svc.Chat(context.Background(), askIn(convID, "next"))
	require.NoError(t, err)
}

func TestErrorClass(t *testing.T) {
	assert.Equal(t, ErrorClassTimeout, errorClass(context.DeadlineExceeded))
	assert.Equal(t, ErrorClassCancelled, errorClass(context.Canceled))
	assert.Equal(t, ErrorClassProvider, errorClass(classify(ErrorClassProvider, errors.New("x"))))
	assert.Equal(t, ErrorClassTimeout, errorClass(classify(ErrorClassProvider, context.DeadlineExceeded)),
		"a timeout wins over where it surfaced")
	assert.Equal(t, ErrorClassInternal, errorClass(errors.New("unknown")))
	assert.Equal(t, TurnCancelled, failedStatus(ErrorClassCancelled))
	assert.Equal(t, TurnFailed, failedStatus(ErrorClassTimeout))
}

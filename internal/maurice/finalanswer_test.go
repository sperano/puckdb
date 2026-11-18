package maurice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/llm"
)

// captureLog points the global logger at a buffer for the test and returns
// a function decoding each line it logged. Not for parallel tests.
func captureLog(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = previous })
	return func() []map[string]any {
		var lines []map[string]any
		for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
			if len(raw) == 0 {
				continue
			}
			var line map[string]any
			require.NoError(t, json.Unmarshal(raw, &line))
			lines = append(lines, line)
		}
		return lines
	}
}

// linesWithMessage keeps the log lines whose message is msg.
func linesWithMessage(lines []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["message"] == msg {
			out = append(out, l)
		}
	}
	return out
}

// An empty answer after a tool round gets one retry without tools: the loop's
// messages followed by the nudge. The nudge is the call's instruction, never
// a turn message, and the retried answer is the turn's answer and title
// source.
func TestChat_EmptyAnswerAfterToolsIsRetriedWithNudge(t *testing.T) {
	db := newTestDB(t)
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = "rows"
	llmMock := &mockLLMClient{responses: []*llm.Response{
		toolCallResponse("pg_read_query", "c1"),
		{Content: "", FinishReason: "end_turn"},
		{Content: "Drop him."},
		{Content: "Title"},
	}}

	svc := NewService(llmMock, mcpMock, db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, "Drop him.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, resp.ToolsUsed)
	retry := llmMock.requests[2]
	assert.Nil(t, retry.Tools, "the retry offers no tools")
	assert.Equal(t, DefaultMaxTokens, retry.MaxTokens)
	var roles []string
	for _, m := range retry.Messages {
		roles = append(roles, m.Role)
	}
	assert.Equal(t, []string{"system", "user", "assistant", "tool", "user"}, roles,
		"the loop's messages without the empty response, then the nudge")
	assert.Equal(t, emptyAnswerNudge, retry.Messages[len(retry.Messages)-1].Content)

	record := db.finishedTurns()[0]
	assert.Equal(t, TurnSucceeded, record.Status)
	assert.Equal(t, []CallKind{CallChatRound, CallChatRound, CallForcedFinal}, callKinds(record.Calls))
	nudged := record.Calls[2]
	require.NotNil(t, nudged.Instruction)
	assert.Equal(t, emptyAnswerNudge, *nudged.Instruction)
	assert.Nil(t, nudged.ToolDefinitions)
	assert.Equal(t, []MessageRef{{TurnIndex: 0}, {TurnIndex: 1}, {TurnIndex: 2}}, nudged.Inputs)
	require.NotNil(t, nudged.Round)
	assert.Equal(t, 2, *nudged.Round)
	assert.Nil(t, record.Calls[1].Instruction, "only the retry carries the nudge")

	msgs, err := db.GetMessages(context.Background(), testUser, resp.ConversationID)
	require.NoError(t, err)
	var got []string
	for _, m := range msgs {
		got = append(got, m.Role+":"+m.Content)
	}
	assert.Equal(t, []string{"user:q", "assistant:", "tool:rows", "assistant:Drop him."}, got, "the nudge is no turn message")
	assert.Equal(t, "Drop him.", llmMock.requests[3].Messages[1].Content, "title from the retried answer")
}

// An answer of only whitespace is as empty as no text.
func TestChat_WhitespaceAnswerIsRetried(t *testing.T) {
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: " \n\t"}, {Content: "Answer"}}}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, "Answer", resp.Content)
	assert.Equal(t, []CallKind{CallChatRound, CallForcedFinal}, callKinds(db.finishedTurns()[0].Calls))
}

// A second empty answer fails the turn as empty_answer: no assistant message,
// no title, both calls kept, and each empty response logged with its stop
// reason.
func TestChat_EmptyAnswerTwiceFailsTurn(t *testing.T) {
	logged := captureLog(t)
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{
		{Content: "", FinishReason: "end_turn", Usage: &llm.Usage{CompletionTokens: 3}},
		{Content: "", FinishReason: "max_tokens"},
	}}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	_, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, svc.Close())

	require.ErrorIs(t, err, ErrEmptyAnswer)
	assert.Equal(t, ErrorClassEmptyAnswer, errorClass(err))
	assert.Equal(t, 2, llmMock.callCount(), "one retry, no title")
	assert.Empty(t, db.titleRecords())
	record := db.finishedTurns()[0]
	assert.Equal(t, TurnFailed, record.Status)
	assert.Equal(t, ErrorClassEmptyAnswer, record.ErrorClass)
	assert.Equal(t, []CallKind{CallChatRound, CallForcedFinal}, callKinds(record.Calls))
	assert.Equal(t, []string{"end_turn", "max_tokens"}, []string{record.Calls[0].FinishReason, record.Calls[1].FinishReason})
	assert.Equal(t, []TurnMessage{{Role: "user", Content: "q"}}, record.Messages, "no empty answer is stored")

	empty := linesWithMessage(logged(), "LLM final response has no text")
	require.Len(t, empty, 2)
	assert.Equal(t, "end_turn", empty[0]["finish_reason"])
	assert.Equal(t, float64(3), empty[0]["completion_tokens"])
	assert.Equal(t, "max_tokens", empty[1]["finish_reason"])
	assert.NotContains(t, empty[1], "completion_tokens", "no usage reported, none logged")
	assert.Equal(t, "warn", empty[0]["level"])
}

// The forced final answer after the tool rounds ran out gets the same retry
// when it is empty.
func TestChat_EmptyForcedFinalAnswerIsRetried(t *testing.T) {
	const maxRounds = 1
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{
		toolCallResponse("pg_read_query", "c1"),
		{Content: ""},
		{Content: "Answer"},
	}}
	cfg := testConfig
	cfg.MaxToolRounds = maxRounds

	svc := NewService(llmMock, newMockMCP(), db, cfg)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, "Answer", resp.Content)
	calls := db.finishedTurns()[0].Calls
	assert.Equal(t, []CallKind{CallChatRound, CallForcedFinal, CallForcedFinal}, callKinds(calls))
	assert.Nil(t, calls[1].Instruction, "the forced call has no nudge")
	require.NotNil(t, calls[2].Instruction)
	assert.Equal(t, emptyAnswerNudge, *calls[2].Instruction)
	assert.Equal(t, calls[1].Inputs, calls[2].Inputs, "the retry is sent what the forced call was")
}

// A provider error on the retry fails the turn as a provider error.
func TestChat_EmptyAnswerRetryProviderError(t *testing.T) {
	db := newTestDB(t)
	llmMock := &mockLLMClient{
		responses: []*llm.Response{{Content: ""}, nil},
		errors:    []error{nil, errors.New("overloaded")},
	}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	_, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, svc.Close())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM empty answer retry: overloaded")
	assert.Equal(t, ErrorClassProvider, errorClass(err))
	record := db.finishedTurns()[0]
	assert.Equal(t, ErrorClassProvider, record.ErrorClass)
	assert.Equal(t, CallFailed, record.Calls[1].Status)
}

// An answer cut off at max tokens is kept and logged as cut off.
func TestChat_TruncatedAnswerIsKeptAndLogged(t *testing.T) {
	logged := captureLog(t)
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "Partial", FinishReason: "max_tokens"}}}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, "Partial", resp.Content)
	cut := linesWithMessage(logged(), "LLM final response cut off at max tokens")
	require.Len(t, cut, 1)
	assert.Equal(t, "max_tokens", cut[0]["finish_reason"])
	assert.Equal(t, "warn", cut[0]["level"])
	assert.Empty(t, linesWithMessage(logged(), "LLM final response has no text"))
}

// PostgreSQL stores a turn that failed with an empty answer, the retry's
// kind and nudge instruction included.
func TestPg_EmptyAnswerTurnIsStored(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: ""}, {Content: ""}}}

	svc := NewService(llmMock, newMockMCP(), NewPgDB(pool), testConfig)
	_, err := svc.Chat(context.Background(), ChatRequest{UserID: user, Message: "q"})
	require.NoError(t, svc.Close())
	require.ErrorIs(t, err, ErrEmptyAnswer)

	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_turns t JOIN maurice_conversations c ON c.id = t.conversation_id
		WHERE c.user_id = $1 AND t.status = 'failed' AND t.error_class = 'empty_answer'`, user))
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls l JOIN maurice_conversations c ON c.id = l.conversation_id
		WHERE c.user_id = $1 AND l.call_kind = 'forced_final' AND l.round_number = 1 AND l.instruction = $2`, user, emptyAnswerNudge))
}

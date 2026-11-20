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

// An empty answer after a tool round gets a retry without tools: the loop's
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

// The last allowed attempt still answers the turn: two empty answers after a
// tool round, then text. Each retry carries the nudge and is sent what the
// first final call was, and the tool chain never runs again.
func TestChat_EmptyAnswerSucceedsOnLastAttempt(t *testing.T) {
	db := newTestDB(t)
	mcpMock := newMockMCP()
	mcpMock.callResults["pg_read_query"] = "rows"
	var toolRuns []string
	mcpMock.onCall = func(name string) { toolRuns = append(toolRuns, name) }
	llmMock := &mockLLMClient{responses: []*llm.Response{
		toolCallResponse("pg_read_query", "c1"),
		{Content: "", FinishReason: "end_turn"},
		{Content: "", FinishReason: "end_turn"},
		{Content: "Drop him."},
		{Content: "Title"},
	}}

	svc := NewService(llmMock, mcpMock, db, testConfig)
	resp, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, "Drop him.", resp.Content)
	assert.Equal(t, []string{"pg_read_query"}, toolRuns, "the tool ran once, never again on a retry")
	record := db.finishedTurns()[0]
	assert.Equal(t, TurnSucceeded, record.Status)
	assert.Equal(t, []CallKind{CallChatRound, CallChatRound, CallForcedFinal, CallForcedFinal}, callKinds(record.Calls))
	for _, retry := range record.Calls[2:] {
		require.NotNil(t, retry.Instruction)
		assert.Equal(t, emptyAnswerNudge, *retry.Instruction)
		assert.Nil(t, retry.ToolDefinitions)
		assert.Equal(t, []MessageRef{{TurnIndex: 0}, {TurnIndex: 1}, {TurnIndex: 2}}, retry.Inputs)
	}
	for _, req := range llmMock.requests[2:4] {
		assert.Nil(t, req.Tools, "a retry offers no tools")
	}
	assert.Equal(t, "Drop him.", llmMock.requests[4].Messages[1].Content, "title from the last attempt's answer")
}

// Three empty answers fail the turn as empty_answer after exactly three
// completions: no fourth attempt, no assistant message, no title, every call
// kept, and each empty response logged with its stop reason and attempt.
func TestChat_EmptyAnswerExhaustsAttemptsFailsTurn(t *testing.T) {
	const wantAttempts = 3
	require.Equal(t, wantAttempts, maxFinalAnswerAttempts)
	logged := captureLog(t)
	db := newTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{
		{Content: "", FinishReason: "end_turn", Usage: &llm.Usage{CompletionTokens: 3}},
		{Content: "", FinishReason: "max_tokens"},
		{Content: " ", FinishReason: "stop"},
		{Content: "a fourth attempt must not be made"},
	}}

	svc := NewService(llmMock, newMockMCP(), db, testConfig)
	_, err := svc.Chat(context.Background(), ask("q"))
	require.NoError(t, svc.Close())

	require.ErrorIs(t, err, ErrEmptyAnswer)
	assert.Equal(t, ErrorClassEmptyAnswer, errorClass(err))
	assert.Equal(t, wantAttempts, llmMock.callCount(), "three attempts, no title")
	assert.Empty(t, db.titleRecords())
	record := db.finishedTurns()[0]
	assert.Equal(t, TurnFailed, record.Status)
	assert.Equal(t, ErrorClassEmptyAnswer, record.ErrorClass)
	assert.Equal(t, []CallKind{CallChatRound, CallForcedFinal, CallForcedFinal}, callKinds(record.Calls))
	assert.Equal(t, []string{"end_turn", "max_tokens", "stop"},
		[]string{record.Calls[0].FinishReason, record.Calls[1].FinishReason, record.Calls[2].FinishReason})
	assert.Equal(t, []TurnMessage{{Role: "user", Content: "q"}}, record.Messages, "no empty answer is stored")

	empty := linesWithMessage(logged(), "LLM final response has no text")
	require.Len(t, empty, wantAttempts)
	for i, want := range []string{"end_turn", "max_tokens", "stop"} {
		assert.Equal(t, want, empty[i]["finish_reason"])
		assert.Equal(t, float64(i+1), empty[i]["attempt"])
		assert.Equal(t, float64(wantAttempts), empty[i]["max_attempts"])
		assert.Equal(t, "warn", empty[i]["level"])
	}
	assert.Equal(t, float64(3), empty[0]["completion_tokens"])
	assert.NotContains(t, empty[1], "completion_tokens", "no usage reported, none logged")
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

// A provider error on a retry fails the turn as a provider error.
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

// PostgreSQL stores a turn that failed with an empty answer, each retry's
// kind and nudge instruction included.
func TestPg_EmptyAnswerTurnIsStored(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: ""}, {Content: ""}, {Content: ""}}}

	svc := NewService(llmMock, newMockMCP(), NewPgDB(pool), testConfig)
	_, err := svc.Chat(context.Background(), ChatRequest{UserID: user, Message: "q"})
	require.NoError(t, svc.Close())
	require.ErrorIs(t, err, ErrEmptyAnswer)

	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_turns t JOIN maurice_conversations c ON c.id = t.conversation_id
		WHERE c.user_id = $1 AND t.status = 'failed' AND t.error_class = 'empty_answer'`, user))
	assert.Equal(t, 2, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls l JOIN maurice_conversations c ON c.id = l.conversation_id
		WHERE c.user_id = $1 AND l.call_kind = 'forced_final' AND l.round_number IN (1, 2) AND l.instruction = $2`, user, emptyAnswerNudge))
}

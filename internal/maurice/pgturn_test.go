package maurice

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PostgreSQL-only behaviour: usage rows, atomic commits across tables, and
// turn concurrency between separate connections (service instances).

func pgCount(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func int64p(v int64) *int64 { return &v }

// usageTurnRecord is a two-call turn over one prior message: a tool round and
// a final answer.
func usageTurnRecord(start *TurnStart, priorID string) TurnRecord {
	now := time.Now()
	result := "rows"
	system := "system prompt v1"
	round0, round1 := 0, 1
	ran, done := now.Add(2*time.Millisecond), now.Add(3*time.Millisecond)
	return TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded,
		Messages: []TurnMessage{
			{Role: "user", Content: "q1"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "pg_read_query", Arguments: `{"sql":"SELECT 1"}`}},
				{ID: "c2", Type: "function", Function: llm.ToolCallFunction{Name: "broken_args", Arguments: `{"a":`}},
			}},
			{Role: "tool", Content: "rows", ToolCallID: "c1"},
			{Role: "assistant", Content: "a1"},
		},
		Calls: []LLMCallRecord{
			{
				Kind: CallChatRound, Round: &round0, Provider: "anthropic", Model: "m", ProviderRequestID: "req-1",
				Status: CallSucceeded, FinishReason: "tool_use", StartedAt: now, CompletedAt: now.Add(time.Millisecond),
				Usage:        TokenUsage{Input: int64p(100), Output: int64p(9), CacheCreation: int64p(0), CacheRead: int64p(80)},
				SystemPrompt: &system,
				ToolDefinitions: []llm.Tool{{Type: "function", Function: llm.ToolFunction{
					Name: "pg_read_query", Parameters: []byte(`{"type":"object"}`)}}},
				Inputs: []MessageRef{{MessageID: priorID}, {TurnIndex: 0}},
				ToolCalls: []ToolCallRecord{
					{ProviderToolCallID: "c1", Name: "pg_read_query", ArgumentsRaw: `{"sql":"SELECT 1"}`,
						Result: &result, Status: ToolSucceeded, StartedAt: &ran, CompletedAt: &done},
					{ProviderToolCallID: "c2", Name: "broken_args", ArgumentsRaw: `{"a":`, Status: ToolCancelled},
				},
			},
			{
				Kind: CallChatRound, Round: &round1, Provider: "anthropic", Model: "m", Status: CallSucceeded,
				StartedAt: now.Add(4 * time.Millisecond), CompletedAt: now.Add(5 * time.Millisecond), SystemPrompt: &system,
				Inputs: []MessageRef{{MessageID: priorID}, {TurnIndex: 0}, {TurnIndex: 1}, {TurnIndex: 2}},
			},
		},
	}
}

func TestPg_FinishTurn_StoresCallsLinksAndToolCalls(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	ctx := context.Background()
	user := newPgUser(t, pool)
	prior := chat(t, db, user, "", "q0", "a0")
	priorMsgs, err := db.GetMessages(ctx, user, prior.ConversationID)
	require.NoError(t, err)
	start := beginTurn(t, db, user, prior.ConversationID, "q1")

	ids, err := db.FinishTurn(ctx, usageTurnRecord(start, priorMsgs[0].ID))
	require.NoError(t, err)
	require.Len(t, ids, 4)

	var input, output, cacheCreation, cacheRead *int64
	var system, requestID string
	var tools []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
		       system_prompt, provider_request_id, tool_definitions
		FROM maurice_llm_calls WHERE turn_id = $1 AND round_number = 0`, start.TurnID,
	).Scan(&input, &output, &cacheCreation, &cacheRead, &system, &requestID, &tools))
	assert.Equal(t, int64(100), *input)
	assert.Equal(t, int64(9), *output)
	assert.Equal(t, int64(0), *cacheCreation)
	assert.Equal(t, int64(80), *cacheRead)
	assert.Equal(t, "system prompt v1", system)
	assert.Equal(t, "req-1", requestID)
	assert.JSONEq(t, `[{"type":"function","function":{"name":"pg_read_query","parameters":{"type":"object"}}}]`, string(tools))

	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls
		WHERE turn_id = $1 AND round_number = 1 AND input_tokens IS NULL AND output_tokens IS NULL
		  AND cache_creation_input_tokens IS NULL AND cache_read_input_tokens IS NULL AND tool_definitions IS NULL`, start.TurnID),
		"unreported usage is NULL, not zero")

	rows, err := pool.Query(ctx, `
		SELECT m.id::text FROM maurice_llm_call_messages l
		JOIN maurice_llm_calls c ON c.id = l.llm_call_id
		JOIN maurice_messages m ON m.id = l.message_id
		WHERE c.turn_id = $1 AND c.round_number = 1 ORDER BY l.input_number`, start.TurnID)
	require.NoError(t, err)
	var linked []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		linked = append(linked, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{priorMsgs[0].ID, ids[0], ids[1], ids[2]}, linked, "inputs rebuild the exact request")

	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_tool_calls
		WHERE turn_id = $1 AND sequence_number = 0 AND status = 'succeeded' AND result = 'rows'
		  AND arguments = '{"sql":"SELECT 1"}'::jsonb AND started_at IS NOT NULL`, start.TurnID))
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_tool_calls
		WHERE turn_id = $1 AND sequence_number = 1 AND status = 'cancelled' AND result IS NULL
		  AND arguments IS NULL AND arguments_raw = '{"a":' AND started_at IS NULL`, start.TurnID),
		"invalid JSON arguments keep only the raw text")
}

// A failed turn stores its messages and the failed call's links, so the
// exact provider prompt survives, while the transcript ignores them.
func TestPg_FailedTurn_KeepsExactPrompts(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	ctx := context.Background()
	user := newPgUser(t, pool)
	start := beginTurn(t, db, user, "", "doomed")
	round := 0
	now := time.Now()
	_, err := db.FinishTurn(ctx, TurnRecord{
		TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnFailed, ErrorClass: ErrorClassProvider,
		Messages: []TurnMessage{{Role: "user", Content: "doomed"}},
		Calls: []LLMCallRecord{{
			Kind: CallChatRound, Round: &round, Provider: "anthropic", Model: "m", Status: CallFailed,
			ErrorClass: ErrorClassProvider, StartedAt: now, CompletedAt: now, Inputs: []MessageRef{{TurnIndex: 0}},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_call_messages l
		JOIN maurice_llm_calls c ON c.id = l.llm_call_id
		JOIN maurice_messages m ON m.id = l.message_id
		WHERE c.turn_id = $1 AND c.status = 'failed' AND c.error_class = 'provider_error' AND m.content = 'doomed'`, start.TurnID))
	msgs, err := db.GetMessages(ctx, user, start.ConversationID)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

// A failure on any row rolls the whole commit back: no messages, no calls,
// and the turn is still running so it can be committed again.
func TestPg_FinishTurn_IsAtomic(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	ctx := context.Background()
	user := newPgUser(t, pool)
	start := beginTurn(t, db, user, "", "q")

	bad := usageTurnRecord(start, uuid.NewString()) // input links a message that does not exist
	_, err := db.FinishTurn(ctx, bad)
	require.Error(t, err)

	assert.Zero(t, pgCount(t, pool, `SELECT count(*) FROM maurice_messages WHERE turn_id = $1`, start.TurnID))
	assert.Zero(t, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls WHERE turn_id = $1`, start.TurnID))
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_turns WHERE id = $1 AND status = 'running'`, start.TurnID))

	finishTurn(t, db, start, "q", TurnMessage{Role: "assistant", Content: "a"})
}

// Title-generation tokens count toward the conversation owner, and sessions
// are counted as puckdb_session_count.
func TestPg_UsageView(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	ctx := context.Background()
	user := newPgUser(t, pool)
	start := beginTurn(t, db, user, "", "q1")
	ids, err := db.FinishTurn(ctx, usageTurnRecord(start, mustSeedMessage(t, db, user)))
	require.NoError(t, err)
	title := titleCallRecord(ids)
	title.Usage = TokenUsage{Input: int64p(30), Output: int64p(4)}
	require.NoError(t, db.RecordTitle(ctx, TitleRecord{UserID: user, ConversationID: start.ConversationID, Title: "T", Call: title}))
	for range 2 {
		_, err = pool.Exec(ctx, `INSERT INTO app_sessions (user_id, session_key_hash) VALUES ($1, $2)`, user, []byte(uuid.NewString()))
		require.NoError(t, err)
	}

	var sessions, turns, calls, input, output, cacheRead int64
	var active, callSeconds float64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT puckdb_session_count, succeeded_turn_count, llm_call_count, input_tokens, output_tokens,
		       cache_read_input_tokens, active_seconds, llm_call_seconds
		FROM app_user_usage WHERE user_id = $1`, user,
	).Scan(&sessions, &turns, &calls, &input, &output, &cacheRead, &active, &callSeconds))
	assert.Equal(t, int64(2), sessions)
	assert.Equal(t, int64(2), turns, "the seed turn and the usage turn")
	assert.Equal(t, int64(3), calls, "two chat rounds and the title call")
	assert.Equal(t, int64(130), input, "title tokens count toward the user")
	assert.Equal(t, int64(13), output)
	assert.Equal(t, int64(80), cacheRead)
	assert.Positive(t, active)
	assert.Positive(t, callSeconds)
}

// mustSeedMessage commits one prompt/answer turn and returns the prompt's ID.
func mustSeedMessage(t *testing.T, db DB, user string) string {
	t.Helper()
	start := beginTurn(t, db, user, "", "seed")
	return finishTurn(t, db, start, "seed", TurnMessage{Role: "assistant", Content: "ok"})[0]
}

func TestPg_MalformedConversationIDIsNotFound(t *testing.T) {
	pool := openPgTestPool(t)
	db := NewPgDB(pool)
	user := newPgUser(t, pool)
	_, err := db.GetConversation(context.Background(), user, "not-a-uuid")
	require.ErrorIs(t, err, ErrConversationNotFound)
	_, err = db.BeginTurn(context.Background(), turnParams(user, "not-a-uuid", "k", "q"))
	require.ErrorIs(t, err, ErrConversationNotFound)
}

// concurrentTurnStarts is how many requests race for one conversation.
const concurrentTurnStarts = 8

// Requests on separate connections (as from separate service replicas) race
// to start a turn on one conversation; exactly one wins.
func TestPg_ConcurrentTurnStarts_OneWins(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	conv := chat(t, NewPgDB(pool), user, "", "q", "a").ConversationID

	for _, sameKey := range []bool{false, true} {
		errs := raceTurnStarts(t, func(i int) BeginTurnParams {
			key := uuid.NewString()
			if sameKey {
				key = "shared-retry-key"
			}
			return turnParams(user, conv, key, "same prompt")
		})
		assertOneWinner(t, errs)
		_, err := pool.Exec(context.Background(), `UPDATE maurice_turns SET status = 'failed', error_class = 'abandoned', completed_at = clock_timestamp() WHERE status = 'running'`)
		require.NoError(t, err)
	}

	// A retried first prompt racing itself opens one conversation.
	errs := raceTurnStarts(t, func(int) BeginTurnParams { return turnParams(user, "", "new-conv-key", "hello") })
	assertOneWinner(t, errs)
	assert.Equal(t, 2, pgCount(t, pool, `SELECT count(*) FROM maurice_conversations WHERE user_id = $1`, user))
}

// raceTurnStarts releases concurrentTurnStarts BeginTurn calls at once, each
// on its own pool.
func raceTurnStarts(t *testing.T, params func(i int) BeginTurnParams) []error {
	t.Helper()
	dbs := make([]DB, concurrentTurnStarts)
	for i := range dbs {
		dbs[i] = NewPgDB(newPgPool(t))
	}
	errs := make([]error, concurrentTurnStarts)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := range dbs {
		wg.Go(func() {
			<-barrier
			_, errs[i] = dbs[i].BeginTurn(context.Background(), params(i))
		})
	}
	close(barrier)
	wg.Wait()
	return errs
}

func assertOneWinner(t *testing.T, errs []error) {
	t.Helper()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		assert.ErrorIs(t, err, ErrTurnInProgress)
	}
	assert.Equal(t, 1, wins)
}

// newPgPool opens another pool on the already prepared test database.
func newPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), openPgTestURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// Two service instances share one database: while one runs a turn, the other
// rejects a prompt to that conversation; cancelling the first releases it.
func TestPg_TwoServiceInstances(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	conv := chat(t, NewPgDB(pool), user, "", "q", "a").ConversationID
	blocker := newBlockingLLM()
	first := NewService(blocker, newMockMCP(), NewPgDB(pool), testConfig)
	second := NewService(&mockLLMClient{}, newMockMCP(), NewPgDB(newPgPool(t)), testConfig)
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var firstErr error
	wg.Go(func() {
		_, firstErr = first.Chat(ctx, ChatRequest{UserID: user, ConversationID: &conv, Message: "slow"})
	})
	<-blocker.entered

	_, err := second.Chat(context.Background(), ChatRequest{UserID: user, ConversationID: &conv, Message: "other replica"})
	require.ErrorIs(t, err, ErrTurnInProgress)

	cancel()
	wg.Wait()
	require.ErrorIs(t, firstErr, context.Canceled)
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_turns WHERE conversation_id = $1 AND status = 'cancelled'`, conv))

	_, err = second.Chat(context.Background(), ChatRequest{UserID: user, ConversationID: &conv, Message: "now"})
	require.NoError(t, err, "a cancelled turn releases the conversation")
	msgs, err := NewPgDB(pool).GetMessages(context.Background(), user, conv)
	require.NoError(t, err)
	assert.Len(t, msgs, 4, "the cancelled turn never reaches the transcript")
}

// A full service turn on PostgreSQL stores its calls and tool calls.
func TestPg_ServiceTurnStoresUsage(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	first := toolCallResponse("pg_read_query", "c1")
	first.Usage = &llm.Usage{PromptTokens: 12, CompletionTokens: 3, CacheReported: true}
	svc := NewService(&mockLLMClient{responses: []*llm.Response{first, {Content: "done"}}}, newMockMCP(), NewPgDB(pool), testConfig)
	resp, err := svc.Chat(context.Background(), ChatRequest{UserID: user, Message: "q"})
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.Equal(t, 3, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls WHERE conversation_id = $1`, resp.ConversationID),
		"two chat rounds and the title call")
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_tool_calls t
		JOIN maurice_turns u ON u.id = t.turn_id WHERE u.conversation_id = $1 AND t.status = 'succeeded'`, resp.ConversationID))
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_llm_calls
		WHERE conversation_id = $1 AND input_tokens = 12 AND cache_read_input_tokens = 0`, resp.ConversationID))
}

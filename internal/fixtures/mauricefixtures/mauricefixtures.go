// Package mauricefixtures inserts Maurice users, conversations, turns,
// messages, LLM calls and tool calls with explicit timestamps, for tests of
// queries over the Maurice tables (internal/mauricereport, the GraphQL
// reporting fields). Every insert satisfies the tables' CHECK constraints:
// a terminal turn gets a completion time, a failed or cancelled one an
// error class. Only tests import this package.
package mauricefixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Turn and call statuses and the error classes the fixtures store with them.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	errorClassFailed    = "provider_error"
	errorClassCancelled = "cancelled"

	// ProviderAuthentik is the login provider of a User without one.
	ProviderAuthentik = "authentik"

	// turnDuration is how long a terminal fixture turn, call or tool call
	// took unless it names another duration.
	turnDuration = time.Second
)

// Seeder inserts fixture rows through pool, failing the test on any error.
type Seeder struct {
	t    testing.TB
	pool *pgxpool.Pool
	// keys numbers the turns' idempotency keys.
	keys int
}

// NewSeeder returns a seeder writing through pool.
func NewSeeder(t testing.TB, pool *pgxpool.Pool) *Seeder {
	return &Seeder{t: t, pool: pool}
}

// User is an app_users row. Provider defaults to ProviderAuthentik and
// Subject to Username; an empty DisplayName or Email is NULL.
type User struct {
	Instance    string
	Provider    string
	Subject     string
	Username    string
	DisplayName string
	Email       string
	FirstSeenAt time.Time
	// ProfileUpdatedAt left zero is FirstSeenAt.
	ProfileUpdatedAt time.Time
	DisabledAt       *time.Time
}

// User inserts u and returns its ID.
func (s *Seeder) User(u User) string {
	s.t.Helper()
	if u.Provider == "" {
		u.Provider = ProviderAuthentik
	}
	if u.Subject == "" {
		u.Subject = u.Username
	}
	if u.ProfileUpdatedAt.IsZero() {
		u.ProfileUpdatedAt = u.FirstSeenAt
	}
	return s.uuid(`INSERT INTO app_users (instance, auth_provider, auth_subject, username, display_name,
		email, first_seen_at, profile_updated_at, disabled_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9) RETURNING id::text`,
		u.Instance, u.Provider, u.Subject, u.Username, u.DisplayName, u.Email, u.FirstSeenAt,
		u.ProfileUpdatedAt, u.DisabledAt)
}

// Conversation is a maurice_conversations row. An empty Title is NULL; a
// zero UpdatedAt is CreatedAt.
type Conversation struct {
	UserID    string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Conversation inserts c and returns its ID.
func (s *Seeder) Conversation(c Conversation) string {
	s.t.Helper()
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	return s.uuid(`INSERT INTO maurice_conversations (user_id, title, created_at, updated_at, deleted_at)
		VALUES ($1::uuid, NULLIF($2, ''), $3, $4, $5) RETURNING id::text`,
		c.UserID, c.Title, c.CreatedAt, c.UpdatedAt, c.DeletedAt)
}

// Turn is a maurice_turns row of a conversation owned by UserID.
type Turn struct {
	ConversationID string
	UserID         string
	Number         int
	Status         string
	StartedAt      time.Time
	// Duration left zero is turnDuration; a running turn has none.
	Duration time.Duration
}

// Turn inserts t and returns its ID. A terminal turn completes Duration
// after it started.
func (s *Seeder) Turn(t Turn) string {
	s.t.Helper()
	s.keys++
	var completed *time.Time
	if t.Status != StatusRunning {
		end := t.StartedAt.Add(orDefault(t.Duration))
		completed = &end
	}
	return s.uuid(`INSERT INTO maurice_turns (conversation_id, user_id, turn_number, idempotency_key,
		request_hash, status, started_at, completed_at, error_class)
		VALUES ($1::uuid, $2::uuid, $3, $4, '\x00', $5, $6, $7, $8) RETURNING id::text`,
		t.ConversationID, t.UserID, t.Number, fmt.Sprintf("fixture-%d", s.keys), t.Status,
		t.StartedAt, completed, errorClass(t.Status))
}

// errorClass is the error class stored with a status: none for running and
// succeeded rows.
func errorClass(status string) *string {
	var class string
	switch status {
	case StatusFailed:
		class = errorClassFailed
	case StatusCancelled:
		class = errorClassCancelled
	default:
		return nil
	}
	return &class
}

// Message is a maurice_messages row of a turn; Role is system, user,
// assistant or tool.
type Message struct {
	ConversationID string
	TurnID         string
	Number         int
	Role           string
	Content        string
	// ResultToolCallID is the tool call a tool message answers; 0 is NULL.
	ResultToolCallID int64
}

// Message inserts m and returns its ID.
func (s *Seeder) Message(m Message) string {
	s.t.Helper()
	return s.uuid(`INSERT INTO maurice_messages (conversation_id, turn_id, message_number, role, content,
		result_tool_call_id)
		VALUES ($1::uuid, $2::uuid, $3, $4::chat_role, $5, NULLIF($6, 0)) RETURNING id::text`,
		m.ConversationID, m.TurnID, m.Number, m.Role, m.Content, m.ResultToolCallID)
}

// Call is a maurice_llm_calls row: a chat round of TurnID, or a title
// generation call of the conversation when TurnID is empty.
type Call struct {
	ConversationID string
	TurnID         string
	Round          int
	Provider       string
	Model          string
	StartedAt      time.Time
	// Duration left zero is turnDuration.
	Duration time.Duration
	// Failed stores a failed call (with an error class); the default is
	// succeeded.
	Failed bool
	// ForcedFinal stores a turn call as forced_final instead of chat_round.
	ForcedFinal bool
	// Token counts; nil is NULL, a count the provider did not report.
	InputTokens              *int64
	OutputTokens             *int64
	CacheCreationInputTokens *int64
	CacheReadInputTokens     *int64
	// Retained request inputs; nil is NULL. ToolDefinitions is JSON.
	SystemPrompt    *string
	Instruction     *string
	ToolDefinitions *string
}

// Call inserts c and returns its ID.
func (s *Seeder) Call(c Call) int64 {
	s.t.Helper()
	kind, turnID, round := "chat_round", &c.TurnID, &c.Round
	if c.ForcedFinal {
		kind = "forced_final"
	}
	if c.TurnID == "" {
		kind, turnID, round = "title_generation", nil, nil
	}
	status := StatusSucceeded
	if c.Failed {
		status = StatusFailed
	}
	var id int64
	err := s.pool.QueryRow(context.Background(), `INSERT INTO maurice_llm_calls (conversation_id, turn_id,
		call_kind, round_number, provider, model, status, started_at, completed_at, error_class,
		input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
		system_prompt, instruction, tool_definitions)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17::jsonb)
		RETURNING id`,
		c.ConversationID, turnID, kind, round, c.Provider, c.Model, status, c.StartedAt,
		c.StartedAt.Add(orDefault(c.Duration)), errorClass(status),
		c.InputTokens, c.OutputTokens, c.CacheCreationInputTokens, c.CacheReadInputTokens,
		c.SystemPrompt, c.Instruction, c.ToolDefinitions).Scan(&id)
	require.NoError(s.t, err)
	return id
}

// CallInput links message messageID as input number of call callID.
func (s *Seeder) CallInput(callID int64, number int, messageID string) {
	s.t.Helper()
	_, err := s.pool.Exec(context.Background(), `INSERT INTO maurice_llm_call_messages (llm_call_id,
		input_number, message_id) VALUES ($1, $2, $3::uuid)`, callID, number, messageID)
	require.NoError(s.t, err)
}

// Text returns a pointer to s, for the retained inputs of a Call.
func Text(s string) *string {
	return &s
}

// Tokens returns a pointer to n, for the token counts of a Call.
func Tokens(n int64) *int64 {
	return &n
}

// orDefault is d, or turnDuration when d is zero.
func orDefault(d time.Duration) time.Duration {
	if d == 0 {
		return turnDuration
	}
	return d
}

// ToolCall is a maurice_tool_calls row of turn TurnID. Status defaults to
// succeeded; a cancelled call never ran (no start, completion or result).
// CallID 0 is a transcript-only call (no LLM call, no status), MessageID ""
// no requesting message. The parsed arguments are ArgumentsRaw when it is
// valid JSON, NULL otherwise.
type ToolCall struct {
	TurnID       string
	CallID       int64
	MessageID    string
	Sequence     int
	Name         string
	ArgumentsRaw string
	Result       string
	Status       string
	StartedAt    time.Time
}

// ToolCall inserts c and returns its ID.
func (s *Seeder) ToolCall(c ToolCall) int64 {
	s.t.Helper()
	if c.Status == "" {
		c.Status = StatusSucceeded
	}
	var (
		status         *string
		result         = &c.Result
		started, ended = &c.StartedAt, c.StartedAt.Add(turnDuration)
		completed      = &ended
		callID         *int64
	)
	if c.CallID != 0 {
		callID, status = &c.CallID, &c.Status
	}
	if c.Status == StatusCancelled || callID == nil {
		result, started, completed = nil, nil, nil
	}
	var arguments *string
	if json.Valid([]byte(c.ArgumentsRaw)) {
		arguments = &c.ArgumentsRaw
	}
	var id int64
	err := s.pool.QueryRow(context.Background(), `INSERT INTO maurice_tool_calls (turn_id, llm_call_id,
		message_id, sequence_number, tool_name, arguments, arguments_raw, result, status, started_at, completed_at)
		VALUES ($1::uuid, $2, NULLIF($3, '')::uuid, $4, $5, $6::jsonb, $7, $8, $9, $10, $11)
		RETURNING id`,
		c.TurnID, callID, c.MessageID, c.Sequence, c.Name, arguments, c.ArgumentsRaw, result, status,
		started, completed).Scan(&id)
	require.NoError(s.t, err)
	return id
}

func (s *Seeder) uuid(sql string, args ...any) string {
	s.t.Helper()
	var id string
	require.NoError(s.t, s.pool.QueryRow(context.Background(), sql, args...).Scan(&id))
	return id
}

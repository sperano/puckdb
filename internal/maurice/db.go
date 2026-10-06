package maurice

import (
	"context"
	"errors"
	"time"

	"github.com/sperano/puckdb/internal/llm"
)

// LocalUserID owns every conversation of the single-user REPL store.
const LocalUserID = "local"

var (
	// ErrConversationNotFound is returned when a conversation does not exist
	// or belongs to another user; the two are deliberately indistinguishable.
	ErrConversationNotFound = errors.New("conversation not found")
	// ErrTurnInProgress is returned when the conversation already has a
	// running turn, or a concurrent request holds the same idempotency key.
	ErrTurnInProgress = errors.New("a turn is already running in this conversation")
	// ErrIdempotencyKeyReused is returned when an idempotency key comes back
	// with a different prompt or conversation than its first use.
	ErrIdempotencyKeyReused = errors.New("idempotency key was already used for a different request")
	// ErrTurnNotRunning is returned by FinishTurn when the turn is no longer
	// running (it finished already or was reconciled as abandoned).
	ErrTurnNotRunning = errors.New("turn is not running")
)

// DB defines the storage operations Maurice needs. Every conversation
// operation is scoped by userID. Implementations exist for PostgreSQL
// (pgdb.go, pgturn.go) and SQLite (sqlitedb.go, sqliteturn.go).
type DB interface {
	GetConversation(ctx context.Context, userID, id string) (*Conversation, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error)
	// DeleteConversation hides the conversation from its owner for good; its
	// messages, turns and usage are kept. It returns ErrConversationNotFound
	// when userID owns no such conversation.
	DeleteConversation(ctx context.Context, userID, id string) error
	// GetMessages returns the replayable transcript: the messages of
	// succeeded turns in turn order.
	GetMessages(ctx context.Context, userID, conversationID string) ([]*Message, error)
	// BeginTurn opens a turn, creating the conversation when
	// params.ConversationID is empty. A known idempotency key returns the
	// earlier turn in TurnStart.Replay instead of opening a new one.
	BeginTurn(ctx context.Context, params BeginTurnParams) (*TurnStart, error)
	// FinishTurn commits a turn atomically: its terminal status, every
	// message, and (where the backend records usage) every LLM call, call
	// input link and tool call. It returns the stored message IDs in record
	// order.
	FinishTurn(ctx context.Context, record TurnRecord) ([]string, error)
	// RecordTitle stores a title-generation call and, when Title is not
	// empty, sets the conversation title.
	RecordTitle(ctx context.Context, record TitleRecord) error
}

// TurnStatus is the lifecycle state of a turn.
type TurnStatus string

const (
	TurnRunning   TurnStatus = "running"
	TurnSucceeded TurnStatus = "succeeded"
	TurnFailed    TurnStatus = "failed"
	TurnCancelled TurnStatus = "cancelled"
)

// ErrorClass is a bounded failure label. Provider error text never goes into
// storage: it may carry prompt content or credentials.
type ErrorClass string

const (
	ErrorClassTimeout   ErrorClass = "timeout"
	ErrorClassCancelled ErrorClass = "cancelled"
	ErrorClassProvider  ErrorClass = "provider_error"
	ErrorClassTool      ErrorClass = "tool_error"
	ErrorClassInternal  ErrorClass = "internal_error"
	ErrorClassAbandoned ErrorClass = "abandoned"
)

// BeginTurnParams identifies a new turn.
type BeginTurnParams struct {
	UserID string
	// ConversationID is empty to start a new conversation.
	ConversationID string
	IdempotencyKey string
	// RequestHash fingerprints the prompt so a reused key with a different
	// prompt is rejected.
	RequestHash []byte
	// StaleAfter is how long a turn may stay running before a new request
	// treats it as abandoned.
	StaleAfter time.Duration
}

// TurnStart is the result of BeginTurn. Exactly one of Replay and a running
// turn (TurnID set, Replay nil) is returned.
type TurnStart struct {
	TurnID          string
	ConversationID  string
	TurnNumber      int
	NewConversation bool
	Replay          *TurnReplay
}

// TurnReplay is an earlier turn found by its idempotency key.
type TurnReplay struct {
	Status     TurnStatus
	ErrorClass ErrorClass
	// MessageID, Content, ToolsUsed and Warnings describe the final answer of a
	// succeeded turn.
	MessageID string
	Content   string
	ToolsUsed []string
	Warnings  []string
}

// TurnMessage is one message produced during a turn. Message 0 is the user
// prompt.
type TurnMessage struct {
	Role       string
	Content    string
	ToolCalls  []llm.ToolCall
	ToolCallID string
}

// TurnRecord is everything FinishTurn commits for one turn.
type TurnRecord struct {
	TurnID         string
	ConversationID string
	Status         TurnStatus
	ErrorClass     ErrorClass
	Messages       []TurnMessage
	Calls          []LLMCallRecord
	Warnings       []string
}

// TitleRecord is one title generation for a conversation.
type TitleRecord struct {
	UserID         string
	ConversationID string
	Title          string
	Call           LLMCallRecord
}

// CallKind distinguishes the provider calls of a turn.
type CallKind string

const (
	CallChatRound       CallKind = "chat_round"
	CallForcedFinal     CallKind = "forced_final"
	CallTitleGeneration CallKind = "title_generation"
)

// CallStatus is the outcome of one provider call.
type CallStatus string

const (
	CallSucceeded CallStatus = "succeeded"
	CallFailed    CallStatus = "failed"
)

// LLMCallRecord is one provider completion request and its outcome.
type LLMCallRecord struct {
	Kind CallKind
	// Round is nil only for title generation.
	Round             *int
	Provider          string
	Model             string
	ProviderRequestID string
	Status            CallStatus
	FinishReason      string
	ErrorClass        ErrorClass
	StartedAt         time.Time
	CompletedAt       time.Time
	Usage             TokenUsage
	SystemPrompt      *string
	Instruction       *string
	ToolDefinitions   []llm.Tool
	// Inputs are the request messages after the system prompt, in order.
	Inputs    []MessageRef
	ToolCalls []ToolCallRecord
}

// TokenUsage holds provider-reported token counts. A nil count was not
// reported, which differs from zero.
type TokenUsage struct {
	Input         *int64
	Output        *int64
	CacheCreation *int64
	CacheRead     *int64
}

// MessageRef points at a request message: a stored message of an earlier turn
// (MessageID) or a message of the turn being recorded (TurnIndex into
// TurnRecord.Messages).
type MessageRef struct {
	MessageID string
	TurnIndex int
}

// ToolCallStatus is the outcome of one tool call the model requested.
type ToolCallStatus string

const (
	ToolSucceeded  ToolCallStatus = "succeeded"
	ToolError      ToolCallStatus = "tool_error"
	ToolParseError ToolCallStatus = "parse_error"
	// ToolCancelled marks a requested call that never ran.
	ToolCancelled ToolCallStatus = "cancelled"
)

// ToolCallRecord is one tool call requested by an LLM call's response.
type ToolCallRecord struct {
	ProviderToolCallID string
	Name               string
	ArgumentsRaw       string
	// Result is nil for a cancelled call.
	Result      *string
	Status      ToolCallStatus
	StartedAt   *time.Time
	CompletedAt *time.Time
}

package maurice

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/mcp"
)

const (
	DefaultMaxToolRounds = 10
	DefaultMaxHistory    = 50
	DefaultMaxTokens     = 4096

	// maxConcurrentTitleGen bounds the number of in-flight background
	// title-generation goroutines so a burst of new conversations cannot spawn
	// unbounded goroutines. Excess generations are skipped (title is best-effort).
	maxConcurrentTitleGen = 4
)

// Service is the Maurice AI chat orchestration layer. Every operation is
// scoped by the user that owns the conversation.
type Service interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	GetConversation(ctx context.Context, userID, id string) (*Conversation, []*Message, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, userID, id string) error
	// Close cancels any in-flight background title generation and waits for
	// those goroutines to return. It must be called once, after the final Chat.
	Close() error
}

// ChatRequest is one user prompt.
type ChatRequest struct {
	UserID string
	// ConversationID is nil to start a new conversation.
	ConversationID *string
	Message        string
	// IdempotencyKey makes retries safe: the same key returns the original
	// turn instead of running the prompt again. Empty means the caller does
	// not retry; a fresh key is generated.
	IdempotencyKey string
}

// ChatResponse is returned by Chat with the assistant's final answer.
type ChatResponse struct {
	ConversationID string
	MessageID      string
	Content        string
	ToolsUsed      []string
	Warnings       []string
}

// Conversation represents a stored conversation.
type Conversation struct {
	ID        string
	Title     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message represents a stored chat message.
type Message struct {
	ID         string
	Role       string
	Content    string
	ToolCalls  []llm.ToolCall
	ToolCallID string
	CreatedAt  time.Time
}

// ServiceConfig configures NewService. Provider and Model name the LLM the
// client talks to; they are recorded on every LLM call. Zero limits fall back
// to the defaults.
type ServiceConfig struct {
	Provider      string
	Model         string
	MaxHistory    int
	MaxTokens     int
	MaxToolRounds int
}

type service struct {
	llmClient     llm.Client
	toolCache     *mcp.ToolCache
	db            DB
	provider      string
	model         string
	maxHistory    int
	maxTokens     int
	maxToolRounds int
	now           func() time.Time
	// persistTimeout and releaseTimeout bound committing and releasing a
	// turn (turnPersistTimeout, turnReleaseTimeout).
	persistTimeout time.Duration
	releaseTimeout time.Duration

	// titleGroup bounds (via SetLimit) and tracks the detached goroutines that
	// generate conversation titles, so shutdown can await them.
	titleGroup *errgroup.Group
	// shutdownCtx is cancelled by Close to signal in-flight title generation to
	// abandon its LLM/DB work.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewService creates a new Maurice service.
func NewService(llmClient llm.Client, mcpClient mcp.Client, db DB, cfg ServiceConfig) Service {
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = DefaultMaxHistory
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = DefaultMaxToolRounds
	}
	titleGroup := new(errgroup.Group)
	titleGroup.SetLimit(maxConcurrentTitleGen)
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &service{
		llmClient:      llmClient,
		toolCache:      mcp.NewToolCache(mcpClient),
		db:             db,
		provider:       cfg.Provider,
		model:          cfg.Model,
		maxHistory:     cfg.MaxHistory,
		maxTokens:      cfg.MaxTokens,
		maxToolRounds:  cfg.MaxToolRounds,
		now:            time.Now,
		persistTimeout: turnPersistTimeout,
		releaseTimeout: turnReleaseTimeout,
		titleGroup:     titleGroup,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
}

// Chat runs one turn: it opens the turn (or finds the earlier one with the
// same idempotency key), runs the LLM tool loop, and commits the turn's
// messages and usage atomically. Nothing is written mid-turn, so a crash
// during the loop leaves no partial transcript; a failed turn keeps its
// prompts and usage but never becomes replayable history.
func (s *service) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	params, err := s.beginTurnParams(req)
	if err != nil {
		return nil, err
	}
	start, err := s.db.BeginTurn(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("begin turn: %w", err)
	}
	if start.Replay != nil {
		return replayResponse(start)
	}
	return s.runTurn(ctx, req, start)
}

// Close cancels in-flight background title generation and waits for the
// goroutines to return. It is safe to call once; no Chat may run after Close.
func (s *service) Close() error {
	s.shutdownCancel()
	return s.titleGroup.Wait()
}

func (s *service) GetConversation(ctx context.Context, userID, id string) (*Conversation, []*Message, error) {
	conv, err := s.db.GetConversation(ctx, userID, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get conversation: %w", err)
	}

	msgs, err := s.db.GetMessages(ctx, userID, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get messages: %w", err)
	}

	return conv, msgs, nil
}

func (s *service) ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = s.maxHistory
	}
	return s.db.ListConversations(ctx, userID, limit)
}

func (s *service) DeleteConversation(ctx context.Context, userID, id string) error {
	return s.db.DeleteConversation(ctx, userID, id)
}

// loadHistory returns the request messages for a turn (system prompt, capped
// history, current prompt) and, aligned with them, the stored ID of each
// history message ("" for the system prompt and the unsaved prompt).
func (s *service) loadHistory(ctx context.Context, userID, convID, currentUserMessage string) ([]llm.Message, []string, error) {
	msgs, err := s.db.GetMessages(ctx, userID, convID)
	if err != nil {
		return nil, nil, err
	}

	// Append the current user turn (not yet persisted) as the newest message
	// so it participates in history capping exactly as a persisted message
	// would.
	msgs = append(msgs, &Message{Role: "user", Content: currentUserMessage})

	// Cap history, ensuring we don't split a tool_use/tool_result pair.
	// After slicing, skip any leading "tool" messages whose corresponding
	// assistant tool_use block was cut off.
	if len(msgs) > s.maxHistory {
		msgs = msgs[len(msgs)-s.maxHistory:]
	}
	for len(msgs) > 0 && msgs[0].Role == "tool" {
		msgs = msgs[1:]
	}

	messages := make([]llm.Message, 0, len(msgs)+1)
	ids := make([]string, 0, len(msgs)+1)
	messages = append(messages, llm.Message{Role: "system", Content: SystemPrompt()})
	ids = append(ids, "")
	for _, m := range msgs {
		messages = append(messages, llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		})
		ids = append(ids, m.ID)
	}
	return messages, ids, nil
}

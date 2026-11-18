// Package maurice orchestrates the Maurice AI chat: Service runs the
// prompt/tool-call loop (internal/llm for completions, internal/mcp for
// tool discovery and calls) and persists conversations, turns and messages
// through the DB interface, implemented by a SQLite store (the REPL,
// single local user) and a PostgreSQL store (the API, one user per login
// provider identity and API instance via internal/appuser). LLM-call and
// tool-call usage is persisted only by the PostgreSQL store; SQLite keeps
// conversations, turns and messages alone. RunREPL (repl*.go) is the
// `maurice` CLI command's REPL loop, its streams injectable through
// REPLConfig.
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
	DefaultMaxToolRounds = 25
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
	// Chat runs one turn and waits for its answer. The turn runs detached
	// from ctx: when ctx ends first, Chat returns ctx's error and the turn
	// still commits.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// StartChat opens a turn and returns at once while it runs detached; an
	// idempotency key resent with the same prompt returns its turn, running
	// or finished, and with another prompt ErrIdempotencyKeyReused.
	StartChat(ctx context.Context, req ChatRequest) (*TurnRef, error)
	// GetTurn reads one of userID's turns; any other is ErrTurnNotFound.
	GetTurn(ctx context.Context, userID, turnID string) (*Turn, error)
	GetConversation(ctx context.Context, userID, id string) (*Conversation, []*Message, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, userID, id string) error
	// Model is the LLM the service was configured with (ServiceConfig's
	// Provider and Model), not what a provider reports per call.
	Model() ModelInfo
	// Close cancels the running turns and any in-flight background title
	// generation and waits for them to return; a cancelled turn commits as
	// cancelled. It must be called once; Chat and StartChat fail with
	// ErrServiceClosed once it has begun.
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

// TeamLister reports the Yahoo fantasy teams a user manages, for the system
// prompt (SystemPrompt). Implementations limit the result to the leagues
// the instance allows; an empty allowlist means every league
// (internal/yahoolink.PGStore.ListUserTeams, wired in by the caller).
type TeamLister interface {
	ListUserTeams(ctx context.Context, userID string) ([]UserTeam, error)
}

// UserTeam is one Yahoo team a user manages, named in the system prompt.
// Season is the league's Yahoo season, its start year (2026 for 2026-27).
type UserTeam struct {
	LeagueID   int32
	TeamID     int32
	TeamName   string
	LeagueName string
	Season     int32
}

// ServiceConfig configures NewService. Provider and Model name the LLM the
// client talks to; they are recorded on every LLM call. Zero limits fall back
// to the defaults. Teams is nil when the instance has no Yahoo team linking
// (the REPL's single local user): the system prompt then names no team.
type ServiceConfig struct {
	Provider      string
	Model         string
	MaxHistory    int
	MaxTokens     int
	MaxToolRounds int
	Teams         TeamLister
}

// ModelInfo names a configured LLM.
type ModelInfo struct {
	Provider string
	Model    string
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
	// turnTimeout, persistTimeout and releaseTimeout bound running,
	// committing and releasing a turn (turnTimeout, turnPersistTimeout,
	// turnReleaseTimeout).
	turnTimeout    time.Duration
	persistTimeout time.Duration
	releaseTimeout time.Duration

	// runner tracks the detached turns (launchTurn).
	runner *turnRunner
	// titleGroup bounds (via SetLimit) and tracks the detached goroutines that
	// generate conversation titles, so shutdown can await them.
	titleGroup *errgroup.Group
	// shutdownCtx is cancelled by Close to signal running turns and in-flight
	// title generation to abandon their LLM/DB work.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	teams          TeamLister
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
		turnTimeout:    turnTimeout,
		persistTimeout: turnPersistTimeout,
		releaseTimeout: turnReleaseTimeout,
		runner:         newTurnRunner(),
		titleGroup:     titleGroup,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
		teams:          cfg.Teams,
	}
}

// Chat runs one turn: it opens the turn (or finds the earlier one with the
// same idempotency key), runs the LLM tool loop detached from ctx
// (launchTurn), and waits for the turn to commit its messages and usage
// atomically. Mid-turn, only each starting tool's name is written to the turn
// row (recordToolStart), so a crash during the loop leaves no partial
// transcript; a failed turn keeps its prompts and usage but never becomes
// replayable history.
func (s *service) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start, release, err := s.beginTurn(ctx, req, false)
	if err != nil {
		return nil, err
	}
	if start.Replay != nil {
		return replayResponse(start)
	}
	done, err := s.launchTurn(ctx, req, start, release)
	if err != nil {
		return nil, err
	}
	select {
	case res := <-done:
		return res.resp, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close refuses new turns, cancels the running turns and in-flight title
// generation, and waits for them to return. Turns are awaited first: a turn
// that commits during Close may still start a title generation, which the
// cancelled shutdownCtx ends at once.
func (s *service) Close() error {
	s.runner.close()
	s.shutdownCancel()
	s.runner.wg.Wait()
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

func (s *service) Model() ModelInfo {
	return ModelInfo{Provider: s.provider, Model: s.model}
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

	teams, err := s.userTeams(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	messages := make([]llm.Message, 0, len(msgs)+1)
	ids := make([]string, 0, len(msgs)+1)
	messages = append(messages, llm.Message{Role: "system", Content: SystemPrompt(teams)})
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

// userTeams returns the Yahoo teams userID manages, or nil when the service
// has no TeamLister configured.
func (s *service) userTeams(ctx context.Context, userID string) ([]UserTeam, error) {
	if s.teams == nil {
		return nil, nil
	}
	teams, err := s.teams.ListUserTeams(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list user's yahoo teams: %w", err)
	}
	return teams, nil
}

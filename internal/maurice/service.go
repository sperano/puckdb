package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
	"github.com/sperano/puckdb/internal/mcp"
)

const (
	DefaultMaxToolRounds = 10
	DefaultMaxHistory    = 50
	DefaultMaxTokens     = 4096
	titleGenerationHint  = "Summarize this conversation in 5 words or fewer. Reply with only the title, no quotes."

	// maxConcurrentTitleGen bounds the number of in-flight background
	// title-generation goroutines so a burst of new conversations cannot spawn
	// unbounded goroutines. Excess generations are skipped (title is best-effort).
	maxConcurrentTitleGen = 4
	// titleGenTimeout caps how long a detached title generation may run before
	// its context is cancelled.
	titleGenTimeout = 30 * time.Second
)

// Service is the Maurice AI chat orchestration layer.
type Service interface {
	Chat(ctx context.Context, conversationID *string, message string) (*ChatResponse, error)
	GetConversation(ctx context.Context, id string) (*Conversation, []*Message, error)
	ListConversations(ctx context.Context, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
	// Close cancels any in-flight background title generation and waits for
	// those goroutines to return. It must be called once, after the final Chat.
	Close() error
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

type service struct {
	llmClient     llm.Client
	mcpClient     mcp.Client
	toolCache     *mcp.ToolCache
	db            DB
	maxHistory    int
	maxTokens     int
	maxToolRounds int

	// titleGroup bounds (via SetLimit) and tracks the detached goroutines that
	// generate conversation titles, so shutdown can await them.
	titleGroup *errgroup.Group
	// shutdownCtx is cancelled by Close to signal in-flight title generation to
	// abandon its LLM/DB work.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewService creates a new Maurice service.
func NewService(llmClient llm.Client, mcpClient mcp.Client, db DB, maxHistory, maxTokens, maxToolRounds int) Service {
	if maxHistory <= 0 {
		maxHistory = DefaultMaxHistory
	}
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	if maxToolRounds <= 0 {
		maxToolRounds = DefaultMaxToolRounds
	}
	titleGroup := new(errgroup.Group)
	titleGroup.SetLimit(maxConcurrentTitleGen)
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &service{
		llmClient:      llmClient,
		mcpClient:      mcpClient,
		toolCache:      mcp.NewToolCache(mcpClient),
		db:             db,
		maxHistory:     maxHistory,
		maxTokens:      maxTokens,
		maxToolRounds:  maxToolRounds,
		titleGroup:     titleGroup,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
}

func (s *service) Chat(ctx context.Context, conversationID *string, message string) (*ChatResponse, error) {
	convID, isNew, err := s.resolveConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	log.Debug().Str("conversation", convID).Bool("new", isNew).Str("message", truncateLog(message, maxLogMessageLen)).Msg("maurice chat started")

	// Load prior history and append the current user turn. The user message and
	// every message produced during this turn are buffered (see pending, below)
	// and persisted together only after the turn produces a final answer (B17).
	// Nothing is written mid-turn, so a crash during the LLM/tool loop leaves no
	// partial user/tool-result state to poison loadHistory on resume.
	history, err := s.loadHistory(ctx, convID, message)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	log.Debug().Str("conversation", convID).Int("history_messages", len(history)).Msg("loaded conversation history")

	// Get tool definitions. Partial discovery is usable, but the model and
	// caller must both see the degraded-data warning. If every configured
	// server failed, do not let the model produce an ungrounded answer.
	llmTools, err := s.toolCache.GetLLMTools(ctx)
	var warnings []string
	if err != nil {
		if len(llmTools) == 0 {
			return nil, fmt.Errorf("discover MCP tools: %w", err)
		}
		warning := fmt.Sprintf("Some configured data sources are unavailable: %v", err)
		warnings = []string{warning}
		history[0].Content += "\n\nAvailability warning: " + warning +
			" Use only the available tools and explicitly disclose this limitation in the answer."
		log.Warn().Err(err).Int("tools", len(llmTools)).Msg("using partial MCP tool discovery")
	} else {
		log.Debug().Int("tools", len(llmTools)).Msg("loaded MCP tools for LLM")
	}

	// Run the multi-round LLM tool-use loop. agentloop.Run owns the per-round
	// mechanics Chat used to hand-roll (see llm/agentloop, whose package doc
	// calls out this migration). The executor bridges each tool call to the MCP
	// client; it formats a tool failure into the result string and returns a nil
	// error so a failed tool surfaces to the model instead of aborting the turn
	// — exactly the historical behavior.
	historyLen := len(history)
	res, runErr := agentloop.Run(ctx, s.llmClient, history, s.mcpToolExecutor(convID), agentloop.Config{
		Tools:         llmTools,
		MaxToolRounds: s.maxToolRounds,
		MaxTokens:     s.maxTokens,
	})
	if runErr != nil {
		// A mid-turn failure persists nothing (B17): not the user message, not
		// any partial tool result. loadHistory on resume is therefore never
		// poisoned by a half-written turn. runErr already carries the
		// "LLM completion (round N)" / "tool executor" context from agentloop.
		return nil, runErr
	}
	if res.Final != nil {
		logLLMResponse(convID, res.Rounds, res.Final)
	}

	// Buffered turn writes, persisted atomically after the loop (B17). The user
	// message is first so persisted ordering matches the pre-buffering behavior.
	pending := []pendingMessage{{
		params: CreateMessageParams{ConversationID: convID, Role: "user", Content: message},
	}}
	// The messages agentloop appended beyond our input history are this turn's
	// assistant(tool_calls) and tool-result messages, in execution order. The
	// terminating text answer is in res.Final, not in res.Messages.
	for _, m := range res.Messages[historyLen:] {
		pending = append(pending, pendingFromTurnMessage(convID, m))
	}

	var toolsUsed []string
	for _, a := range res.Audit {
		toolsUsed = append(toolsUsed, a.Call.Function.Name)
	}

	// Resolve the final assistant answer. A terminating response with no tool
	// calls IS the final answer, even when its content is empty — an empty final
	// reply is legitimate and must not be treated as "no answer yet" (the old
	// code used empty content as a loop-exhaustion sentinel, which forced a
	// spurious extra completion). Only a genuine max-rounds termination, where
	// res.Final still requests tools, triggers a forced final completion.
	var finalContent string
	if res.Final != nil && !res.Final.HasToolCalls() {
		finalContent = res.Final.Content
		log.Debug().Str("conversation", convID).Str("content", truncateLog(finalContent, maxLogMessageLen)).Msg("LLM final response (no tool calls)")
		pending = append(pending, pendingMessage{
			params:  CreateMessageParams{ConversationID: convID, Role: "assistant", Content: finalContent},
			isFinal: true,
		})
	} else {
		// Exhausted all tool rounds without a final text response; make one last
		// LLM call with no tools to force a text answer.
		log.Warn().Str("conversation", convID).Int("max_rounds", s.maxToolRounds).Msg("tool rounds exhausted, forcing final response without tools")
		req := &llm.Request{
			Messages:  res.Messages,
			MaxTokens: s.maxTokens,
		}
		resp, err := s.llmClient.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM forced final completion: %w", err)
		}
		finalContent = resp.Content
		pending = append(pending, pendingMessage{
			params:  CreateMessageParams{ConversationID: convID, Role: "assistant", Content: finalContent},
			isFinal: true,
		})
	}

	// Persist the whole turn now that a final answer exists.
	finalMessageID, err := s.persistTurn(ctx, pending)
	if err != nil {
		return nil, err
	}

	// Auto-generate title for new conversations
	if isNew && finalContent != "" {
		s.startTitleGeneration(ctx, convID, message, finalContent)
	}

	log.Debug().Str("conversation", convID).Int("tools_used", len(toolsUsed)).Strs("tools", toolsUsed).Msg("maurice chat completed")

	return &ChatResponse{
		ConversationID: convID,
		MessageID:      finalMessageID,
		Content:        finalContent,
		ToolsUsed:      toolsUsed,
		Warnings:       warnings,
	}, nil
}

// mcpToolExecutor returns an agentloop.ToolExecutor that resolves each tool call
// against the MCP client. A tool-call error is formatted into the returned
// result string with a nil error, so the failure is surfaced to the model on the
// next round rather than aborting the turn (the historical Chat behavior).
func (s *service) mcpToolExecutor(convID string) agentloop.ToolExecutor {
	return func(ctx context.Context, tc llm.ToolCall) (string, error) {
		log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Str("call_id", tc.ID).Msg("calling MCP tool")
		log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("arguments", tc.Function.Arguments).Msg("MCP tool arguments")

		result, err := s.mcpClient.CallTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
		if err != nil {
			resultContent := fmt.Sprintf("Error calling tool %s: %s", tc.Function.Name, err.Error())
			log.Warn().Err(err).Str("tool", tc.Function.Name).Msg("tool call failed")
			return resultContent, nil
		}
		log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Bool("is_error", result.IsError).Int("result_len", len(result.Content)).Msg("MCP tool returned")
		log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("result", truncateLog(result.Content, maxLogTraceLen)).Msg("MCP tool result content")
		return result.Content, nil
	}
}

// pendingFromTurnMessage maps a turn message that agentloop appended (an
// assistant message carrying tool calls, or a tool-result message) to a buffered
// write. agentloop only ever appends "assistant" and "tool" roles.
func pendingFromTurnMessage(convID string, m llm.Message) pendingMessage {
	if m.Role == "tool" {
		return pendingMessage{
			params: CreateMessageParams{ConversationID: convID, Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID},
		}
	}
	return pendingMessage{
		params: CreateMessageParams{ConversationID: convID, Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls},
	}
}

// pendingMessage is a buffered turn write. isFinal marks the assistant message
// whose stored ID is returned as the ChatResponse.MessageID.
type pendingMessage struct {
	params  CreateMessageParams
	isFinal bool
}

// persistTurn writes the buffered turn messages atomically via CreateMessages
// and returns the stored ID of the final assistant message. Because the whole
// turn commits in one transaction, a crash mid-flush can never leave a partial
// turn in the store to poison loadHistory on resume (B17).
func (s *service) persistTurn(ctx context.Context, pending []pendingMessage) (string, error) {
	params := make([]CreateMessageParams, len(pending))
	finalIdx := -1
	for i, pm := range pending {
		params[i] = pm.params
		if pm.isFinal {
			finalIdx = i
		}
	}

	created, err := s.db.CreateMessages(ctx, params)
	if err != nil {
		return "", fmt.Errorf("persist turn: %w", err)
	}
	if finalIdx >= 0 && finalIdx < len(created) {
		return created[finalIdx].ID, nil
	}
	return "", nil
}

// startTitleGeneration launches a bounded, detached title generation. It
// derives the context from context.WithoutCancel(ctx) so the title survives the
// request returning, but bounds it with titleGenTimeout and ties it to
// shutdownCtx so Close can abandon it. Concurrency is capped by the errgroup's
// limit; if the cap is reached the (best-effort) title is skipped.
func (s *service) startTitleGeneration(ctx context.Context, convID, userMsg, assistantMsg string) {
	titleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleGenTimeout)
	started := s.titleGroup.TryGo(func() error {
		stop := context.AfterFunc(s.shutdownCtx, cancel)
		defer stop()
		defer cancel()
		s.generateTitle(titleCtx, convID, userMsg, assistantMsg)
		return nil
	})
	if !started {
		cancel()
		log.Debug().Str("conversation", convID).Msg("title generation skipped: concurrency limit reached")
	}
}

// Close cancels in-flight background title generation and waits for the
// goroutines to return. It is safe to call once; no Chat may run after Close.
func (s *service) Close() error {
	s.shutdownCancel()
	return s.titleGroup.Wait()
}

func (s *service) GetConversation(ctx context.Context, id string) (*Conversation, []*Message, error) {
	conv, err := s.db.GetConversation(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get conversation: %w", err)
	}

	msgs, err := s.db.GetMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get messages: %w", err)
	}

	return conv, msgs, nil
}

func (s *service) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = s.maxHistory
	}
	return s.db.ListConversations(ctx, limit)
}

func (s *service) DeleteConversation(ctx context.Context, id string) error {
	return s.db.DeleteConversation(ctx, id)
}

// resolveConversation returns the ID for the conversation, creating a new one if needed.
func (s *service) resolveConversation(ctx context.Context, conversationID *string) (string, bool, error) {
	if conversationID != nil {
		return *conversationID, false, nil
	}
	conv, err := s.db.CreateConversation(ctx)
	if err != nil {
		return "", false, fmt.Errorf("create conversation: %w", err)
	}
	return conv.ID, true, nil
}

func (s *service) loadHistory(ctx context.Context, convID, currentUserMessage string) ([]llm.Message, error) {
	msgs, err := s.db.GetMessages(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Append the current user turn (not yet persisted — see Chat/B17) as the
	// newest message so it participates in history capping exactly as a
	// persisted message would have before buffering.
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

	// System prompt + history messages
	messages := make([]llm.Message, 0, len(msgs)+1)
	messages = append(messages, llm.Message{Role: "system", Content: SystemPrompt()})

	for _, m := range msgs {
		messages = append(messages, llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		})
	}

	return messages, nil
}

// generateTitle asks the LLM to create a short title for the conversation.
func (s *service) generateTitle(ctx context.Context, convID, userMsg, assistantMsg string) {
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: userMsg},
			{Role: "assistant", Content: assistantMsg},
			{Role: "user", Content: titleGenerationHint},
		},
		MaxTokens: 20,
	}

	resp, err := s.llmClient.Complete(ctx, req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate conversation title")
		return
	}

	title := resp.Content
	if title == "" {
		return
	}

	if err := s.db.UpdateConversationTitle(ctx, convID, title); err != nil {
		log.Warn().Err(err).Msg("failed to update conversation title")
	}
}

// Logging helpers

const (
	maxLogMessageLen = 200  // truncation limit for Debug-level message content
	maxLogTraceLen   = 2000 // truncation limit for Trace-level full payloads
)

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func logLLMResponse(convID string, round int, resp *llm.Response) {
	evt := log.Debug().Str("conversation", convID).Int("round", round)
	if resp.Usage != nil {
		evt = evt.Int("prompt_tokens", resp.Usage.PromptTokens).
			Int("completion_tokens", resp.Usage.CompletionTokens).
			Int("total_tokens", resp.Usage.TotalTokens)
	}
	if resp.FinishReason != "" {
		evt = evt.Str("finish_reason", resp.FinishReason)
	}
	evt.Str("model", resp.Model).Msg("LLM response received")
}

package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/mcp"
)

const (
	MaxToolRounds       = 10
	DefaultMaxHistory   = 50
	DefaultMaxTokens    = 4096
	titleGenerationHint = "Summarize this conversation in 5 words or fewer. Reply with only the title, no quotes."
)

// Service is the Maurice AI chat orchestration layer.
type Service interface {
	Chat(ctx context.Context, conversationID *string, message string) (*ChatResponse, error)
	GetConversation(ctx context.Context, id string) (*Conversation, []*Message, error)
	ListConversations(ctx context.Context, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
}

// ChatResponse is returned by Chat with the assistant's final answer.
type ChatResponse struct {
	ConversationID string
	MessageID      string
	Content        string
	ToolsUsed      []string
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
	llmClient  llm.Client
	mcpClient  mcp.Client
	toolCache  *mcp.ToolCache
	db         DB
	maxHistory int
	maxTokens  int
}

// NewService creates a new Maurice service.
func NewService(llmClient llm.Client, mcpClient mcp.Client, db DB, maxHistory, maxTokens int) Service {
	if maxHistory <= 0 {
		maxHistory = DefaultMaxHistory
	}
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	return &service{
		llmClient:  llmClient,
		mcpClient:  mcpClient,
		toolCache:  mcp.NewToolCache(mcpClient),
		db:         db,
		maxHistory: maxHistory,
		maxTokens:  maxTokens,
	}
}

func (s *service) Chat(ctx context.Context, conversationID *string, message string) (*ChatResponse, error) {
	convID, isNew, err := s.resolveConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	log.Debug().Str("conversation", convID).Bool("new", isNew).Str("message", truncateLog(message, maxLogMessageLen)).Msg("maurice chat started")

	// Store user message
	if _, err := s.db.CreateMessage(ctx, CreateMessageParams{
		ConversationID: convID, Role: "user", Content: message,
	}); err != nil {
		return nil, fmt.Errorf("store user message: %w", err)
	}

	// Load history
	history, err := s.loadHistory(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	log.Debug().Str("conversation", convID).Int("history_messages", len(history)).Msg("loaded conversation history")

	// Get tool definitions
	llmTools, err := s.toolCache.GetLLMTools(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load MCP tools, continuing without tools")
		llmTools = nil
	} else {
		log.Debug().Int("tools", len(llmTools)).Msg("loaded MCP tools for LLM")
	}

	// Conversation loop with tool calls
	var toolsUsed []string
	var finalContent string
	var finalMessageID string

	for round := range MaxToolRounds {
		req := &llm.Request{
			Messages:  history,
			Tools:     llmTools,
			MaxTokens: s.maxTokens,
		}

		log.Debug().Str("conversation", convID).Int("round", round).Int("messages", len(history)).Int("tools", len(llmTools)).Msg("sending LLM request")
		log.Trace().Str("conversation", convID).Int("round", round).Func(func(e *zerolog.Event) {
			for i, m := range history {
				e.Str(fmt.Sprintf("msg[%d].role", i), m.Role)
				e.Str(fmt.Sprintf("msg[%d].content", i), truncateLog(m.Content, maxLogMessageLen))
			}
		}).Msg("LLM request messages")

		resp, err := s.llmClient.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM completion (round %d): %w", round, err)
		}

		logLLMResponse(convID, round, resp)

		if !resp.HasToolCalls() {
			// Final text response
			finalContent = resp.Content
			log.Debug().Str("conversation", convID).Int("round", round).Str("content", truncateLog(finalContent, maxLogMessageLen)).Msg("LLM final response (no tool calls)")
			msg, err := s.db.CreateMessage(ctx, CreateMessageParams{
				ConversationID: convID, Role: "assistant", Content: finalContent,
			})
			if err != nil {
				return nil, fmt.Errorf("store assistant message: %w", err)
			}
			finalMessageID = msg.ID
			break
		}

		// Store assistant message with tool calls
		toolNames := make([]string, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			toolNames[i] = tc.Function.Name
		}
		log.Debug().Str("conversation", convID).Int("round", round).Strs("tool_calls", toolNames).Msg("LLM requested tool calls")

		if _, err := s.db.CreateMessage(ctx, CreateMessageParams{
			ConversationID: convID, Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls,
		}); err != nil {
			return nil, fmt.Errorf("store assistant tool_calls message: %w", err)
		}

		// Add assistant message to history
		history = append(history, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Function.Name)

			log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Str("call_id", tc.ID).Msg("calling MCP tool")
			log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("arguments", tc.Function.Arguments).Msg("MCP tool arguments")

			result, err := s.mcpClient.CallTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			var resultContent string
			if err != nil {
				resultContent = fmt.Sprintf("Error calling tool %s: %s", tc.Function.Name, err.Error())
				log.Warn().Err(err).Str("tool", tc.Function.Name).Msg("tool call failed")
			} else {
				resultContent = result.Content
				log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Bool("is_error", result.IsError).Int("result_len", len(resultContent)).Msg("MCP tool returned")
				log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("result", truncateLog(resultContent, maxLogTraceLen)).Msg("MCP tool result content")
			}

			// Store tool result message
			if _, err := s.db.CreateMessage(ctx, CreateMessageParams{
				ConversationID: convID, Role: "tool", Content: resultContent, ToolCallID: tc.ID,
			}); err != nil {
				return nil, fmt.Errorf("store tool result: %w", err)
			}

			// Add tool result to history
			history = append(history, llm.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
			})
		}
	}

	// Auto-generate title for new conversations
	if isNew && finalContent != "" {
		go s.generateTitle(context.Background(), convID, message, finalContent)
	}

	log.Debug().Str("conversation", convID).Int("tools_used", len(toolsUsed)).Strs("tools", toolsUsed).Msg("maurice chat completed")

	return &ChatResponse{
		ConversationID: convID,
		MessageID:      finalMessageID,
		Content:        finalContent,
		ToolsUsed:      toolsUsed,
	}, nil
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

func (s *service) loadHistory(ctx context.Context, convID string) ([]llm.Message, error) {
	msgs, err := s.db.GetMessages(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Cap history
	if len(msgs) > s.maxHistory {
		msgs = msgs[len(msgs)-s.maxHistory:]
	}

	// System prompt + history messages
	messages := make([]llm.Message, 0, len(msgs)+1)
	messages = append(messages, llm.Message{Role: "system", Content: SystemPrompt})

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

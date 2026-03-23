package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/mcp"
	"github.com/sperano/puckdb/sqlcdb"
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

// DB defines the database operations Maurice needs.
// This allows mocking in tests without a real database.
type DB interface {
	CreateConversation(ctx context.Context, title pgtype.Text) (sqlcdb.MauriceConversation, error)
	GetConversation(ctx context.Context, id pgtype.UUID) (sqlcdb.MauriceConversation, error)
	UpdateConversationTitle(ctx context.Context, arg sqlcdb.UpdateConversationTitleParams) error
	ListConversations(ctx context.Context, limit int32) ([]sqlcdb.MauriceConversation, error)
	DeleteConversation(ctx context.Context, id pgtype.UUID) error
	CreateMessage(ctx context.Context, arg sqlcdb.CreateMessageParams) (sqlcdb.MauriceMessage, error)
	GetMessagesByConversation(ctx context.Context, conversationID pgtype.UUID) ([]sqlcdb.MauriceMessage, error)
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
	convUUID, isNew, err := s.resolveConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	// Store user message
	if _, err := s.createMessage(ctx, convUUID, "user", message, nil, ""); err != nil {
		return nil, fmt.Errorf("store user message: %w", err)
	}

	// Load history
	history, err := s.loadHistory(ctx, convUUID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	// Get tool definitions
	llmTools, err := s.toolCache.GetLLMTools(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load MCP tools, continuing without tools")
		llmTools = nil
	}

	// Conversation loop with tool calls
	var toolsUsed []string
	var finalContent string
	var finalMessageID string

	for round := range MaxToolRounds {
		req := &llm.ChatCompletionRequest{
			Messages:  history,
			Tools:     llmTools,
			MaxTokens: s.maxTokens,
		}

		resp, err := s.llmClient.ChatCompletion(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM completion (round %d): %w", round, err)
		}

		if !resp.HasToolCalls() {
			// Final text response
			finalContent = resp.FirstContent()
			msg, err := s.createMessage(ctx, convUUID, "assistant", finalContent, nil, "")
			if err != nil {
				return nil, fmt.Errorf("store assistant message: %w", err)
			}
			finalMessageID = uuidToString(msg.ID)
			break
		}

		// Store assistant message with tool calls
		assistantMsg := resp.Choices[0].Message
		if _, err := s.createMessage(ctx, convUUID, "assistant", assistantMsg.Content, assistantMsg.ToolCalls, ""); err != nil {
			return nil, fmt.Errorf("store assistant tool_calls message: %w", err)
		}

		// Add assistant message to history
		history = append(history, llm.Message{
			Role:      "assistant",
			Content:   assistantMsg.Content,
			ToolCalls: assistantMsg.ToolCalls,
		})

		// Execute each tool call
		for _, tc := range assistantMsg.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Function.Name)

			result, err := s.mcpClient.CallTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			var resultContent string
			if err != nil {
				resultContent = fmt.Sprintf("Error calling tool %s: %s", tc.Function.Name, err.Error())
				log.Warn().Err(err).Str("tool", tc.Function.Name).Msg("tool call failed")
			} else {
				resultContent = result.Content
			}

			// Store tool result message
			if _, err := s.createMessage(ctx, convUUID, "tool", resultContent, nil, tc.ID); err != nil {
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
		go s.generateTitle(context.Background(), convUUID, message, finalContent)
	}

	return &ChatResponse{
		ConversationID: uuidToString(convUUID),
		MessageID:      finalMessageID,
		Content:        finalContent,
		ToolsUsed:      toolsUsed,
	}, nil
}

func (s *service) GetConversation(ctx context.Context, id string) (*Conversation, []*Message, error) {
	uuid, err := parseUUID(id)
	if err != nil {
		return nil, nil, err
	}

	conv, err := s.db.GetConversation(ctx, uuid)
	if err != nil {
		return nil, nil, fmt.Errorf("get conversation: %w", err)
	}

	dbMsgs, err := s.db.GetMessagesByConversation(ctx, uuid)
	if err != nil {
		return nil, nil, fmt.Errorf("get messages: %w", err)
	}

	messages := make([]*Message, len(dbMsgs))
	for i, m := range dbMsgs {
		messages[i] = dbMessageToMessage(m)
	}

	return dbConvToConversation(conv), messages, nil
}

func (s *service) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = s.maxHistory
	}
	convs, err := s.db.ListConversations(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}

	result := make([]*Conversation, len(convs))
	for i, c := range convs {
		result[i] = dbConvToConversation(c)
	}
	return result, nil
}

func (s *service) DeleteConversation(ctx context.Context, id string) error {
	uuid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return s.db.DeleteConversation(ctx, uuid)
}

// resolveConversation returns the UUID for the conversation, creating a new one if needed.
func (s *service) resolveConversation(ctx context.Context, conversationID *string) (pgtype.UUID, bool, error) {
	if conversationID != nil {
		uuid, err := parseUUID(*conversationID)
		return uuid, false, err
	}

	conv, err := s.db.CreateConversation(ctx, pgtype.Text{})
	if err != nil {
		return pgtype.UUID{}, false, fmt.Errorf("create conversation: %w", err)
	}
	return conv.ID, true, nil
}

func (s *service) loadHistory(ctx context.Context, convID pgtype.UUID) ([]llm.Message, error) {
	dbMsgs, err := s.db.GetMessagesByConversation(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Cap history
	if len(dbMsgs) > s.maxHistory {
		dbMsgs = dbMsgs[len(dbMsgs)-s.maxHistory:]
	}

	// System prompt + history messages
	messages := make([]llm.Message, 0, len(dbMsgs)+1)
	messages = append(messages, llm.Message{Role: "system", Content: SystemPrompt})

	for _, m := range dbMsgs {
		msg := llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: textToString(m.ToolCallID),
		}
		if len(m.ToolCalls) > 0 {
			_ = json.Unmarshal(m.ToolCalls, &msg.ToolCalls)
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (s *service) createMessage(ctx context.Context, convID pgtype.UUID, role, content string, toolCalls []llm.ToolCall, toolCallID string) (sqlcdb.MauriceMessage, error) {
	params := sqlcdb.CreateMessageParams{
		ConversationID: convID,
		Role:           role,
		Content:        content,
	}

	if len(toolCalls) > 0 {
		tc, err := json.Marshal(toolCalls)
		if err != nil {
			return sqlcdb.MauriceMessage{}, fmt.Errorf("marshal tool calls: %w", err)
		}
		params.ToolCalls = tc
	}

	if toolCallID != "" {
		params.ToolCallID = pgtype.Text{String: toolCallID, Valid: true}
	}

	return s.db.CreateMessage(ctx, params)
}

// generateTitle asks the LLM to create a short title for the conversation.
func (s *service) generateTitle(ctx context.Context, convID pgtype.UUID, userMsg, assistantMsg string) {
	req := &llm.ChatCompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: userMsg},
			{Role: "assistant", Content: assistantMsg},
			{Role: "user", Content: titleGenerationHint},
		},
		MaxTokens: 20,
	}

	resp, err := s.llmClient.ChatCompletion(ctx, req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate conversation title")
		return
	}

	title := resp.FirstContent()
	if title == "" {
		return
	}

	err = s.db.UpdateConversationTitle(ctx, sqlcdb.UpdateConversationTitleParams{
		ID:    convID,
		Title: pgtype.Text{String: title, Valid: true},
	})
	if err != nil {
		log.Warn().Err(err).Msg("failed to update conversation title")
	}
}

// Helper conversions

func parseUUID(s string) (pgtype.UUID, error) {
	var uuid pgtype.UUID
	if err := uuid.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return uuid, nil
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func textToString(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func dbConvToConversation(c sqlcdb.MauriceConversation) *Conversation {
	conv := &Conversation{
		ID:        uuidToString(c.ID),
		CreatedAt: c.CreatedAt.Time,
		UpdatedAt: c.UpdatedAt.Time,
	}
	if c.Title.Valid {
		conv.Title = &c.Title.String
	}
	return conv
}

func dbMessageToMessage(m sqlcdb.MauriceMessage) *Message {
	msg := &Message{
		ID:         uuidToString(m.ID),
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: textToString(m.ToolCallID),
		CreatedAt:  m.CreatedAt.Time,
	}
	if len(m.ToolCalls) > 0 {
		_ = json.Unmarshal(m.ToolCalls, &msg.ToolCalls)
	}
	return msg
}

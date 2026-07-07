package maurice

import (
	"context"

	"github.com/sperano/puckdb/llm"
)

// DB defines the storage operations Maurice needs for conversation persistence.
// Implementations exist for PostgreSQL (pgdb.go) and SQLite (sqlitedb.go).
type DB interface {
	CreateConversation(ctx context.Context) (*Conversation, error)
	GetConversation(ctx context.Context, id string) (*Conversation, error)
	UpdateConversationTitle(ctx context.Context, id, title string) error
	ListConversations(ctx context.Context, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
	CreateMessage(ctx context.Context, p CreateMessageParams) (*Message, error)
	// CreateMessages persists a whole turn's messages atomically: either every
	// message is written or none are. It returns the created messages in the
	// same order as params. Chat uses it to flush a buffered turn so a crash
	// between individual writes can never leave a partial turn in the store.
	CreateMessages(ctx context.Context, params []CreateMessageParams) ([]*Message, error)
	GetMessages(ctx context.Context, conversationID string) ([]*Message, error)
}

// CreateMessageParams contains the fields for creating a new message.
type CreateMessageParams struct {
	ConversationID string
	Role           string
	Content        string
	ToolCalls      []llm.ToolCall
	ToolCallID     string
}

package maurice

import (
	"context"
	"errors"
	"fmt"

	"github.com/sperano/puckdb/llm"
)

// ErrMixedConversations is returned by CreateMessages when the batch spans
// more than one conversation. A turn belongs to exactly one conversation, and
// the activity-timestamp bump is applied to that one conversation.
var ErrMixedConversations = errors.New("messages in one batch must share a conversation")

// batchConversationID returns the single conversation ID every message in
// params refers to, or ErrMixedConversations if they differ. Callers check it
// before writing anything so no backend has to reason about mixed batches.
func batchConversationID(params []CreateMessageParams) (string, error) {
	id := params[0].ConversationID
	for i, p := range params[1:] {
		if p.ConversationID != id {
			return "", fmt.Errorf("message %d: %w", i+1, ErrMixedConversations)
		}
	}
	return id, nil
}

// DB defines the storage operations Maurice needs for conversation persistence.
// Implementations exist for PostgreSQL (pgdb.go) and SQLite (sqlitedb.go).
type DB interface {
	CreateConversation(ctx context.Context) (*Conversation, error)
	GetConversation(ctx context.Context, id string) (*Conversation, error)
	UpdateConversationTitle(ctx context.Context, id, title string) error
	ListConversations(ctx context.Context, limit int) ([]*Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
	// CreateMessages persists a whole turn's messages atomically: either every
	// message is written or none are, and the conversation's updated_at is
	// bumped in the same write. It returns the created messages in the same
	// order as params. Every message must belong to the same conversation
	// (ErrMixedConversations otherwise). Chat uses it to flush a buffered turn
	// so a crash between individual writes can never leave a partial turn in
	// the store.
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

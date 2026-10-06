package maurice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

type pgDB struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

// NewPgDB wraps a pgx pool as a maurice.DB for PostgreSQL persistence. The pool
// backs both the pool-scoped Queries used by single-statement operations and
// the per-transaction Queries used by BeginTurn, FinishTurn and RecordTitle.
// User IDs are app_users UUIDs.
func NewPgDB(pool *pgxpool.Pool) DB {
	return &pgDB{pool: pool, q: sqlcdb.New(pool)}
}

func (db *pgDB) GetConversation(ctx context.Context, userID, id string) (*Conversation, error) {
	user, conv, err := parseOwned(userID, id)
	if err != nil {
		return nil, err
	}
	row, err := db.q.GetConversation(ctx, sqlcdb.GetConversationParams{ID: conv, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, err
	}
	return newPgConversation(row.ID, row.Title, row.CreatedAt, row.UpdatedAt), nil
}

func (db *pgDB) ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error) {
	user, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.q.ListConversations(ctx, sqlcdb.ListConversationsParams{UserID: user, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]*Conversation, len(rows))
	for i, r := range rows {
		result[i] = newPgConversation(r.ID, r.Title, r.CreatedAt, r.UpdatedAt)
	}
	return result, nil
}

func (db *pgDB) DeleteConversation(ctx context.Context, userID, id string) error {
	user, conv, err := parseOwned(userID, id)
	if err != nil {
		return err
	}
	deleted, err := db.q.DeleteConversation(ctx, sqlcdb.DeleteConversationParams{ID: conv, UserID: user})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrConversationNotFound
	}
	return nil
}

func (db *pgDB) GetMessages(ctx context.Context, userID, conversationID string) ([]*Message, error) {
	user, conv, err := parseOwned(userID, conversationID)
	if err != nil {
		return nil, err
	}
	rows, err := db.q.GetTranscript(ctx, sqlcdb.GetTranscriptParams{ConversationID: conv, UserID: user})
	if err != nil {
		return nil, err
	}
	result := make([]*Message, len(rows))
	for i, r := range rows {
		if result[i], err = newPgMessage(r.ID, r.Role, r.Content, r.ToolCalls, r.ToolCallID, r.CreatedAt); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// parseOwned parses a user ID and a conversation ID. A malformed
// conversation ID is reported as ErrConversationNotFound: it cannot name a
// conversation the user owns.
func parseOwned(userID, conversationID string) (pgtype.UUID, pgtype.UUID, error) {
	user, err := parseUUID(userID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	conv, err := parseUUID(conversationID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("%w: %w", ErrConversationNotFound, err)
	}
	return user, conv, nil
}

// pgtype conversion helpers

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

func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func optionalTextPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func newPgConversation(id pgtype.UUID, title pgtype.Text, createdAt, updatedAt pgtype.Timestamptz) *Conversation {
	conv := &Conversation{
		ID:        uuidToString(id),
		CreatedAt: createdAt.Time,
		UpdatedAt: updatedAt.Time,
	}
	if title.Valid {
		conv.Title = &title.String
	}
	return conv
}

// newPgMessage maps a stored row to a Message. A tool_calls value that does
// not decode is reported rather than dropped: silently returning a message
// without its tool calls would hand the LLM a history whose tool results have
// no matching calls.
func newPgMessage(id pgtype.UUID, role sqlcdb.ChatRole, content string, toolCalls []byte, toolCallID pgtype.Text, createdAt pgtype.Timestamptz) (*Message, error) {
	msg := &Message{
		ID:        uuidToString(id),
		Role:      string(role),
		Content:   content,
		CreatedAt: createdAt.Time,
	}
	if toolCallID.Valid {
		msg.ToolCallID = toolCallID.String
	}
	if len(toolCalls) > 0 {
		if err := json.Unmarshal(toolCalls, &msg.ToolCalls); err != nil {
			return nil, fmt.Errorf("message %s: decode tool calls: %w", msg.ID, err)
		}
	}
	return msg, nil
}

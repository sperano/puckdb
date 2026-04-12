package maurice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
)

type pgDB struct {
	q *sqlcdb.Queries
}

// NewPgDB wraps sqlcdb.Queries as a maurice.DB for PostgreSQL persistence.
func NewPgDB(q *sqlcdb.Queries) DB {
	return &pgDB{q: q}
}

func (db *pgDB) CreateConversation(ctx context.Context) (*Conversation, error) {
	row, err := db.q.CreateConversation(ctx, pgtype.Text{})
	if err != nil {
		return nil, err
	}
	return pgConvToConv(row), nil
}

func (db *pgDB) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	uuid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := db.q.GetConversation(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return pgConvToConv(row), nil
}

func (db *pgDB) UpdateConversationTitle(ctx context.Context, id, title string) error {
	uuid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return db.q.UpdateConversationTitle(ctx, sqlcdb.UpdateConversationTitleParams{
		ID:    uuid,
		Title: pgtype.Text{String: title, Valid: true},
	})
}

func (db *pgDB) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	rows, err := db.q.ListConversations(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	result := make([]*Conversation, len(rows))
	for i, r := range rows {
		result[i] = pgConvToConv(r)
	}
	return result, nil
}

func (db *pgDB) DeleteConversation(ctx context.Context, id string) error {
	uuid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return db.q.DeleteConversation(ctx, uuid)
}

func (db *pgDB) CreateMessage(ctx context.Context, p CreateMessageParams) (*Message, error) {
	uuid, err := parseUUID(p.ConversationID)
	if err != nil {
		return nil, err
	}
	params := sqlcdb.CreateMessageParams{
		ConversationID: uuid,
		Role:           sqlcdb.ChatRole(p.Role),
		Content:        p.Content,
	}
	if len(p.ToolCalls) > 0 {
		tc, err := json.Marshal(p.ToolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal tool calls: %w", err)
		}
		params.ToolCalls = tc
	}
	if p.ToolCallID != "" {
		params.ToolCallID = pgtype.Text{String: p.ToolCallID, Valid: true}
	}
	row, err := db.q.CreateMessage(ctx, params)
	if err != nil {
		return nil, err
	}
	return pgMsgToMsg(row), nil
}

func (db *pgDB) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) {
	uuid, err := parseUUID(conversationID)
	if err != nil {
		return nil, err
	}
	rows, err := db.q.GetMessagesByConversation(ctx, uuid)
	if err != nil {
		return nil, err
	}
	result := make([]*Message, len(rows))
	for i, r := range rows {
		result[i] = pgMsgToMsg(r)
	}
	return result, nil
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

func pgConvToConv(c sqlcdb.MauriceConversation) *Conversation {
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

func pgMsgToMsg(m sqlcdb.MauriceMessage) *Message {
	msg := &Message{
		ID:        uuidToString(m.ID),
		Role:      string(m.Role),
		Content:   m.Content,
		CreatedAt: m.CreatedAt.Time,
	}
	if m.ToolCallID.Valid {
		msg.ToolCallID = m.ToolCallID.String
	}
	if len(m.ToolCalls) > 0 {
		_ = json.Unmarshal(m.ToolCalls, &msg.ToolCalls)
	}
	return msg
}

package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/sqlcdb"
)

type pgDB struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

// NewPgDB wraps a pgx pool as a maurice.DB for PostgreSQL persistence. The pool
// backs both the pool-scoped Queries used by single-statement operations and
// the per-transaction Queries used by CreateMessages.
func NewPgDB(pool *pgxpool.Pool) DB {
	return &pgDB{pool: pool, q: sqlcdb.New(pool)}
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
	return createMessageWith(ctx, db.q, p)
}

// createMessageWithCreatedAt inserts one message with an explicit created_at.
// The generated sqlcdb.CreateMessage relies on the DEFAULT NOW(), which returns
// the transaction start time and is therefore identical for every row inserted
// in one transaction — leaving GetMessagesByConversation's "ORDER BY created_at
// ASC" (no tiebreaker) unable to preserve turn order. CreateMessages needs a
// strictly increasing created_at, so it uses this explicit-timestamp insert.
const createMessageWithCreatedAt = `
INSERT INTO maurice_messages (conversation_id, role, content, tool_calls, tool_call_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, conversation_id, role, content, tool_calls, tool_call_id, created_at`

// createdAtStepMicros is the spacing between successive messages' created_at
// values within a turn. TIMESTAMPTZ has microsecond resolution, so a full
// microsecond guarantees distinct, strictly increasing timestamps that
// ORDER BY created_at can sort into turn order.
const createdAtStepMicros = time.Microsecond

// CreateMessages inserts every message inside a single pgx transaction, so the
// turn is persisted all-or-nothing. A failure on any insert (or the commit)
// rolls back the whole batch and returns the error; the returned slice matches
// params order on success. Each message is stamped with a strictly increasing
// created_at (see createMessageWithCreatedAt) so turn order is preserved by the
// stored ordering even though the whole batch commits at one transaction time.
func (db *pgDB) CreateMessages(ctx context.Context, params []CreateMessageParams) ([]*Message, error) {
	if len(params) == 0 {
		return nil, nil
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a safe no-op after a successful Commit (documented in pgx),
	// so this deferred call can stay unconditional.
	defer func() { _ = tx.Rollback(ctx) }()

	base := time.Now().UTC()
	result := make([]*Message, len(params))
	for i, p := range params {
		createdAt := base.Add(time.Duration(i) * createdAtStepMicros)
		msg, err := insertMessageTx(ctx, tx, p, createdAt)
		if err != nil {
			return nil, err
		}
		result[i] = msg
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return result, nil
}

// insertMessageTx inserts one message with an explicit created_at using the
// given pgx.Tx and returns the stored row mapped to a Message.
func insertMessageTx(ctx context.Context, tx pgx.Tx, p CreateMessageParams, createdAt time.Time) (*Message, error) {
	convUUID, err := parseUUID(p.ConversationID)
	if err != nil {
		return nil, err
	}
	var toolCalls []byte
	if len(p.ToolCalls) > 0 {
		tc, err := json.Marshal(p.ToolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal tool calls: %w", err)
		}
		toolCalls = tc
	}
	var toolCallID pgtype.Text
	if p.ToolCallID != "" {
		toolCallID = pgtype.Text{String: p.ToolCallID, Valid: true}
	}

	var row sqlcdb.MauriceMessage
	err = tx.QueryRow(ctx, createMessageWithCreatedAt,
		convUUID,
		sqlcdb.ChatRole(p.Role),
		p.Content,
		toolCalls,
		toolCallID,
		pgtype.Timestamptz{Time: createdAt, Valid: true},
	).Scan(
		&row.ID,
		&row.ConversationID,
		&row.Role,
		&row.Content,
		&row.ToolCalls,
		&row.ToolCallID,
		&row.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return pgMsgToMsg(row), nil
}

// createMessageWith inserts one message using the given Queries handle, which
// may be pool-scoped (CreateMessage) or transaction-scoped (CreateMessages).
func createMessageWith(ctx context.Context, q *sqlcdb.Queries, p CreateMessageParams) (*Message, error) {
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
	row, err := q.CreateMessage(ctx, params)
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

package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// createdAtStep is the spacing between successive messages' created_at values
// within a turn. TIMESTAMPTZ has microsecond resolution, so a full microsecond
// guarantees distinct, strictly increasing timestamps that ORDER BY created_at
// can sort into turn order.
const createdAtStep = time.Microsecond

// CreateMessages inserts every message inside a single pgx transaction and
// bumps the conversation's updated_at in that same transaction, so the turn is
// persisted all-or-nothing and ListConversations reflects it as soon as it
// commits. A failure on any insert, the touch, or the commit rolls back the
// whole batch and returns the error; the returned slice matches params order
// on success. Each message is stamped with a strictly increasing created_at
// (see sqlcdb.CreateMessage) so turn order survives the shared commit time.
func (db *pgDB) CreateMessages(ctx context.Context, params []CreateMessageParams) ([]*Message, error) {
	if len(params) == 0 {
		return nil, nil
	}
	convID, err := batchConversationID(params)
	if err != nil {
		return nil, err
	}
	convUUID, err := parseUUID(convID)
	if err != nil {
		return nil, err
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a safe no-op after a successful Commit (documented in pgx),
	// so this deferred call can stay unconditional.
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.q.WithTx(tx)

	base := time.Now().UTC()
	result := make([]*Message, len(params))
	for i, p := range params {
		sqlcParams, err := toSQLCMessageParams(convUUID, p, base.Add(time.Duration(i)*createdAtStep))
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		row, err := q.CreateMessage(ctx, sqlcParams)
		if err != nil {
			return nil, fmt.Errorf("insert message %d: %w", i, err)
		}
		if result[i], err = pgMsgToMsg(row); err != nil {
			return nil, err
		}
	}

	// Inside the transaction so a failure here rolls the turn back rather than
	// leaving messages without an activity bump.
	if err := q.TouchConversation(ctx, convUUID); err != nil {
		return nil, fmt.Errorf("touch conversation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return result, nil
}

// toSQLCMessageParams converts the backend-neutral params into the sqlc insert
// params, serializing tool calls to JSON and mapping empty strings to NULL.
// convUUID is the already-parsed form of p.ConversationID.
func toSQLCMessageParams(convUUID pgtype.UUID, p CreateMessageParams, createdAt time.Time) (sqlcdb.CreateMessageParams, error) {
	params := sqlcdb.CreateMessageParams{
		ConversationID: convUUID,
		Role:           sqlcdb.ChatRole(p.Role),
		Content:        p.Content,
		CreatedAt:      pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
	if len(p.ToolCalls) > 0 {
		tc, err := json.Marshal(p.ToolCalls)
		if err != nil {
			return sqlcdb.CreateMessageParams{}, fmt.Errorf("marshal tool calls: %w", err)
		}
		params.ToolCalls = tc
	}
	if p.ToolCallID != "" {
		params.ToolCallID = pgtype.Text{String: p.ToolCallID, Valid: true}
	}
	return params, nil
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
		if result[i], err = pgMsgToMsg(r); err != nil {
			return nil, err
		}
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

// pgMsgToMsg maps a stored row to a Message. A tool_calls value that does not
// decode is reported rather than dropped: silently returning a message without
// its tool calls would hand the LLM a history whose tool results have no
// matching calls.
func pgMsgToMsg(m sqlcdb.MauriceMessage) (*Message, error) {
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
		if err := json.Unmarshal(m.ToolCalls, &msg.ToolCalls); err != nil {
			return nil, fmt.Errorf("message %s: decode tool calls: %w", msg.ID, err)
		}
	}
	return msg, nil
}

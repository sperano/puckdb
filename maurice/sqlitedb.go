package maurice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS conversations (
	id TEXT PRIMARY KEY,
	title TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	tool_calls TEXT,
	tool_call_id TEXT,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_conversations_updated ON conversations(updated_at DESC);
`

// sqliteTimeLayout is the fixed-width form every timestamp is stored in. It is
// RFC 3339 with the fractional seconds always padded to nine digits, unlike
// time.RFC3339Nano which trims trailing zeros: with trimming, "…11.12Z" sorts
// AFTER "…11.123456Z" because 'Z' > '3', so ORDER BY on the TEXT column would
// not be chronological. Padding keeps every value the same width, making the
// string order and the time order identical. time.RFC3339Nano still parses
// both padded and (legacy) trimmed values.
const sqliteTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

type sqliteDB struct {
	db *sql.DB
}

// NewSQLiteDB wraps a *sql.DB (opened with a SQLite driver) as a maurice.DB.
// It creates the required tables if they don't exist.
func NewSQLiteDB(db *sql.DB) (DB, error) {
	// Single connection — keeps PRAGMA foreign_keys consistent
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), sqliteSchema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &sqliteDB{db: db}, nil
}

func (s *sqliteDB) CreateConversation(ctx context.Context) (*Conversation, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(sqliteTimeLayout)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO conversations (id, created_at, updated_at) VALUES (?, ?, ?)",
		id, nowStr, nowStr,
	)
	if err != nil {
		return nil, err
	}
	return &Conversation{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *sqliteDB) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, title, created_at, updated_at FROM conversations WHERE id = ?", id,
	)
	return scanConversation(row)
}

func (s *sqliteDB) UpdateConversationTitle(ctx context.Context, id, title string) error {
	now := time.Now().UTC().Format(sqliteTimeLayout)
	_, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?",
		title, now, id,
	)
	return err
}

func (s *sqliteDB) ListConversations(ctx context.Context, limit int) ([]*Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, title, created_at, updated_at FROM conversations ORDER BY updated_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Conversation
	for rows.Next() {
		conv, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, conv)
	}
	return result, rows.Err()
}

func (s *sqliteDB) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM conversations WHERE id = ?", id)
	return err
}

// CreateMessages inserts every message inside a single transaction and bumps
// the conversation's updated_at in that same transaction, so the turn is
// persisted all-or-nothing and ListConversations reflects it as soon as it
// commits. A failure on any insert, the bump, or the commit rolls the whole
// batch back. The returned slice matches params order on success.
//
// Every message in the turn is stamped with the SAME created_at. Ordering is
// then decided by the rowid tiebreaker in GetMessages' "ORDER BY created_at
// ASC, rowid ASC", which preserves insertion (turn) order regardless of
// timestamp resolution.
func (s *sqliteDB) CreateMessages(ctx context.Context, params []CreateMessageParams) ([]*Message, error) {
	if len(params) == 0 {
		return nil, nil
	}
	convID, err := batchConversationID(params)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback after a successful Commit is a no-op (database/sql returns
	// ErrTxDone, which we ignore), so this deferred call can stay unconditional.
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	result := make([]*Message, len(params))
	for i, p := range params {
		msg, err := insertMessage(ctx, tx, p, now)
		if err != nil {
			return nil, fmt.Errorf("insert message %d: %w", i, err)
		}
		result[i] = msg
	}

	// Bump the conversation's updated_at once for the whole turn. Inside the
	// transaction so a failure here rolls the turn back rather than leaving
	// messages without an activity bump.
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET updated_at = ? WHERE id = ?",
		now.Format(sqliteTimeLayout), convID,
	); err != nil {
		return nil, fmt.Errorf("touch conversation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// insertMessage writes one message row inside the given transaction, stamped
// with the given created_at, and returns the resulting Message.
func insertMessage(ctx context.Context, tx *sql.Tx, p CreateMessageParams, now time.Time) (*Message, error) {
	id := uuid.New().String()
	nowStr := now.Format(sqliteTimeLayout)

	var toolCallsJSON *string
	if len(p.ToolCalls) > 0 {
		b, err := json.Marshal(p.ToolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal tool calls: %w", err)
		}
		str := string(b)
		toolCallsJSON = &str
	}

	var toolCallID *string
	if p.ToolCallID != "" {
		toolCallID = &p.ToolCallID
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, p.ConversationID, p.Role, p.Content, toolCallsJSON, toolCallID, nowStr,
	); err != nil {
		return nil, err
	}

	return &Message{
		ID:         id,
		Role:       p.Role,
		Content:    p.Content,
		ToolCalls:  p.ToolCalls,
		ToolCallID: p.ToolCallID,
		CreatedAt:  now,
	}, nil
}

func (s *sqliteDB) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, content, tool_calls, tool_call_id, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY created_at ASC, rowid ASC`,
		conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, msg)
	}
	return result, rows.Err()
}

// rowScanner is implemented by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// parseStoredTime decodes a timestamp column. RFC3339Nano parses both the
// padded sqliteTimeLayout and legacy trimmed values.
func parseStoredTime(column, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s %q: %w", column, value, err)
	}
	return t, nil
}

// scanConversation maps a row to a Conversation. A timestamp that does not
// parse is reported rather than zeroed, since ordering and display depend on it.
func scanConversation(s rowScanner) (*Conversation, error) {
	var id, createdAt, updatedAt string
	var title sql.NullString
	if err := s.Scan(&id, &title, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	conv := &Conversation{ID: id}
	var err error
	if conv.CreatedAt, err = parseStoredTime("created_at", createdAt); err != nil {
		return nil, fmt.Errorf("conversation %s: %w", id, err)
	}
	if conv.UpdatedAt, err = parseStoredTime("updated_at", updatedAt); err != nil {
		return nil, fmt.Errorf("conversation %s: %w", id, err)
	}
	if title.Valid {
		conv.Title = &title.String
	}
	return conv, nil
}

// scanMessage maps a row to a Message. A tool_calls value that does not decode
// is reported rather than dropped: silently returning a message without its
// tool calls would hand the LLM a history whose tool results have no matching
// calls.
func scanMessage(s rowScanner) (*Message, error) {
	var id, role, content, createdAt string
	var toolCallsStr, toolCallID sql.NullString
	if err := s.Scan(&id, &role, &content, &toolCallsStr, &toolCallID, &createdAt); err != nil {
		return nil, err
	}
	msg := &Message{
		ID:      id,
		Role:    role,
		Content: content,
	}
	var err error
	if msg.CreatedAt, err = parseStoredTime("created_at", createdAt); err != nil {
		return nil, fmt.Errorf("message %s: %w", id, err)
	}
	if toolCallID.Valid {
		msg.ToolCallID = toolCallID.String
	}
	if toolCallsStr.Valid {
		if err := json.Unmarshal([]byte(toolCallsStr.String), &msg.ToolCalls); err != nil {
			return nil, fmt.Errorf("message %s: decode tool calls: %w", id, err)
		}
	}
	return msg, nil
}

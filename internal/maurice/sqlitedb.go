package maurice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// The SQLite store backs the single-user REPL. It keeps owner-scoped
// conversations, turns (idempotency, one running turn per conversation) and
// the transcript, but not LLM-call or tool-call usage: that analysis belongs
// to the multi-user PostgreSQL store.
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS conversations (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	title TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS turns (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	user_id TEXT NOT NULL,
	turn_number INTEGER NOT NULL,
	idempotency_key TEXT NOT NULL,
	request_hash BLOB NOT NULL,
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	completed_at TEXT,
	error_class TEXT,
	UNIQUE (conversation_id, turn_number),
	UNIQUE (user_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS messages (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	turn_id TEXT REFERENCES turns(id) ON DELETE CASCADE,
	message_number INTEGER,
	role TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	tool_calls TEXT,
	tool_call_id TEXT,
	created_at TEXT NOT NULL
);
`

// sqliteIndexes run after sqliteUpgrades so the columns they cover exist in
// databases created before those columns.
const sqliteIndexes = `
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_updated ON conversations(user_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_turns_one_running ON turns(conversation_id) WHERE status = 'running';
CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_turn_number ON messages(turn_id, message_number) WHERE turn_id IS NOT NULL;
`

// sqliteUpgrade adds a column missing from a database created by an earlier
// version. Messages written before turns existed keep a NULL turn_id and stay
// in the transcript; their conversations belong to LocalUserID.
type sqliteUpgrade struct {
	table, column, definition string
}

var sqliteUpgrades = []sqliteUpgrade{
	{"conversations", "user_id", "TEXT NOT NULL DEFAULT '" + LocalUserID + "'"},
	{"messages", "turn_id", "TEXT REFERENCES turns(id) ON DELETE CASCADE"},
	{"messages", "message_number", "INTEGER"},
}

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
// It creates the required tables if they don't exist and upgrades tables
// created by earlier versions.
func NewSQLiteDB(db *sql.DB) (DB, error) {
	// Single connection — keeps PRAGMA foreign_keys consistent and serializes
	// every transaction, which BeginTurn relies on.
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, sqliteSchema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	for _, u := range sqliteUpgrades {
		if err := addMissingColumn(ctx, db, u); err != nil {
			return nil, err
		}
	}
	if _, err := db.ExecContext(ctx, sqliteIndexes); err != nil {
		return nil, fmt.Errorf("create indexes: %w", err)
	}
	return &sqliteDB{db: db}, nil
}

func addMissingColumn(ctx context.Context, db *sql.DB, u sqliteUpgrade) error {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", u.table, u.column,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect %s.%s: %w", u.table, u.column, err)
	}
	if count > 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", u.table, u.column, u.definition),
	); err != nil {
		return fmt.Errorf("add %s.%s: %w", u.table, u.column, err)
	}
	return nil
}

func (s *sqliteDB) GetConversation(ctx context.Context, userID, id string) (*Conversation, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, title, created_at, updated_at FROM conversations WHERE id = ? AND user_id = ?", id, userID,
	)
	conv, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return nil, ErrConversationNotFound
	}
	return conv, err
}

func (s *sqliteDB) ListConversations(ctx context.Context, userID string, limit int) ([]*Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, title, created_at, updated_at FROM conversations WHERE user_id = ? ORDER BY updated_at DESC LIMIT ?",
		userID, limit,
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

func (s *sqliteDB) DeleteConversation(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM conversations WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConversationNotFound
	}
	return nil
}

// GetMessages returns messages of succeeded turns plus legacy messages written
// before turns existed. Each turn's messages share one created_at; rowid keeps
// their insertion (turn) order.
func (s *sqliteDB) GetMessages(ctx context.Context, userID, conversationID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.role, m.content, m.tool_calls, m.tool_call_id, m.created_at
		 FROM messages m
		 JOIN conversations c ON c.id = m.conversation_id
		 LEFT JOIN turns t ON t.id = m.turn_id
		 WHERE m.conversation_id = ? AND c.user_id = ? AND (m.turn_id IS NULL OR t.status = ?)
		 ORDER BY m.created_at ASC, m.rowid ASC`,
		conversationID, userID, string(TurnSucceeded),
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

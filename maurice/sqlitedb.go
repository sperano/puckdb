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
	nowStr := now.Format(time.RFC3339Nano)
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
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

func (s *sqliteDB) CreateMessage(ctx context.Context, p CreateMessageParams) (*Message, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

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

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, p.ConversationID, p.Role, p.Content, toolCallsJSON, toolCallID, nowStr,
	)
	if err != nil {
		return nil, err
	}

	// Update conversation's updated_at
	_, _ = s.db.ExecContext(ctx,
		"UPDATE conversations SET updated_at = ? WHERE id = ?",
		nowStr, p.ConversationID,
	)

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

func scanConversation(s rowScanner) (*Conversation, error) {
	var id, createdAt, updatedAt string
	var title sql.NullString
	if err := s.Scan(&id, &title, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	conv := &Conversation{ID: id}
	conv.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	conv.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if title.Valid {
		conv.Title = &title.String
	}
	return conv, nil
}

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
	msg.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if toolCallID.Valid {
		msg.ToolCallID = toolCallID.String
	}
	if toolCallsStr.Valid {
		_ = json.Unmarshal([]byte(toolCallsStr.String), &msg.ToolCalls)
	}
	return msg, nil
}

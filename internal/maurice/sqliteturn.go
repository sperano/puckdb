package maurice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/llm"
)

// BeginTurn opens a turn. The store has a single connection, so the
// transaction serializes every turn start; the unique indexes back that up.
func (s *sqliteDB) BeginTurn(ctx context.Context, params BeginTurnParams) (*TurnStart, error) {
	var start *TurnStart
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		start, err = beginSQLiteTurn(ctx, tx, params)
		return err
	})
	if err != nil {
		return nil, err
	}
	return start, nil
}

func beginSQLiteTurn(ctx context.Context, tx *sql.Tx, params BeginTurnParams) (*TurnStart, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-params.StaleAfter).Format(sqliteTimeLayout)
	existing, err := sqliteTurnByKey(ctx, tx, params, staleBefore)
	switch {
	case err == nil:
		return replaySQLiteTurn(ctx, tx, params, existing)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("look up idempotency key: %w", err)
	}

	convID, err := ensureSQLiteConversation(ctx, tx, params, now)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE turns SET status = ?, error_class = ?, completed_at = ?
		 WHERE conversation_id = ? AND status = ? AND started_at < ?`,
		TurnFailed, ErrorClassAbandoned, now.Format(sqliteTimeLayout), convID, TurnRunning, staleBefore,
	); err != nil {
		return nil, fmt.Errorf("abandon stale turns: %w", err)
	}
	var running int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM turns WHERE conversation_id = ? AND status = ?", convID, TurnRunning,
	).Scan(&running); err != nil {
		return nil, fmt.Errorf("check running turn: %w", err)
	}
	if running > 0 {
		return nil, ErrTurnInProgress
	}
	start := &TurnStart{TurnID: uuid.NewString(), ConversationID: convID, NewConversation: params.ConversationID == ""}
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(turn_number) + 1, 0) FROM turns WHERE conversation_id = ?", convID,
	).Scan(&start.TurnNumber); err != nil {
		return nil, fmt.Errorf("next turn number: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO turns (id, conversation_id, user_id, turn_number, idempotency_key, request_hash, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		start.TurnID, convID, params.UserID, start.TurnNumber, params.IdempotencyKey, params.RequestHash,
		TurnRunning, now.Format(sqliteTimeLayout),
	); err != nil {
		return nil, fmt.Errorf("insert turn: %w", err)
	}
	return start, nil
}

func sqliteTurnByKey(ctx context.Context, tx *sql.Tx, params BeginTurnParams, staleBefore string) (storedTurn, error) {
	var turn storedTurn
	var errorClass sql.NullString
	var startedAt string
	var warnings string
	err := tx.QueryRowContext(ctx,
		`SELECT t.id, t.conversation_id, t.request_hash, t.status, t.error_class, t.started_at, t.warnings,
		        c.deleted_at IS NOT NULL
		 FROM turns t JOIN conversations c ON c.id = t.conversation_id
		 WHERE t.user_id = ? AND t.idempotency_key = ?`,
		params.UserID, params.IdempotencyKey,
	).Scan(&turn.ID, &turn.ConversationID, &turn.RequestHash, &turn.Status, &errorClass, &startedAt, &warnings, &turn.ConversationDeleted)
	if err != nil {
		return storedTurn{}, err
	}
	turn.ErrorClass = ErrorClass(errorClass.String)
	if err := json.Unmarshal([]byte(warnings), &turn.Warnings); err != nil {
		return storedTurn{}, fmt.Errorf("decode turn warnings: %w", err)
	}
	turn.Stale = turn.Status == TurnRunning && startedAt < staleBefore
	return turn, nil
}

func ensureSQLiteConversation(ctx context.Context, tx *sql.Tx, params BeginTurnParams, now time.Time) (string, error) {
	if params.ConversationID == "" {
		id := uuid.NewString()
		stamp := now.Format(sqliteTimeLayout)
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO conversations (id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?)",
			id, params.UserID, stamp, stamp,
		); err != nil {
			return "", fmt.Errorf("create conversation: %w", err)
		}
		return id, nil
	}
	var id string
	err := tx.QueryRowContext(ctx,
		"SELECT id FROM conversations WHERE id = ? AND user_id = ? AND deleted_at IS NULL", params.ConversationID, params.UserID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrConversationNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find conversation: %w", err)
	}
	return id, nil
}

func replaySQLiteTurn(ctx context.Context, tx *sql.Tx, params BeginTurnParams, turn storedTurn) (*TurnStart, error) {
	abandon, err := checkKeyReuse(params, turn)
	if err != nil {
		return nil, err
	}
	if abandon {
		if _, err := tx.ExecContext(ctx,
			"UPDATE turns SET status = ?, error_class = ?, completed_at = ? WHERE id = ? AND status = ?",
			TurnFailed, ErrorClassAbandoned, time.Now().UTC().Format(sqliteTimeLayout), turn.ID, TurnRunning,
		); err != nil {
			return nil, fmt.Errorf("abandon turn: %w", err)
		}
		return abandonedReplay(turn), nil
	}
	start := &TurnStart{
		TurnID: turn.ID, ConversationID: turn.ConversationID,
		Replay: &TurnReplay{Status: turn.Status, ErrorClass: turn.ErrorClass, Warnings: turn.Warnings},
	}
	if turn.Status != TurnSucceeded {
		return start, nil
	}
	return start, loadSQLiteReplay(ctx, tx, turn.ID, start.Replay)
}

func loadSQLiteReplay(ctx context.Context, tx *sql.Tx, turnID string, replay *TurnReplay) error {
	rows, err := tx.QueryContext(ctx,
		"SELECT id, content, tool_calls FROM messages WHERE turn_id = ? ORDER BY message_number", turnID,
	)
	if err != nil {
		return fmt.Errorf("load replayed turn: %w", err)
	}
	defer rows.Close()
	var lists [][]llm.ToolCall
	for rows.Next() {
		var toolCalls sql.NullString
		if err := rows.Scan(&replay.MessageID, &replay.Content, &toolCalls); err != nil {
			return err
		}
		if toolCalls.Valid {
			var calls []llm.ToolCall
			if err := json.Unmarshal([]byte(toolCalls.String), &calls); err != nil {
				return fmt.Errorf("decode replayed tool calls: %w", err)
			}
			lists = append(lists, calls)
		}
	}
	replay.ToolsUsed = toolNames(lists)
	return rows.Err()
}

// FinishTurn sets the terminal status and writes the turn's messages in one
// transaction. LLM and tool calls are not stored by this backend.
func (s *sqliteDB) FinishTurn(ctx context.Context, record TurnRecord) ([]string, error) {
	var ids []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(sqliteTimeLayout)
		warnings, err := json.Marshal(record.Warnings)
		if err != nil {
			return fmt.Errorf("marshal turn warnings: %w", err)
		}
		var errorClass *string
		if record.ErrorClass != "" {
			ec := string(record.ErrorClass)
			errorClass = &ec
		}
		var convID string
		err = tx.QueryRowContext(ctx,
			`UPDATE turns SET status = ?, error_class = ?, warnings = ?, completed_at = ?
			 WHERE id = ? AND status = ? RETURNING conversation_id`,
			record.Status, errorClass, string(warnings), now, record.TurnID, TurnRunning,
		).Scan(&convID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTurnNotRunning
		}
		if err != nil {
			return fmt.Errorf("complete turn: %w", err)
		}
		if ids, err = insertSQLiteMessages(ctx, tx, convID, record, now); err != nil {
			return err
		}
		if record.Status != TurnSucceeded {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE conversations SET updated_at = ? WHERE id = ?", now, convID); err != nil {
			return fmt.Errorf("touch conversation: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// insertSQLiteMessages stamps every message of the turn with the same
// created_at; GetMessages orders them by rowid within it.
func insertSQLiteMessages(ctx context.Context, tx *sql.Tx, convID string, record TurnRecord, now string) ([]string, error) {
	ids := make([]string, len(record.Messages))
	for i, m := range record.Messages {
		var toolCalls, toolCallID *string
		if len(m.ToolCalls) > 0 {
			b, err := json.Marshal(m.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("message %d: marshal tool calls: %w", i, err)
			}
			str := string(b)
			toolCalls = &str
		}
		if m.ToolCallID != "" {
			toolCallID = &m.ToolCallID
		}
		ids[i] = uuid.NewString()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO messages (id, conversation_id, turn_id, message_number, role, content, tool_calls, tool_call_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids[i], convID, record.TurnID, i, m.Role, m.Content, toolCalls, toolCallID, now,
		); err != nil {
			return nil, fmt.Errorf("insert message %d: %w", i, err)
		}
	}
	return ids, nil
}

// RecordTitle sets the conversation title; the call itself is not stored by
// this backend.
func (s *sqliteDB) RecordTitle(ctx context.Context, record TitleRecord) error {
	var owned int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM conversations WHERE id = ? AND user_id = ?", record.ConversationID, record.UserID,
	).Scan(&owned); err != nil {
		return err
	}
	if owned == 0 {
		return ErrConversationNotFound
	}
	if record.Title == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET title = ?, updated_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL",
		record.Title, time.Now().UTC().Format(sqliteTimeLayout), record.ConversationID, record.UserID,
	)
	return err
}

// inTx runs fn inside one transaction and commits when it returns nil.
func (s *sqliteDB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback after a successful Commit returns ErrTxDone, which is ignored.
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

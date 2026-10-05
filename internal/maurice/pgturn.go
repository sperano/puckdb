package maurice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// pgUniqueViolation is the SQLSTATE of a unique constraint violation.
const pgUniqueViolation = "23505"

// BeginTurn opens a turn in one short transaction; no transaction stays open
// across model calls. The conversation row is locked so turn starts on one
// conversation serialize; the partial unique index on running turns and the
// (user_id, idempotency_key) constraint back that up, and a violation of
// either means another request owns the turn (ErrTurnInProgress).
func (db *pgDB) BeginTurn(ctx context.Context, params BeginTurnParams) (*TurnStart, error) {
	user, err := parseUUID(params.UserID)
	if err != nil {
		return nil, err
	}
	var start *TurnStart
	err = db.inTx(ctx, func(q *sqlcdb.Queries) error {
		start, err = db.beginTurn(ctx, q, user, params)
		return err
	})
	if err != nil {
		return nil, err
	}
	return start, nil
}

func (db *pgDB) beginTurn(ctx context.Context, q *sqlcdb.Queries, user pgtype.UUID, params BeginTurnParams) (*TurnStart, error) {
	existing, err := q.GetTurnByKey(ctx, sqlcdb.GetTurnByKeyParams{
		UserID: user, IdempotencyKey: params.IdempotencyKey, StaleSeconds: staleCutoffSeconds(params),
	})
	switch {
	case err == nil:
		return replayPgTurn(ctx, q, params, existing)
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("look up idempotency key: %w", err)
	}

	start := &TurnStart{ConversationID: params.ConversationID}
	conv, err := lockOrCreateConversation(ctx, q, user, params.ConversationID)
	if err != nil {
		return nil, err
	}
	start.NewConversation = params.ConversationID == ""
	start.ConversationID = uuidToString(conv)

	if err := q.AbandonStaleTurns(ctx, sqlcdb.AbandonStaleTurnsParams{
		ConversationID: conv, StaleSeconds: staleCutoffSeconds(params),
	}); err != nil {
		return nil, fmt.Errorf("abandon stale turns: %w", err)
	}
	turn, err := q.InsertTurn(ctx, sqlcdb.InsertTurnParams{
		ConversationID: conv, UserID: user, IdempotencyKey: params.IdempotencyKey, RequestHash: params.RequestHash,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return nil, ErrTurnInProgress
	}
	if err != nil {
		return nil, fmt.Errorf("insert turn: %w", err)
	}
	start.TurnID = uuidToString(turn.ID)
	start.TurnNumber = int(turn.TurnNumber)
	return start, nil
}

func lockOrCreateConversation(ctx context.Context, q *sqlcdb.Queries, user pgtype.UUID, id string) (pgtype.UUID, error) {
	if id == "" {
		row, err := q.CreateConversation(ctx, user)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("create conversation: %w", err)
		}
		return row.ID, nil
	}
	_, conv, err := parseOwned(uuidToString(user), id)
	if err != nil {
		return pgtype.UUID{}, err
	}
	locked, err := q.LockConversation(ctx, sqlcdb.LockConversationParams{ID: conv, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrConversationNotFound
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("lock conversation: %w", err)
	}
	return locked, nil
}

func replayPgTurn(ctx context.Context, q *sqlcdb.Queries, params BeginTurnParams, row sqlcdb.GetTurnByKeyRow) (*TurnStart, error) {
	turn := storedTurn{
		ID: uuidToString(row.ID), ConversationID: uuidToString(row.ConversationID),
		RequestHash: row.RequestHash, Status: TurnStatus(row.Status),
		ErrorClass: ErrorClass(row.ErrorClass.String), Stale: row.Stale,
	}
	abandon, err := checkKeyReuse(params, turn)
	if err != nil {
		return nil, err
	}
	if abandon {
		if err := q.AbandonTurn(ctx, row.ID); err != nil {
			return nil, fmt.Errorf("abandon turn: %w", err)
		}
		return abandonedReplay(turn), nil
	}
	start := &TurnStart{
		TurnID: turn.ID, ConversationID: turn.ConversationID, TurnNumber: int(row.TurnNumber),
		Replay: &TurnReplay{Status: turn.Status, ErrorClass: turn.ErrorClass},
	}
	if turn.Status != TurnSucceeded {
		return start, nil
	}
	final, err := q.GetTurnFinalMessage(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("load replayed answer: %w", err)
	}
	start.Replay.MessageID, start.Replay.Content = uuidToString(final.ID), final.Content
	if start.Replay.ToolsUsed, err = pgTurnToolNames(ctx, q, row.ID); err != nil {
		return nil, err
	}
	return start, nil
}

func pgTurnToolNames(ctx context.Context, q *sqlcdb.Queries, turnID pgtype.UUID) ([]string, error) {
	raw, err := q.GetTurnToolCallLists(ctx, turnID)
	if err != nil {
		return nil, fmt.Errorf("load replayed tool calls: %w", err)
	}
	lists := make([][]llm.ToolCall, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &lists[i]); err != nil {
			return nil, fmt.Errorf("decode replayed tool calls: %w", err)
		}
	}
	return toolNames(lists), nil
}

// FinishTurn commits the turn's terminal status, messages, LLM calls, call
// input links and tool calls in one transaction. Only a succeeded turn bumps
// the conversation's activity timestamp.
func (db *pgDB) FinishTurn(ctx context.Context, record TurnRecord) ([]string, error) {
	turnID, err := parseUUID(record.TurnID)
	if err != nil {
		return nil, err
	}
	var ids []pgtype.UUID
	err = db.inTx(ctx, func(q *sqlcdb.Queries) error {
		conv, err := q.CompleteTurn(ctx, sqlcdb.CompleteTurnParams{
			ID: turnID, Status: string(record.Status), ErrorClass: optionalText(string(record.ErrorClass)),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTurnNotRunning
		}
		if err != nil {
			return fmt.Errorf("complete turn: %w", err)
		}
		if ids, err = insertTurnMessages(ctx, q, conv, turnID, record.Messages); err != nil {
			return err
		}
		if record.Status == TurnSucceeded {
			if err := q.TouchConversation(ctx, conv); err != nil {
				return fmt.Errorf("touch conversation: %w", err)
			}
		}
		for i, call := range record.Calls {
			if err := insertLLMCall(ctx, q, conv, turnID, call, ids); err != nil {
				return fmt.Errorf("llm call %d: %w", i, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = uuidToString(id)
	}
	return result, nil
}

func insertTurnMessages(ctx context.Context, q *sqlcdb.Queries, conv, turnID pgtype.UUID, msgs []TurnMessage) ([]pgtype.UUID, error) {
	base := time.Now().UTC()
	ids := make([]pgtype.UUID, len(msgs))
	for i, m := range msgs {
		params := sqlcdb.CreateMessageParams{
			ConversationID: conv,
			TurnID:         turnID,
			MessageNumber:  int32(i),
			Role:           sqlcdb.ChatRole(m.Role),
			Content:        m.Content,
			ToolCallID:     optionalText(m.ToolCallID),
			CreatedAt:      timestamptz(base.Add(time.Duration(i) * createdAtStep)),
		}
		if len(m.ToolCalls) > 0 {
			tc, err := json.Marshal(m.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("message %d: marshal tool calls: %w", i, err)
			}
			params.ToolCalls = tc
		}
		row, err := q.CreateMessage(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("insert message %d: %w", i, err)
		}
		ids[i] = row.ID
	}
	return ids, nil
}

// RecordTitle stores the title-generation call (its tokens count toward the
// conversation owner's usage) and sets the title in one transaction.
func (db *pgDB) RecordTitle(ctx context.Context, record TitleRecord) error {
	user, conv, err := parseOwned(record.UserID, record.ConversationID)
	if err != nil {
		return err
	}
	return db.inTx(ctx, func(q *sqlcdb.Queries) error {
		_, err := q.LockConversation(ctx, sqlcdb.LockConversationParams{ID: conv, UserID: user})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("lock conversation: %w", err)
		}
		if err := insertLLMCall(ctx, q, conv, pgtype.UUID{}, record.Call, nil); err != nil {
			return fmt.Errorf("title call: %w", err)
		}
		if record.Title == "" {
			return nil
		}
		return q.UpdateConversationTitle(ctx, sqlcdb.UpdateConversationTitleParams{
			ID: conv, UserID: user, Title: pgtype.Text{String: record.Title, Valid: true},
		})
	})
}

// inTx runs fn inside one transaction and commits when it returns nil.
func (db *pgDB) inTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a safe no-op after a successful Commit (documented in pgx).
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

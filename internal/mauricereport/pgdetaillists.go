package mauricereport

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Turns lists the turns of ref's conversation, by turn number, each with
// counts and usage of its messages, LLM calls and tool calls. found is false
// when the conversation is not found.
func (s *PGStore) Turns(ctx context.Context, ref ConversationRef, page PageRequest) (TurnPage, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return TurnPage{}, false, err
	}
	list := detailList[Turn]{
		kind: cursorTurns,
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]Turn, bool, error) {
			keys := afterKeys(after, 1)
			rows, err := tx.Query(ctx, `
				SELECT t.id::text, t.turn_number, t.status, t.error_class, t.started_at, t.completed_at,
				       CASE WHEN jsonb_typeof(t.warnings) = 'array'
				            THEN ARRAY(SELECT jsonb_array_elements_text(t.warnings)) ELSE '{}'::text[] END,
				       (SELECT count(*) FROM maurice_messages m WHERE m.turn_id = t.id),
				       (SELECT count(*) FROM maurice_tool_calls tc WHERE tc.turn_id = t.id),
				       ca.*, md.providers, md.models
				FROM maurice_turns t
				CROSS JOIN LATERAL (SELECT `+usageSQL+` FROM maurice_llm_calls WHERE turn_id = t.id) ca
				CROSS JOIN LATERAL (
					SELECT COALESCE(array_agg(provider ORDER BY provider, model), '{}') AS providers,
					       COALESCE(array_agg(model ORDER BY provider, model), '{}') AS models
					FROM (SELECT DISTINCT provider, model FROM maurice_llm_calls WHERE turn_id = t.id) used
				) md
				WHERE t.conversation_id = $1::uuid AND ($2::bigint IS NULL OR t.turn_number > $2)
				ORDER BY t.turn_number
				LIMIT $3`, r.conversationID.String(), keys[0], limit)
			turns, err := collect(rows, err, "turns", scanTurn)
			return turns, true, err
		},
		key: func(t Turn) cursor { return cursor{Keys: []int64{int64(t.Number)}} },
	}
	turns, next, found, err := list.page(ctx, s, r, page)
	return TurnPage{Turns: turns, NextCursor: next}, found, err
}

func scanTurn(row pgx.CollectableRow) (Turn, error) {
	var (
		t         Turn
		status    string
		providers []string
		models    []string
	)
	targets := []any{&t.ID, &t.Number, &status, &t.ErrorClass, &t.StartedAt, &t.CompletedAt,
		&t.Warnings, &t.Messages, &t.ToolCalls}
	targets = append(targets, usageTargets(&t.Usage)...)
	if err := row.Scan(append(targets, &providers, &models)...); err != nil {
		return Turn{}, err
	}
	t.Status = TurnStatus(status)
	finishUsage(&t.Usage)
	t.Models = make([]Model, len(providers))
	for i := range providers {
		t.Models[i] = Model{Provider: providers[i], Model: models[i]}
	}
	return t, nil
}

// TurnCalls lists the LLM calls of turn turnID of ref's conversation, by
// round (ties by ID). found is false when the conversation is not found or
// the turn is not one of its turns.
func (s *PGStore) TurnCalls(ctx context.Context, ref ConversationRef, turnID string, page PageRequest) (CallPage, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return CallPage{}, false, err
	}
	turn, err := parseID("turnId", turnID)
	if err != nil {
		return CallPage{}, false, err
	}
	list := detailList[Call]{
		kind:   cursorTurnCalls,
		parent: turn.String(),
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]Call, bool, error) {
			if ok, err := turnInConversation(ctx, tx, r, turn.String()); err != nil || !ok {
				return nil, false, err
			}
			keys := afterKeys(after, 2)
			rows, err := tx.Query(ctx, `
				SELECT `+callColumnsDetail+`
				FROM maurice_llm_calls l
				WHERE l.turn_id = $1::uuid AND l.conversation_id = $2::uuid
				  AND ($3::bigint IS NULL OR (l.round_number, l.id) > ($3, $4::bigint))
				ORDER BY l.round_number, l.id
				LIMIT $5`, turn.String(), r.conversationID.String(), keys[0], keys[1], limit)
			calls, err := collect(rows, err, "turn calls", scanCall)
			return calls, true, err
		},
		key: func(c Call) cursor { return cursor{Keys: []int64{int64(*c.Round), c.ID}} },
	}
	calls, next, found, err := list.page(ctx, s, r, page)
	return CallPage{Calls: calls, NextCursor: next}, found, err
}

// TitleCalls lists the title generation calls of ref's conversation (which
// belong to no turn), by start (ties by ID). found is false when the
// conversation is not found.
func (s *PGStore) TitleCalls(ctx context.Context, ref ConversationRef, page PageRequest) (CallPage, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return CallPage{}, false, err
	}
	list := detailList[Call]{
		kind: cursorTitleCalls,
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]Call, bool, error) {
			keys := afterKeys(after, 1)
			var at any
			if after != nil {
				at = *after.At
			}
			rows, err := tx.Query(ctx, `
				SELECT `+callColumnsDetail+`
				FROM maurice_llm_calls l
				WHERE l.conversation_id = $1::uuid AND l.turn_id IS NULL
				  AND ($2::timestamptz IS NULL OR (l.started_at, l.id) > ($2, $3::bigint))
				ORDER BY l.started_at, l.id
				LIMIT $4`, r.conversationID.String(), at, keys[0], limit)
			calls, err := collect(rows, err, "title calls", scanCall)
			return calls, true, err
		},
		key: func(c Call) cursor { return cursor{At: &c.StartedAt, Keys: []int64{c.ID}} },
	}
	calls, next, found, err := list.page(ctx, s, r, page)
	return CallPage{Calls: calls, NextCursor: next}, found, err
}

// ToolCalls lists the tool calls of turn turnID of ref's conversation, by
// ID, metadata only. found is false when the conversation is not found or
// the turn is not one of its turns.
func (s *PGStore) ToolCalls(ctx context.Context, ref ConversationRef, turnID string, page PageRequest) (ToolCallPage, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return ToolCallPage{}, false, err
	}
	turn, err := parseID("turnId", turnID)
	if err != nil {
		return ToolCallPage{}, false, err
	}
	list := detailList[ToolCall]{
		kind:   cursorToolCalls,
		parent: turn.String(),
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]ToolCall, bool, error) {
			if ok, err := turnInConversation(ctx, tx, r, turn.String()); err != nil || !ok {
				return nil, false, err
			}
			keys := afterKeys(after, 1)
			rows, err := tx.Query(ctx, `
				SELECT `+toolCallColumns+`
				FROM maurice_tool_calls tc
				JOIN maurice_turns t ON t.id = tc.turn_id
				WHERE tc.turn_id = $1::uuid AND t.conversation_id = $2::uuid
				  AND ($3::bigint IS NULL OR tc.id > $3)
				ORDER BY tc.id
				LIMIT $4`, turn.String(), r.conversationID.String(), keys[0], limit)
			calls, err := collect(rows, err, "tool calls", scanToolCall)
			return calls, true, err
		},
		key: func(c ToolCall) cursor { return cursor{Keys: []int64{c.ID}} },
	}
	calls, next, found, err := list.page(ctx, s, r, page)
	return ToolCallPage{ToolCalls: calls, NextCursor: next}, found, err
}

// turnInConversation reports whether turn is a turn of r's conversation.
func turnInConversation(ctx context.Context, tx pgx.Tx, r detailRef, turn string) (bool, error) {
	return exists(ctx, tx, "turn",
		`SELECT 1 FROM maurice_turns WHERE id = $1::uuid AND conversation_id = $2::uuid`, turn, r.conversationID.String())
}

// exists reports whether sql returns a row.
func exists(ctx context.Context, tx pgx.Tx, what, sql string, args ...any) (bool, error) {
	var one int
	err := tx.QueryRow(ctx, sql, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find %s: %w", what, err)
	}
	return true, nil
}

// callColumnsDetail are the columns scanCall reads of an LLM call l.
const callColumnsDetail = `l.id, l.turn_id::text, l.call_kind, l.round_number, l.provider, l.model,
	l.provider_request_id, l.status, l.error_class, l.finish_reason, l.started_at, l.completed_at,
	l.input_tokens, l.output_tokens, l.cache_creation_input_tokens, l.cache_read_input_tokens,
	(SELECT count(*) FROM maurice_llm_call_messages i WHERE i.llm_call_id = l.id),
	char_length(l.system_prompt), char_length(l.instruction), char_length(l.tool_definitions::text),
	(SELECT count(*) FROM maurice_tool_calls tc WHERE tc.llm_call_id = l.id)`

func scanCall(row pgx.CollectableRow) (Call, error) {
	var c Call
	err := row.Scan(callTargets(&c)...)
	return c, err
}

func callTargets(c *Call) []any {
	return []any{&c.ID, &c.TurnID, &c.Kind, &c.Round, &c.Provider, &c.Model,
		&c.ProviderRequestID, &c.Status, &c.ErrorClass, &c.FinishReason, &c.StartedAt, &c.CompletedAt,
		&c.InputTokens, &c.OutputTokens, &c.CacheCreationInputTokens, &c.CacheReadInputTokens,
		&c.InputMessages, &c.SystemPromptLength, &c.InstructionLength, &c.ToolDefinitionsLength, &c.ToolCalls}
}

// toolCallColumns are the columns scanToolCall reads of a tool call tc.
const toolCallColumns = `tc.id, tc.turn_id::text, tc.llm_call_id, tc.message_id::text, tc.sequence_number,
	tc.provider_tool_call_id, tc.tool_type, tc.tool_name, tc.status, tc.started_at, tc.completed_at,
	tc.arguments IS NOT NULL, char_length(tc.arguments_raw), char_length(tc.result)`

func scanToolCall(row pgx.CollectableRow) (ToolCall, error) {
	var c ToolCall
	err := row.Scan(toolCallTargets(&c)...)
	return c, err
}

func toolCallTargets(c *ToolCall) []any {
	return []any{&c.ID, &c.TurnID, &c.CallID, &c.MessageID, &c.Sequence,
		&c.ProviderToolCallID, &c.Type, &c.Name, &c.Status, &c.StartedAt, &c.CompletedAt,
		&c.ArgumentsValid, &c.ArgumentsRawLength, &c.ResultLength}
}

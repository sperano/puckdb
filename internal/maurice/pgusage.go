package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// insertLLMCall stores one provider call with its input links and tool calls.
// turnID is invalid (NULL) for title generation; turnMessageIDs resolves
// MessageRef.TurnIndex.
func insertLLMCall(ctx context.Context, q *sqlcdb.Queries, conv, turnID pgtype.UUID, call LLMCallRecord, turnMessageIDs []pgtype.UUID) ([]storedToolLink, error) {
	params, err := llmCallParams(conv, turnID, call)
	if err != nil {
		return nil, err
	}
	callID, err := q.InsertLLMCall(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	if err := insertCallInputs(ctx, q, callID, call.Inputs, turnMessageIDs); err != nil {
		return nil, err
	}
	links := make([]storedToolLink, 0, len(call.ToolCalls))
	for i, tc := range call.ToolCalls {
		params := toolCallParams(turnID, callID, i, tc)
		if call.OutputMessageIndex >= 0 && call.OutputMessageIndex < len(turnMessageIDs) {
			params.MessageID = turnMessageIDs[call.OutputMessageIndex]
		}
		id, err := q.InsertToolCall(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("insert tool call %d: %w", i, err)
		}
		links = append(links, storedToolLink{
			id: id, providerID: tc.ProviderToolCallID, outputMessageIndex: call.OutputMessageIndex, sequence: i,
		})
	}
	return links, nil
}

func llmCallParams(conv, turnID pgtype.UUID, call LLMCallRecord) (sqlcdb.InsertLLMCallParams, error) {
	params := sqlcdb.InsertLLMCallParams{
		TurnID:                   turnID,
		ConversationID:           conv,
		CallKind:                 string(call.Kind),
		ProviderRequestID:        optionalText(call.ProviderRequestID),
		Provider:                 call.Provider,
		Model:                    call.Model,
		Status:                   string(call.Status),
		FinishReason:             optionalText(call.FinishReason),
		StartedAt:                timestamptz(call.StartedAt),
		CompletedAt:              timestamptz(call.CompletedAt),
		InputTokens:              optionalInt8(call.Usage.Input),
		OutputTokens:             optionalInt8(call.Usage.Output),
		CacheCreationInputTokens: optionalInt8(call.Usage.CacheCreation),
		CacheReadInputTokens:     optionalInt8(call.Usage.CacheRead),
		ErrorClass:               optionalText(string(call.ErrorClass)),
		SystemPrompt:             optionalTextPtr(call.SystemPrompt),
		Instruction:              optionalTextPtr(call.Instruction),
	}
	if call.Round != nil {
		params.RoundNumber = pgtype.Int4{Int32: int32(*call.Round), Valid: true}
	}
	if call.ToolDefinitions != nil {
		defs, err := json.Marshal(call.ToolDefinitions)
		if err != nil {
			return params, fmt.Errorf("marshal tool definitions: %w", err)
		}
		params.ToolDefinitions = defs
	}
	return params, nil
}

func insertCallInputs(ctx context.Context, q *sqlcdb.Queries, callID int64, inputs []MessageRef, turnMessageIDs []pgtype.UUID) error {
	if len(inputs) == 0 {
		return nil
	}
	numbers := make([]int32, len(inputs))
	ids := make([]pgtype.UUID, len(inputs))
	for i, ref := range inputs {
		numbers[i] = int32(i)
		if ref.MessageID == "" {
			if ref.TurnIndex < 0 || ref.TurnIndex >= len(turnMessageIDs) {
				return fmt.Errorf("input %d: turn message %d out of range", i, ref.TurnIndex)
			}
			ids[i] = turnMessageIDs[ref.TurnIndex]
			continue
		}
		id, err := parseUUID(ref.MessageID)
		if err != nil {
			return fmt.Errorf("input %d: %w", i, err)
		}
		ids[i] = id
	}
	if err := q.InsertLLMCallMessages(ctx, sqlcdb.InsertLLMCallMessagesParams{
		LlmCallID: callID, InputNumbers: numbers, MessageIds: ids,
	}); err != nil {
		return fmt.Errorf("insert inputs: %w", err)
	}
	return nil
}

func toolCallParams(turnID pgtype.UUID, callID int64, seq int, tc ToolCallRecord) sqlcdb.InsertToolCallParams {
	params := sqlcdb.InsertToolCallParams{
		TurnID:             turnID,
		LlmCallID:          pgtype.Int8{Int64: callID, Valid: true},
		SequenceNumber:     int32(seq),
		ProviderToolCallID: optionalText(tc.ProviderToolCallID),
		ToolType:           defaultToolType(tc.Type),
		ToolName:           tc.Name,
		Arguments:          jsonbArguments(tc.ArgumentsRaw),
		ArgumentsRaw:       tc.ArgumentsRaw,
		Result:             optionalTextPtr(tc.Result),
		Status:             optionalText(string(tc.Status)),
	}
	if tc.StartedAt != nil {
		params.StartedAt = timestamptz(*tc.StartedAt)
	}
	if tc.CompletedAt != nil {
		params.CompletedAt = timestamptz(*tc.CompletedAt)
	}
	return params
}

const (
	defaultFunctionToolType = "function"
	jsonNullEscape          = `\u0000`
)

func defaultToolType(toolType string) string {
	if toolType == "" {
		return defaultFunctionToolType
	}
	return toolType
}

func jsonbArguments(raw string) []byte {
	if !json.Valid([]byte(raw)) || strings.Contains(raw, jsonNullEscape) {
		return nil
	}
	return []byte(raw)
}

func optionalInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

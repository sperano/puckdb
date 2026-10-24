package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// turnChildQueries writes the child rows of one sim_agent_turns row:
// rounds, tool calls and the transcript. Every turn write (the commit's
// RecordTurnTelemetry and the aborted-turn write) needs it.
type turnChildQueries interface {
	InsertSimAgentTurnRound(ctx context.Context, arg []sqlcdb.InsertSimAgentTurnRoundParams) (int64, error)
	InsertSimAgentToolCall(ctx context.Context, arg []sqlcdb.InsertSimAgentToolCallParams) (int64, error)
	InsertSimAgentTurnMessage(ctx context.Context, arg []sqlcdb.InsertSimAgentTurnMessageParams) (int64, error)
}

// turnHeaderParams is the sim_agent_turns row of t. Totals and cost come
// from t.Header.Spend; rounds and latency from t.Captures.
func turnHeaderParams(t TurnTelemetry) (sqlcdb.InsertSimAgentTurnParams, error) {
	header := t.Header
	spend := header.Spend
	costNum, err := pgconv.NumericFromFloat(spend.CostUsd)
	if err != nil {
		return sqlcdb.InsertSimAgentTurnParams{}, fmt.Errorf("simulation: encode turn cost: %w", err)
	}
	return sqlcdb.InsertSimAgentTurnParams{
		PoolID:              header.PoolID,
		AgentID:             header.AgentID,
		SimDate:             header.SimDate,
		Phase:               string(header.Phase),
		PickNumber:          header.PickNumber,
		Status:              string(header.Status),
		SkipReason:          textOrNull(header.SkipReason),
		ErrorKind:           textOrNull(string(header.ErrorKind)),
		ErrorDetail:         textOrNull(header.ErrorDetail),
		Provider:            header.Provider,
		Model:               header.Model,
		Temperature:         numericOrNull(header.Temperature),
		MaxTokens:           int4OrNull(int32(header.MaxTokens)),
		Rounds:              clampTokenCount(len(t.Captures)),
		PromptTokens:        clampTokenCount(spend.Usage.PromptTokens),
		CompletionTokens:    clampTokenCount(spend.Usage.CompletionTokens),
		CacheCreationTokens: clampTokenCount(spend.Usage.CacheCreationInputTokens),
		CacheReadTokens:     clampTokenCount(spend.Usage.CacheReadInputTokens),
		CostUSD:             costNum,
		LatencyMs:           durationToMs(sumLatency(t.Captures)),
		FinalText:           header.FinalText,
		StartedAt:           pgtype.Timestamptz{Time: header.StartedAt, Valid: true},
		CompletedAt:         pgtype.Timestamptz{Time: header.CompletedAt, Valid: true},
	}, nil
}

// recordTurnChildren writes the rounds, tool calls and (when
// t.RecordMessages is true) the transcript of turn turnID.
func recordTurnChildren(ctx context.Context, q turnChildQueries, turnID int32, t TurnTelemetry) error {
	if err := recordTurnRounds(ctx, q, turnID, t.Captures); err != nil {
		return err
	}
	if err := recordTurnToolCalls(ctx, q, turnID, t.ToolCalls); err != nil {
		return err
	}
	if t.RecordMessages {
		return recordTurnMessages(ctx, q, turnID, t.Messages)
	}
	return nil
}

func recordTurnRounds(ctx context.Context, q turnChildQueries, turnID int32, captures []roundCapture) error {
	if len(captures) == 0 {
		return nil
	}
	rounds := make([]sqlcdb.InsertSimAgentTurnRoundParams, 0, len(captures))
	for i, c := range captures {
		var u llm.Usage
		var text string
		if c.Response != nil {
			if c.Response.Usage != nil {
				u = *c.Response.Usage
			}
			text = c.Response.Content
		}
		rounds = append(rounds, sqlcdb.InsertSimAgentTurnRoundParams{
			TurnID:              turnID,
			RoundIndex:          int32(i),
			AssistantText:       text,
			PromptTokens:        clampTokenCount(u.PromptTokens),
			CompletionTokens:    clampTokenCount(u.CompletionTokens),
			CacheCreationTokens: clampTokenCount(u.CacheCreationInputTokens),
			CacheReadTokens:     clampTokenCount(u.CacheReadInputTokens),
			LatencyMs:           durationToMs(c.Latency),
		})
	}
	if _, err := q.InsertSimAgentTurnRound(ctx, rounds); err != nil {
		return fmt.Errorf("simulation: insert turn rounds: %w", err)
	}
	return nil
}

func recordTurnToolCalls(ctx context.Context, q turnChildQueries, turnID int32, toolCalls []ToolCallCapture) error {
	if len(toolCalls) == 0 {
		return nil
	}
	calls := make([]sqlcdb.InsertSimAgentToolCallParams, 0, len(toolCalls))
	for _, tc := range toolCalls {
		calls = append(calls, sqlcdb.InsertSimAgentToolCallParams{
			TurnID:               turnID,
			RoundIndex:           int32(tc.RoundIndex),
			Sequence:             int32(tc.Sequence),
			ToolName:             tc.ToolName,
			RecoveredName:        textOrNull(tc.RecoveredName),
			ArgumentsRaw:         tc.ArgumentsRaw,
			Arguments:            argumentsJSONB(tc.ArgumentsRaw),
			Result:               tc.Result,
			Outcome:              string(tc.Outcome),
			FailureReason:        textOrNull(tc.FailureReason),
			AppliedTransactionID: int4OrNull(tc.AppliedTransactionID),
			LatencyMs:            durationToMs(tc.Latency),
		})
	}
	if _, err := q.InsertSimAgentToolCall(ctx, calls); err != nil {
		return fmt.Errorf("simulation: insert tool calls: %w", err)
	}
	return nil
}

func recordTurnMessages(ctx context.Context, q turnChildQueries, turnID int32, messages []llm.Message) error {
	if len(messages) == 0 {
		return nil
	}
	rows := make([]sqlcdb.InsertSimAgentTurnMessageParams, 0, len(messages))
	for i, m := range messages {
		rows = append(rows, sqlcdb.InsertSimAgentTurnMessageParams{
			TurnID:     turnID,
			Ordinal:    int32(i),
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: textOrNull(m.ToolCallID),
			ToolCalls:  toolCallsJSONB(m.ToolCalls),
		})
	}
	if _, err := q.InsertSimAgentTurnMessage(ctx, rows); err != nil {
		return fmt.Errorf("simulation: insert turn messages: %w", err)
	}
	return nil
}

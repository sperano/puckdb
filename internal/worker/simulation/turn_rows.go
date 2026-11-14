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
	LinkSimAgentTurnMessageResults(ctx context.Context, arg sqlcdb.LinkSimAgentTurnMessageResultsParams) error
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
	links := transcriptLinks{}
	if t.RecordMessages {
		if err := recordTurnMessages(ctx, q, turnID, t.Messages); err != nil {
			return err
		}
		links = buildTranscriptLinks(t.Messages)
	}
	if err := recordTurnToolCalls(ctx, q, turnID, t.ToolCalls, links); err != nil {
		return err
	}
	return linkTurnResultMessages(ctx, q, turnID, links.results)
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

func recordTurnToolCalls(ctx context.Context, q turnChildQueries, turnID int32, toolCalls []ToolCallCapture, links transcriptLinks) error {
	if len(toolCalls) == 0 {
		return nil
	}
	calls := make([]sqlcdb.InsertSimAgentToolCallParams, 0, len(toolCalls))
	for _, tc := range toolCalls {
		messageOrdinal, linked := links.calls[toolCoordinate{round: tc.RoundIndex, sequence: tc.Sequence}]
		calls = append(calls, sqlcdb.InsertSimAgentToolCallParams{
			TurnID:               turnID,
			RoundIndex:           int32(tc.RoundIndex),
			Sequence:             int32(tc.Sequence),
			MessageOrdinal:       nullableInt4(messageOrdinal, linked),
			ProviderToolCallID:   textOrNull(tc.ProviderToolCallID),
			ToolType:             defaultSimulationToolType(tc.ToolType),
			ToolName:             tc.ToolName,
			RecoveredName:        textOrNull(tc.RecoveredName),
			ArgumentsRaw:         tc.ArgumentsRaw,
			Arguments:            simulationArgumentsJSONB(tc.ArgumentsRaw),
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
			TurnID:  turnID,
			Ordinal: int32(i),
			Role:    m.Role,
			Content: m.Content,
		})
	}
	if _, err := q.InsertSimAgentTurnMessage(ctx, rows); err != nil {
		return fmt.Errorf("simulation: insert turn messages: %w", err)
	}
	return nil
}

const simulationFunctionToolType = "function"

type toolCoordinate struct {
	round    int
	sequence int
}

type resultMessageLink struct {
	ordinal    int32
	coordinate toolCoordinate
}

type transcriptLinks struct {
	calls   map[toolCoordinate]int32
	results []resultMessageLink
}

type providerCallLink struct {
	providerID string
	ordinal    int
	coordinate toolCoordinate
}

func buildTranscriptLinks(messages []llm.Message) transcriptLinks {
	links := transcriptLinks{calls: make(map[toolCoordinate]int32)}
	var providerCalls []providerCallLink
	round := 0
	for ordinal, message := range messages {
		if len(message.ToolCalls) > 0 {
			for sequence, call := range message.ToolCalls {
				coordinate := toolCoordinate{round: round, sequence: sequence}
				links.calls[coordinate] = int32(ordinal)
				providerCalls = append(providerCalls, providerCallLink{
					providerID: call.ID, ordinal: ordinal, coordinate: coordinate,
				})
			}
			round++
		}
		if message.ToolCallID != "" {
			if coordinate, ok := precedingProviderCall(providerCalls, message.ToolCallID, ordinal); ok {
				links.results = append(links.results, resultMessageLink{ordinal: int32(ordinal), coordinate: coordinate})
			}
		}
	}
	return links
}

func precedingProviderCall(calls []providerCallLink, providerID string, ordinal int) (toolCoordinate, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].providerID == providerID && calls[i].ordinal < ordinal {
			return calls[i].coordinate, true
		}
	}
	return toolCoordinate{}, false
}

func linkTurnResultMessages(ctx context.Context, q turnChildQueries, turnID int32, links []resultMessageLink) error {
	if len(links) == 0 {
		return nil
	}
	params := sqlcdb.LinkSimAgentTurnMessageResultsParams{TurnID: turnID}
	for _, link := range links {
		params.Ordinals = append(params.Ordinals, link.ordinal)
		params.RoundIndexes = append(params.RoundIndexes, int32(link.coordinate.round))
		params.Sequences = append(params.Sequences, int32(link.coordinate.sequence))
	}
	if err := q.LinkSimAgentTurnMessageResults(ctx, params); err != nil {
		return fmt.Errorf("simulation: link tool result messages: %w", err)
	}
	return nil
}

func defaultSimulationToolType(toolType string) string {
	if toolType == "" {
		return simulationFunctionToolType
	}
	return toolType
}

func nullableInt4(value int32, valid bool) pgtype.Int4 {
	return pgtype.Int4{Int32: value, Valid: valid}
}

package simulation

import (
	"context"
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture builds a roundCapture carrying a response with the given usage.
func capture(u llm.Usage) roundCapture {
	return roundCapture{Response: &llm.Response{Content: "ok", Usage: &u}}
}

// TestRecordTurnTelemetryAggregationEquality pins the B16 invariant: the
// parent sim_agent_turns row's token totals equal the sum of its per-round
// child rows, because both are derived from the same captures.
func TestRecordTurnTelemetryAggregationEquality(t *testing.T) {
	q := &stubSimQueries{}

	captures := []roundCapture{
		capture(llm.Usage{PromptTokens: 100, CompletionTokens: 20, CacheCreationInputTokens: 5, CacheReadInputTokens: 3}),
		capture(llm.Usage{PromptTokens: 200, CompletionTokens: 40, CacheCreationInputTokens: 7, CacheReadInputTokens: 11}),
		{Response: nil}, // provider error mid-call: contributes nothing
	}

	header := TurnHeader{
		Provider: PricingProviderAnthropic,
		Model:    "claude-haiku-4-5",
		Phase:    TurnPhaseDaily,
		Status:   TurnStatusOK,
		// CostUsd deliberately set to a bogus value the recorder must ignore.
		CostUsd: 999.0,
	}

	_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{Header: header, Captures: captures})
	require.NoError(t, err)

	require.Len(t, q.insertTurnCalls, 1)
	require.Len(t, q.insertTurnRoundBatches, 1)
	parent := q.insertTurnCalls[0]
	rounds := q.insertTurnRoundBatches[0]

	// Expected sums come from the captures only (nil-response skipped).
	const (
		wantPrompt     = 300
		wantCompletion = 60
		wantCacheCreat = 12
		wantCacheRead  = 14
	)
	assert.EqualValues(t, wantPrompt, parent.PromptTokens)
	assert.EqualValues(t, wantCompletion, parent.CompletionTokens)
	assert.EqualValues(t, wantCacheCreat, parent.CacheCreationTokens)
	assert.EqualValues(t, wantCacheRead, parent.CacheReadTokens)

	// Parent totals must equal the sum of the child round rows.
	var sumPrompt, sumComp, sumCC, sumCR int32
	for _, r := range rounds {
		sumPrompt += r.PromptTokens
		sumComp += r.CompletionTokens
		sumCC += r.CacheCreationTokens
		sumCR += r.CacheReadTokens
	}
	assert.Equal(t, parent.PromptTokens, sumPrompt, "parent prompt tokens must equal sum of child rows")
	assert.Equal(t, parent.CompletionTokens, sumComp, "parent completion tokens must equal sum of child rows")
	assert.Equal(t, parent.CacheCreationTokens, sumCC, "parent cache-creation tokens must equal sum of child rows")
	assert.Equal(t, parent.CacheReadTokens, sumCR, "parent cache-read tokens must equal sum of child rows")
}

// TestRecordTurnTelemetryCostFromCaptures pins that cost_usd is priced off
// the captures-derived usage (not header.CostUsd), so it stays consistent
// with the recorded token columns.
func TestRecordTurnTelemetryCostFromCaptures(t *testing.T) {
	q := &stubSimQueries{}

	captures := []roundCapture{
		capture(llm.Usage{PromptTokens: 1_000, CompletionTokens: 500, CacheCreationInputTokens: 100, CacheReadInputTokens: 200}),
	}
	header := TurnHeader{
		Provider: PricingProviderAnthropic,
		Model:    "claude-haiku-4-5",
		Phase:    TurnPhaseDaily,
		Status:   TurnStatusOK,
		CostUsd:  42.0, // must be ignored
	}

	_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{Header: header, Captures: captures})
	require.NoError(t, err)
	require.Len(t, q.insertTurnCalls, 1)

	wantUsage := usageFromAggregate(aggregateUsage(captures))
	wantCost := EstimateCost(header.Provider, header.Model, wantUsage)
	require.Greater(t, wantCost, 0.0, "sanity: known model must have nonzero cost")

	gotCost, err := NumericToFloat(q.insertTurnCalls[0].CostUSD)
	require.NoError(t, err)
	assert.InDelta(t, wantCost, gotCost, 1e-9)
	assert.NotEqualValues(t, header.CostUsd, gotCost, "cost must not come from header.CostUsd")
}

// TestRecordTurnTelemetry_MessagesGatedByRecordMessages pins the
// TurnTelemetry.RecordMessages / Messages plumbing introduced when
// RecordTurnTelemetry's 8 positional params were collapsed into one
// struct: messages are only persisted when RecordMessages is true, and
// ToolCalls land regardless of that flag.
func TestRecordTurnTelemetry_MessagesGatedByRecordMessages(t *testing.T) {
	header := TurnHeader{
		Provider: PricingProviderAnthropic,
		Model:    "claude-haiku-4-5",
		Phase:    TurnPhaseDaily,
		Status:   TurnStatusOK,
	}
	messages := []llm.Message{{Role: "user", Content: "hello"}}
	toolCalls := []ToolCallCapture{{ToolName: ToolUpdateNotes, Outcome: ToolCallOutcomeAccepted}}

	t.Run("RecordMessages true persists messages", func(t *testing.T) {
		q := &stubSimQueries{}
		_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{
			Header:         header,
			ToolCalls:      toolCalls,
			Messages:       messages,
			RecordMessages: true,
		})
		require.NoError(t, err)
		require.Len(t, q.insertTurnMessageBatches, 1)
		assert.Len(t, q.insertTurnMessageBatches[0], 1)
		require.Len(t, q.insertToolCallBatches, 1)
		assert.Len(t, q.insertToolCallBatches[0], 1)
	})

	t.Run("RecordMessages false skips messages but keeps tool calls", func(t *testing.T) {
		q := &stubSimQueries{}
		_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{
			Header:         header,
			ToolCalls:      toolCalls,
			Messages:       messages,
			RecordMessages: false,
		})
		require.NoError(t, err)
		assert.Empty(t, q.insertTurnMessageBatches, "messages must not be written when RecordMessages is false")
		require.Len(t, q.insertToolCallBatches, 1)
	})
}

// TestClampTokenCount covers the int4 narrowing boundary directly.
func TestClampTokenCount(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{"zero", 0, 0},
		{"typical", 12345, 12345},
		{"max int32 exact", math.MaxInt32, math.MaxInt32},
		{"one over max int32", math.MaxInt32 + 1, math.MaxInt32},
		{"far over max int32", math.MaxInt64, math.MaxInt32},
		{"negative collapses to zero", -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clampTokenCount(tt.in))
		})
	}
}

// TestRecordTurnTelemetryClampsOverflowingTokens proves that an
// out-of-int32-range token count is saturated rather than wrapped to a
// negative value when written to the token columns.
func TestRecordTurnTelemetryClampsOverflowingTokens(t *testing.T) {
	q := &stubSimQueries{}

	captures := []roundCapture{
		capture(llm.Usage{PromptTokens: math.MaxInt32, CompletionTokens: math.MaxInt32}),
		capture(llm.Usage{PromptTokens: 10, CompletionTokens: 10}),
	}
	header := TurnHeader{
		Provider: PricingProviderAnthropic,
		Model:    "claude-haiku-4-5",
		Phase:    TurnPhaseDaily,
		Status:   TurnStatusOK,
	}

	_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{Header: header, Captures: captures})
	require.NoError(t, err)
	require.Len(t, q.insertTurnCalls, 1)

	parent := q.insertTurnCalls[0]
	// Sum of the two prompt-token captures overflows int32; must clamp,
	// never wrap negative.
	assert.EqualValues(t, math.MaxInt32, parent.PromptTokens)
	assert.EqualValues(t, math.MaxInt32, parent.CompletionTokens)
	assert.GreaterOrEqual(t, parent.PromptTokens, int32(0), "clamped token count must never be negative")
}

// transcriptForRecording is the single gate every phase uses to decide
// what TurnTelemetry.Messages carries: the loop's history only when the
// flag is on AND the loop produced a result.
func TestTranscriptForRecording(t *testing.T) {
	history := []llm.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "prompt"}}
	res := &agentloop.Result{Messages: history}

	assert.Equal(t, history, transcriptForRecording(true, res))
	assert.Nil(t, transcriptForRecording(false, res), "flag off → nothing to record")
	assert.Nil(t, transcriptForRecording(true, nil), "no loop result → nothing to record")
}

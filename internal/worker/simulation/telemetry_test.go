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

// telemetryTestAgent is the priced agent the telemetry tests bill against.
var telemetryTestAgent = AgentConfig{Provider: PricingProviderAnthropic, Model: "claude-haiku-4-5"}

// telemetryHeader returns a daily-turn header for telemetryTestAgent
// carrying spend.
func telemetryHeader(spend TurnSpend) TurnHeader {
	return TurnHeader{
		Provider: telemetryTestAgent.Provider,
		Model:    telemetryTestAgent.Model,
		Phase:    TurnPhaseDaily,
		Status:   TurnStatusOK,
		Spend:    spend,
	}
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

	header := telemetryHeader(turnSpendOf(telemetryTestAgent, captures))
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

// SIM-D3: RecordTurnTelemetry writes the spend its caller computed (and
// added to the pool total) instead of re-summing and re-pricing the
// captures. The header carries a spend that differs from what the
// captures would price to, so a re-pricing path would be caught.
func TestRecordTurnTelemetryWritesHeaderSpend(t *testing.T) {
	q := &stubSimQueries{}

	captures := []roundCapture{
		capture(llm.Usage{PromptTokens: 1_000, CompletionTokens: 500, CacheCreationInputTokens: 100, CacheReadInputTokens: 200}),
	}
	spend := TurnSpend{
		Usage:   llm.Usage{PromptTokens: 7, CompletionTokens: 8, CacheCreationInputTokens: 9, CacheReadInputTokens: 10},
		CostUsd: 0.123456,
	}
	require.NotEqual(t, turnSpendOf(telemetryTestAgent, captures), spend, "sanity: the probe spend differs from the captures")

	_, err := RecordTurnTelemetry(context.Background(), q, TurnTelemetry{Header: telemetryHeader(spend), Captures: captures})
	require.NoError(t, err)
	require.Len(t, q.insertTurnCalls, 1)
	parent := q.insertTurnCalls[0]

	gotCost, err := NumericToFloat(parent.CostUSD)
	require.NoError(t, err)
	assert.Equal(t, spend.CostUsd, gotCost, "cost_usd is the caller's spend, not a re-pricing")
	assert.EqualValues(t, spend.Usage.PromptTokens, parent.PromptTokens)
	assert.EqualValues(t, spend.Usage.CompletionTokens, parent.CompletionTokens)
	assert.EqualValues(t, spend.Usage.CacheCreationInputTokens, parent.CacheCreationTokens)
	assert.EqualValues(t, spend.Usage.CacheReadInputTokens, parent.CacheReadTokens)
}

// turnSpendOf prices every token class at its own rate: one million
// tokens of a single class costs exactly that class's per-million rate.
func TestTurnSpendOfPricesEveryTokenClass(t *testing.T) {
	const million = 1_000_000
	p, ok := LookupPricing(telemetryTestAgent.Provider, telemetryTestAgent.Model)
	require.True(t, ok)

	tests := []struct {
		name  string
		usage llm.Usage
		rate  float64
	}{
		{"prompt", llm.Usage{PromptTokens: million}, p.Input},
		{"completion", llm.Usage{CompletionTokens: million}, p.Output},
		{"cache creation", llm.Usage{CacheCreationInputTokens: million}, p.CacheCreation},
		{"cache read", llm.Usage{CacheReadInputTokens: million}, p.CacheRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Positive(t, tt.rate, "sanity: the test model prices this class")
			spend := turnSpendOf(telemetryTestAgent, []roundCapture{capture(tt.usage)})
			assert.Equal(t, tt.usage, spend.Usage)
			assert.InDelta(t, tt.rate, spend.CostUsd, 1e-12)
		})
	}
}

// turnSpendOf sums every round (all token classes, cache accounting
// flag included), skips rounds without a response or usage, and prices
// the sum once: the result equals the sum of the per-round prices.
func TestTurnSpendOfSumsRounds(t *testing.T) {
	first := llm.Usage{PromptTokens: 1_200, CompletionTokens: 300, TotalTokens: 1_500, CacheCreationInputTokens: 4_000, CacheReadInputTokens: 0, CacheReported: true}
	second := llm.Usage{PromptTokens: 800, CompletionTokens: 150, TotalTokens: 950, CacheCreationInputTokens: 0, CacheReadInputTokens: 4_000, CacheReported: true}
	captures := []roundCapture{
		capture(first),
		{Response: nil},                         // provider error mid-call
		{Response: &llm.Response{Content: "x"}}, // response without usage
		capture(second),
	}

	spend := turnSpendOf(telemetryTestAgent, captures)

	assert.Equal(t, llm.Usage{
		PromptTokens: 2_000, CompletionTokens: 450, TotalTokens: 2_450,
		CacheCreationInputTokens: 4_000, CacheReadInputTokens: 4_000, CacheReported: true,
	}, spend.Usage)
	perRound := EstimateCost(telemetryTestAgent.Provider, telemetryTestAgent.Model, first) +
		EstimateCost(telemetryTestAgent.Provider, telemetryTestAgent.Model, second)
	assert.InDelta(t, perRound, spend.CostUsd, 1e-12)
	assert.Equal(t, TurnSpend{}, turnSpendOf(telemetryTestAgent, nil), "no rounds, no spend")
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
	header := telemetryHeader(turnSpendOf(telemetryTestAgent, captures))

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

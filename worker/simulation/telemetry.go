package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/llm/agentloop"
	"github.com/sperano/puckdb/sqlcdb"
)

// maxTokenColumnValue is the largest value the token columns
// (sim_agent_turns / sim_agent_turn_rounds prompt_tokens,
// completion_tokens, cache_creation_tokens, cache_read_tokens) can hold.
// Those columns are Postgres INT (int4), so a summed token count that
// overflows int32 must be clamped rather than narrowed — an unguarded
// int32(v) conversion would wrap to a negative count and corrupt the
// spend/telemetry accounting. Real turns never approach this bound; the
// clamp is a guard against a pathological or corrupt usage value.
const maxTokenColumnValue = math.MaxInt32

// clampTokenCount narrows a (non-negative) summed token count to the
// int4 width of the telemetry token columns, saturating at
// maxTokenColumnValue instead of wrapping. A negative input (only
// reachable via corruption) collapses to zero.
func clampTokenCount(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > maxTokenColumnValue {
		return maxTokenColumnValue
	}
	return int32(v)
}

// ============================================================================
// Turn telemetry — the persistence-side counterpart to the agentloop
// transcript. Captures every LLM round, every tool call (accepted or
// rejected), and (when sim_pools.record_full_messages is true) the full
// conversation as a row set keyed off a parent sim_agent_turns row.
//
// Wiring: each LLM-driven activity (PickTeamName, DraftPick, ManageRoster)
// constructs a TurnHeader summarizing the turn, accumulates a
// []ToolCallCapture as the executor runs, and calls RecordTurnTelemetry
// from inside the same Transactor.InTx block as its sim_transactions
// writes. All-or-nothing semantics: a crash mid-commit leaves neither the
// fantasy rows nor the telemetry rows, so the next retry sees a clean
// "needs to be re-run" state.
//
// See database/migrations/000018_simulation_turn_telemetry.up.sql for the
// table layout and the reasoning behind partial unique indexes.
// ============================================================================

// TurnPhase enumerates sim_agent_turns.phase. Values match the CHECK
// constraint in the migration.
type TurnPhase string

const (
	TurnPhaseTeamName TurnPhase = "team_name"
	TurnPhaseDraft    TurnPhase = "draft"
	TurnPhaseDaily    TurnPhase = "daily"
)

// TurnStatus enumerates sim_agent_turns.status.
type TurnStatus string

const (
	TurnStatusOK      TurnStatus = "ok"
	TurnStatusErrored TurnStatus = "errored"
	TurnStatusSkipped TurnStatus = "skipped"
)

// ToolCallOutcome enumerates sim_agent_tool_calls.outcome.
//
// "accepted"            — executor recorded the action and mutated working state.
// "parse_error"         — RecoverAction couldn't materialize an Action.
// "validation_rejected" — Action recovered but validator returned an error.
// "unknown_tool"        — tool name didn't match any in-phase tool.
// "unhandled"           — Action recovered but the executor's switch had no case (V1: only fires for cross-phase tool use, e.g. draft_player called during a daily turn).
type ToolCallOutcome string

const (
	ToolCallOutcomeAccepted           ToolCallOutcome = "accepted"
	ToolCallOutcomeParseError         ToolCallOutcome = "parse_error"
	ToolCallOutcomeValidationRejected ToolCallOutcome = "validation_rejected"
	ToolCallOutcomeUnknownTool        ToolCallOutcome = "unknown_tool"
	ToolCallOutcomeUnhandled          ToolCallOutcome = "unhandled"
	// ToolCallOutcomeCommitRejected — the action passed turn-time
	// validation but lost a commit-time recheck (e.g. another agent took
	// the player first this same day). Distinguished from
	// validation_rejected so the audit trail shows the "first-processed
	// agent wins" loss rather than a model mistake.
	ToolCallOutcomeCommitRejected ToolCallOutcome = "commit_rejected"
)

// ToolCallCapture is the per-tool-call data the executor accumulates as
// agentloop processes each ToolCall.
//
// RoundIndex starts as zero in the executor (the closure can't see
// agentloop's round counter) and is filled in post-loop by zipping
// against agentloop.Result.Audit, which holds the round for each call.
// Sequence is the position within the round.
//
// AppliedTransactionID is filled in by the commit path: zero means
// "no fantasy-side row was produced" (rejected calls, update_notes,
// set_team_name). The helper translates zero → SQL NULL.
type ToolCallCapture struct {
	RoundIndex           int
	Sequence             int
	ToolName             string
	RecoveredName        string
	ArgumentsRaw         string
	Result               string
	Outcome              ToolCallOutcome
	FailureReason        string
	AppliedTransactionID int32
	Latency              time.Duration
}

// TurnHeader is the activity-level summary of one turn. The helper
// composes this with the captured round/tool/message data into
// sim_agent_turns + chained children.
type TurnHeader struct {
	PoolID      int32
	AgentID     int32
	SimDate     pgtype.Date // Valid=false for the team_name phase
	Phase       TurnPhase
	PickNumber  int32 // 0 for non-draft phases
	Status      TurnStatus
	SkipReason  string
	ErrorKind   ErrorKind
	ErrorDetail string

	Provider    string
	Model       string
	Temperature *float64
	MaxTokens   int

	CostUsd     float64
	FinalText   string
	StartedAt   time.Time
	CompletedAt time.Time
}

// roundCapture is one (response, duration) pair recorded by the
// latencyRecordingClient as it forwards a single Complete call. The
// activity reads the wrapper's captures after agentloop.Run returns to
// build per-round telemetry rows.
type roundCapture struct {
	Response *llm.Response
	Latency  time.Duration
}

// latencyRecordingClient is a per-turn llm.Client wrapper that records
// each Complete call's response and wall-clock duration. ONE instance
// per turn — the activity creates it on entry and reads its captures
// after agentloop.Run returns.
//
// Why a separate wrapper rather than extending instrumentedLLMClient:
// the metrics wrapper is per-agent and shared across goroutines (one
// pool may host concurrent activity invocations). Adding mutation to it
// would either need a mutex or risk cross-turn contamination. This
// wrapper is per-turn, single-goroutine, append-only — no locking, no
// contention.
type latencyRecordingClient struct {
	inner    llm.Client
	captures []roundCapture
}

func newLatencyRecordingClient(inner llm.Client) *latencyRecordingClient {
	return &latencyRecordingClient{inner: inner}
}

// Complete forwards to the wrapped client and captures the response +
// duration. Called sequentially by agentloop.Run on a single goroutine,
// so the append is safe without synchronization.
func (c *latencyRecordingClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	start := time.Now()
	resp, err := c.inner.Complete(ctx, req)
	c.captures = append(c.captures, roundCapture{Response: resp, Latency: time.Since(start)})
	return resp, err
}

// Captures returns the per-call captures recorded so far.
func (c *latencyRecordingClient) Captures() []roundCapture { return c.captures }

// RecoveredName returns the canonical tool name when RecoverAction's
// distance indicates a fuzzy match was applied, or empty when no
// recovery occurred or the canonical equals the raw name. Used by
// activities to populate ToolCallCapture.RecoveredName.
func RecoveredName(rawName string, action Action, distance int) string {
	if distance <= 0 || action == nil {
		return ""
	}
	canonical := action.ToolName()
	if canonical == rawName {
		return ""
	}
	return canonical
}

// AssignRoundsFromAudit fills RoundIndex and Sequence on each capture
// by zipping against agentloop.Result.Audit. agentloop.Run calls the
// executor in the same order it records Audit entries, so the i-th
// capture corresponds to the i-th audit entry. The fixup mutates the
// slice in place.
//
// When audit is shorter than captures (an executor error aborts the
// loop early in a future revision — V1 never returns executor errors,
// so this branch is defensive), the trailing captures keep their
// zero-value RoundIndex.
func AssignRoundsFromAudit(captures []ToolCallCapture, audit []agentloop.Audit) {
	roundSeq := map[int]int{}
	for i := 0; i < len(captures) && i < len(audit); i++ {
		round := audit[i].Round
		captures[i].RoundIndex = round
		captures[i].Sequence = roundSeq[round]
		roundSeq[round]++
	}
}

// TurnTelemetry bundles everything RecordTurnTelemetry persists for one
// turn. Replaces 8 positional parameters (the previous signature took
// Captures/ToolCalls/Messages as three separate positional slice
// arguments back-to-back, easy to miscount at a call site) with one
// named struct — field names make each call site self-documenting
// instead of relying on positional order.
type TurnTelemetry struct {
	Header TurnHeader

	// Captures is the per-round (response, latency) trail the
	// latencyRecordingClient recorded for this turn. Source of truth
	// for both token totals and cost — see aggregateUsage.
	Captures []roundCapture

	// ToolCalls is the per-tool-call audit trail (accepted, rejected,
	// or errored) the executor accumulated during the turn.
	ToolCalls []ToolCallCapture

	// Messages is the full conversation transcript, recorded only when
	// RecordMessages is true. Callers should leave this nil rather than
	// populate-then-gate — commitDailyTurn/persistTeamNameTurn compute
	// it up front from the same recordMessages flag.
	Messages []llm.Message

	// RecordMessages mirrors sim_pools.record_full_messages — gates
	// whether Messages is written as sim_agent_turn_messages rows.
	RecordMessages bool
}

// RecordTurnTelemetry writes the turn header, per-round usage rows,
// per-tool-call rows, and (when t.RecordMessages is true) the full
// message transcript. Returns the new sim_agent_turns.id.
//
// MUST be called inside the same Transactor.InTx block as the
// sim_transactions writes for the same turn — otherwise a crash between
// the two blocks leaves an idempotency-probe-failing inconsistency.
//
// Usage totals and cost are derived solely from t.Captures (see the
// body comment) — the caller's agentloop.Result is not part of this
// signature; only its Messages (already extracted into t.Messages by
// the caller) and Audit (already folded into t.ToolCalls via
// AssignRoundsFromAudit) are needed here.
//
// Defensive against:
//   - nil response inside a capture (provider error mid-call)
//   - len(t.Captures) != len(audit) that produced t.ToolCalls (see AssignRoundsFromAudit)
//   - empty ToolCalls / Messages slices (no-op for that child write)
func RecordTurnTelemetry(ctx context.Context, q SimQueries, t TurnTelemetry) (int32, error) {
	header := t.Header
	captures := t.Captures
	toolCalls := t.ToolCalls
	messages := t.Messages
	recordMessages := t.RecordMessages
	// Single source of truth: derive BOTH the token totals and the dollar
	// cost from the per-round captures. The parent turn row's token
	// columns are then guaranteed equal to the sum of its per-round child
	// rows (also built from captures below), and cost_usd is priced off
	// that same usage — no divergence between header.CostUsd (which the
	// caller computed independently) and the recorded tokens. header.CostUsd
	// is intentionally not used here; header.Provider/Model come from the
	// same AgentConfig the caller priced against, so this reproduces the
	// caller's cost while staying internally consistent.
	agg := aggregateUsage(captures)
	costNum, err := numericFromFloat(EstimateCost(header.Provider, header.Model, usageFromAggregate(agg)))
	if err != nil {
		return 0, fmt.Errorf("simulation: encode turn cost: %w", err)
	}

	// Idempotency: a Temporal-retried activity (PickTeamName after an
	// earlier tool_use_failure, DraftPick / ManageRoster after a
	// transient blip) replays this whole block. Delete any prior turn
	// at the same (pool, agent, phase, sim_date) coordinate so the
	// INSERT below doesn't collide with the partial unique indexes
	// ux_sim_agent_turns_no_date / _with_date. CASCADE removes the
	// prior turn's rounds / tool_calls / messages so child PKs don't
	// collide either. The DELETE+INSERT runs inside the caller's tx,
	// so either both writes land or neither does.
	if err := q.DeleteSimAgentTurnIdempotent(ctx, sqlcdb.DeleteSimAgentTurnIdempotentParams{
		PoolID:     header.PoolID,
		AgentID:    header.AgentID,
		Phase:      string(header.Phase),
		SimDate:    header.SimDate,
		PickNumber: header.PickNumber,
	}); err != nil {
		return 0, fmt.Errorf("simulation: delete prior turn: %w", err)
	}

	parent := sqlcdb.InsertSimAgentTurnParams{
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
		Rounds:              clampTokenCount(len(captures)),
		PromptTokens:        clampTokenCount(agg.PromptTokens),
		CompletionTokens:    clampTokenCount(agg.CompletionTokens),
		CacheCreationTokens: clampTokenCount(agg.CacheCreationInputTokens),
		CacheReadTokens:     clampTokenCount(agg.CacheReadInputTokens),
		CostUSD:             costNum,
		LatencyMs:           durationToMs(sumLatency(captures)),
		FinalText:           header.FinalText,
		StartedAt:           pgtype.Timestamptz{Time: header.StartedAt, Valid: true},
		CompletedAt:         pgtype.Timestamptz{Time: header.CompletedAt, Valid: true},
	}
	turnID, err := q.InsertSimAgentTurn(ctx, parent)
	if err != nil {
		return 0, fmt.Errorf("simulation: insert turn: %w", err)
	}

	if len(captures) > 0 {
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
			return 0, fmt.Errorf("simulation: insert turn rounds: %w", err)
		}
	}

	if len(toolCalls) > 0 {
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
			return 0, fmt.Errorf("simulation: insert tool calls: %w", err)
		}
	}

	if recordMessages && len(messages) > 0 {
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
			return 0, fmt.Errorf("simulation: insert turn messages: %w", err)
		}
	}

	return turnID, nil
}

// aggregateUsage sums the per-capture Usage values. The captures are the
// single source of truth for the turn's usage: the per-round child rows
// are built from these same captures, so summing them here keeps the
// parent turn row's token totals identical to the sum of its children.
//
// agentloop's own res.Usage is NOT used, even when available: it is
// accumulated from the same Complete responses the recorder captured, so
// it equals this sum in every non-error path — but trusting it would let
// the parent totals silently drift from the child rows if the two ever
// diverged (e.g. a future change to how partial results are summed).
// Captures with a nil Response (a provider error mid-call) contribute
// nothing.
func aggregateUsage(captures []roundCapture) agentloop.AggregateUsage {
	var u agentloop.AggregateUsage
	for _, c := range captures {
		if c.Response == nil || c.Response.Usage == nil {
			continue
		}
		u.PromptTokens += c.Response.Usage.PromptTokens
		u.CompletionTokens += c.Response.Usage.CompletionTokens
		u.TotalTokens += c.Response.Usage.TotalTokens
		u.CacheCreationInputTokens += c.Response.Usage.CacheCreationInputTokens
		u.CacheReadInputTokens += c.Response.Usage.CacheReadInputTokens
	}
	return u
}

// usageFromAggregate re-widens an agentloop.AggregateUsage into the
// llm.Usage shape the pricing table consumes, so cost can be priced off
// the same summed usage the token columns record.
func usageFromAggregate(agg agentloop.AggregateUsage) llm.Usage {
	return llm.Usage{
		PromptTokens:             agg.PromptTokens,
		CompletionTokens:         agg.CompletionTokens,
		TotalTokens:              agg.TotalTokens,
		CacheCreationInputTokens: agg.CacheCreationInputTokens,
		CacheReadInputTokens:     agg.CacheReadInputTokens,
	}
}

func sumLatency(captures []roundCapture) time.Duration {
	var d time.Duration
	for _, c := range captures {
		d += c.Latency
	}
	return d
}

func durationToMs(d time.Duration) int32 {
	if d <= 0 {
		return 0
	}
	return int32(d / time.Millisecond)
}

// textOrNull turns the empty string into a SQL NULL pgtype.Text. None
// of the columns this is used for legitimately stores the empty string,
// so the collapse is unambiguous.
func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// int4OrNull turns zero into a SQL NULL. The columns this serves
// (max_tokens, applied_transaction_id) never carry a legitimate zero
// value — 0-token cap / tx id 0 are both out-of-band. (pick_number is
// NOT NULL DEFAULT 0 and is written directly.)
func int4OrNull(v int32) pgtype.Int4 {
	if v == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}

// numericOrNull handles *float64 (the "unset is nil pointer"
// representation in AgentConfig.Temperature) → SQL NULL.
func numericOrNull(p *float64) pgtype.Numeric {
	if p == nil {
		return pgtype.Numeric{}
	}
	n, err := numericFromFloat(*p)
	if err != nil {
		return pgtype.Numeric{}
	}
	return n
}

// argumentsJSONB validates that raw is JSON-parseable before forwarding
// it as JSONB. Postgres rejects malformed JSONB at insert time; returning
// nil ([]byte → SQL NULL) lets the row land even when the LLM emits
// malformed arguments — the verbatim string is preserved in
// arguments_raw for forensic inspection.
func argumentsJSONB(raw string) []byte {
	if raw == "" {
		return nil
	}
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil
	}
	return []byte(raw)
}

// toolCallsJSONB marshals an assistant message's structured tool calls.
// Empty slice → SQL NULL so non-tool-call assistant messages leave the
// column unset.
func toolCallsJSONB(calls []llm.ToolCall) []byte {
	if len(calls) == 0 {
		return nil
	}
	b, err := json.Marshal(calls)
	if err != nil {
		return nil
	}
	return b
}

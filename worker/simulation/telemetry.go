package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/llm/agentloop"
	"github.com/sperano/puckdb/sqlcdb"
)

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

// RecordTurnTelemetry writes the turn header, per-round usage rows,
// per-tool-call rows, and (when recordMessages is true) the full
// message transcript. Returns the new sim_agent_turns.id.
//
// MUST be called inside the same Transactor.InTx block as the
// sim_transactions writes for the same turn — otherwise a crash between
// the two blocks leaves an idempotency-probe-failing inconsistency.
//
// Defensive against:
//   - nil res (agentloop.Run errored before producing a Result)
//   - nil response inside a capture (provider error mid-call)
//   - len(captures) != len(audit) (see AssignRoundsFromAudit)
//   - empty toolCalls / messages slices (no-op for that child write)
func RecordTurnTelemetry(
	ctx context.Context,
	q SimQueries,
	header TurnHeader,
	captures []roundCapture,
	res *agentloop.Result,
	toolCalls []ToolCallCapture,
	messages []llm.Message,
	recordMessages bool,
) (int32, error) {
	costNum, err := numericFromFloat(header.CostUsd)
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

	agg := aggregateUsage(captures, res)
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
		Rounds:              int32(len(captures)),
		PromptTokens:        int32(agg.PromptTokens),
		CompletionTokens:    int32(agg.CompletionTokens),
		CacheCreationTokens: int32(agg.CacheCreationInputTokens),
		CacheReadTokens:     int32(agg.CacheReadInputTokens),
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
				PromptTokens:        int32(u.PromptTokens),
				CompletionTokens:    int32(u.CompletionTokens),
				CacheCreationTokens: int32(u.CacheCreationInputTokens),
				CacheReadTokens:     int32(u.CacheReadInputTokens),
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

// aggregateUsage prefers agentloop's pre-summed AggregateUsage when
// present (the loop summed it cleanly), falling back to summing the
// per-capture Usage values for the "loop errored before producing a
// Result" path.
func aggregateUsage(captures []roundCapture, res *agentloop.Result) agentloop.AggregateUsage {
	if res != nil {
		return res.Usage
	}
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
// NOT NULL DEFAULT 0 since migration 000024 and is written directly.)
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

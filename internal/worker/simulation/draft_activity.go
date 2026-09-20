package simulation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// DraftPickActivity — single draft pick for one agent.
//
// Lifecycle for a normal pick:
//
//	1. Idempotency probe: GetSimTransactionDraftPick on (pool, agent,
//	   round, pick). Hit ⇒ return Skipped result; the previous attempt
//	   already landed and a Temporal retry must NOT re-invoke the LLM.
//	2. Cost-cap probe: GetSimPool → compare TotalLLMCostUsd with the
//	   pool's max_llm_cost_usd_per_pool. Tripped ⇒ insert
//	   cost_cap_reached row, set status=paused, signal workflow with
//	   "pause", return Skipped result. (Order matters; see the
//	   runCostCapBranch comment for retry semantics.)
//	3. LLM tool call: agent.Client.Complete with the draft prompt and
//	   the draft_player tool. Single-shot — the agent's job is to
//	   choose ONE player; multi-round agentloop is overkill here.
//	4. Recover + validate: feed the response through RecoverAction
//	   (handles fuzzy tool names + lenient JSON), then assert the
//	   chosen player is in AvailableIDs. Failure ⇒ second LLM attempt
//	   with the same prompt.
//	5. Fallback: two failures ⇒ FallbackDraftPick (deterministic).
//	6. Atomic commit (Transactor.InTx):
//	     - InsertSimRoster (slot=BN, acquired_via=draft)
//	     - InsertSimTransactionDraftPick
//	     - IncrementSimPoolLLMCost
//	   All three or none — otherwise the next attempt's idempotency
//	   probe could see a sim_transactions row without the corresponding
//	   sim_rosters row (roster reads would lie).
//
// PLAN.md > "Idempotency > LLM activities" and PLAN.md > "Cost cap >
// Enforcement" are the canonical specs.
// ============================================================================

// DraftPickInput is the per-pick payload assembled by the workflow.
//
// PoolConfig + AgentConfig are static for the pool's lifetime — the
// workflow loads them once at start (or after a ContinueAsNew) and
// passes the same value into every pick. They live on the input
// rather than as Activities-struct fields because the activity worker
// may host multiple pools simultaneously.
//
// AvailableIDs / RankedSkaters / RankedGoalies / Taken come from the
// workflow's draft-state machine. The activity does NOT re-query
// them — picks are serialized in the workflow goroutine, so what the
// workflow knows about availability IS the truth at this pick.
type DraftPickInput struct {
	PoolID     int32       `json:"pool_id"`
	AgentID    int32       `json:"agent_id"`
	Round      int32       `json:"round"`
	Pick       int32       `json:"pick"`
	SimDate    pgtype.Date `json:"sim_date"`
	WorkflowID string      `json:"workflow_id"`

	PoolConfig  PoolConfig  `json:"pool_config"`
	AgentConfig AgentConfig `json:"agent_config"`

	DraftPrompt  DraftPromptInput `json:"draft_prompt"`
	AvailableIDs []int64          `json:"available_ids"`

	// Fallback inputs — consulted only when both LLM attempts fail.
	Roster          RosterState                     `json:"roster"`
	RosterPositions map[int64]sqlcdb.PlayerPosition `json:"roster_positions"`
	RankedSkaters   []SkaterDraftCandidate          `json:"ranked_skaters"`
	RankedGoalies   []GoalieDraftCandidate          `json:"ranked_goalies"`
	Taken           []int64                         `json:"taken"`
}

// DraftPickResult is what the activity returns.
//
// Skipped + SkipReason cover the two no-op paths (idempotency hit,
// cost-cap trip). PlayerID is 0 when Skipped. CostUsd is the dollar
// cost of the LLM call(s) for this pick — the workflow doesn't use
// this directly, but it surfaces in metrics and per-pool cost
// dashboards (Phase 3.3).
type DraftPickResult struct {
	Skipped      bool    `json:"skipped"`
	SkipReason   string  `json:"skip_reason,omitempty"`
	PlayerID     int64   `json:"player_id"`
	UsedFallback bool    `json:"used_fallback"`
	CostUsd      float64 `json:"cost_usd"`
	// Notes carries the agent's latest sim_agents.notes value if the
	// agent emitted any update_notes tool calls during this turn.
	// NotesUpdated discriminates "no change" (false) from "set to ''"
	// (true with Notes=""), since both look the same at the field
	// level but mean different things to the workflow.
	Notes        string `json:"notes,omitempty"`
	NotesUpdated bool   `json:"notes_updated,omitempty"`
}

// SkipReason values for DraftPickResult.SkipReason. Keeping these as
// constants rather than free-form strings so a future "branch on the
// reason" caller (e.g. workflow telemetry) doesn't typo-mismatch.
const (
	SkipReasonAlreadyDrafted = "already_drafted"
	SkipReasonCostCapReached = "cost_cap_reached"
)

// DraftPick is the Temporal-activity entry point.
//
// Always returns nil error on the no-op paths (idempotency hit,
// cost-cap trip) — those are SUCCESSFUL completions of the activity
// from Temporal's perspective. The workflow inspects Result.Skipped
// to decide whether to advance to the next pick.
//
// Returns an error only for unrecoverable failures:
//   - DB read/write error in the pre-flight or commit
//   - The cost-cap signal-back failed (ensures retry, which then
//     observes the cost-cap branch idempotently)
//   - The fallback also returned 0 (truly no draftable player exists)
func (a *Activities) DraftPick(ctx context.Context, in DraftPickInput) (DraftPickResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("DraftPick start",
		"pool_id", in.PoolID,
		"agent_id", in.AgentID,
		"round", in.Round,
		"pick", in.Pick,
	)

	if hit, prev, err := a.idempotencyHit(ctx, in); err != nil {
		return DraftPickResult{}, err
	} else if hit {
		logger.Debug("DraftPick idempotency hit — skipping LLM",
			"existing_player_id", prev.PlayerID.Int64,
		)
		return DraftPickResult{
			Skipped:    true,
			SkipReason: SkipReasonAlreadyDrafted,
			PlayerID:   prev.PlayerID.Int64,
		}, nil
	}

	tripped, currentCostUsd, capUsd, err := a.checkCostCap(ctx, in.PoolID, in.PoolConfig.MaxLLMCostUsdPerPool)
	if err != nil {
		return DraftPickResult{}, err
	}
	if tripped {
		if err := a.runCostCapBranch(ctx, in.PoolID, in.AgentID, in.SimDate, in.WorkflowID, currentCostUsd, capUsd); err != nil {
			return DraftPickResult{}, err
		}
		return DraftPickResult{Skipped: true, SkipReason: SkipReasonCostCapReached}, nil
	}

	// Pre-flight the only read the commit path needs, so a DB hiccup
	// can't fail this activity after the model has been paid.
	recordMessages, err := a.fetchRecordMessagesFlag(ctx, in.PoolID)
	if err != nil {
		return DraftPickResult{}, err
	}

	pick, err := a.chooseDraftPlayer(ctx, in)
	if err != nil {
		return DraftPickResult{}, err
	}

	if err := a.commitDraftPick(ctx, in, pick, recordMessages); err != nil {
		return DraftPickResult{}, err
	}

	logger.Debug("DraftPick complete",
		"player_id", pick.playerID,
		"used_fallback", pick.usedFallback,
		"cost_usd", pick.costUsd,
	)
	return DraftPickResult{
		PlayerID:     pick.playerID,
		UsedFallback: pick.usedFallback,
		CostUsd:      pick.costUsd,
		Notes:        pick.notes,
		NotesUpdated: pick.notesUpdated,
	}, nil
}

// idempotencyHit returns true when sim_transactions already has a
// draft_pick row for this (pool, agent, round, pick). Used as the
// pre-flight check on every DraftPick invocation so a Temporal retry
// after a worker crash mid-commit doesn't re-bill the LLM.
//
// pgx.ErrNoRows is the expected "no prior pick" path and returns
// (false, _, nil); any other error aborts the activity so the caller
// can retry rather than silently re-invoking the model.
func (a *Activities) idempotencyHit(ctx context.Context, in DraftPickInput) (bool, sqlcdb.SimTransaction, error) {
	prev, err := a.Queries.GetSimTransactionDraftPick(ctx, sqlcdb.GetSimTransactionDraftPickParams{
		PoolID:  in.PoolID,
		AgentID: in.AgentID,
		Round:   pgtype.Int4{Int32: in.Round, Valid: true},
		Pick:    pgtype.Int4{Int32: in.Pick, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, sqlcdb.SimTransaction{}, nil
	}
	if err != nil {
		return false, sqlcdb.SimTransaction{}, fmt.Errorf("simulation: idempotency probe: %w", err)
	}
	return true, prev, nil
}

// chooseResult bundles what chooseDraftPlayer hands back: the chosen
// player_id, the LLM's reasoning text (empty for the fallback path),
// whether the fallback fired, total LLM cost (the fallback path counts
// failed-attempt cost — those calls billed the operator), and the turn
// telemetry the activity will persist alongside the draft_pick row.
//
// acceptedToolCaptureIndex points into toolCaptures at the entry whose
// applied_transaction_id should be backfilled at commit time. -1 means
// no LLM call produced the pick (fallback or LLM-side total failure).
type chooseResult struct {
	playerID     int64
	reasoning    string
	usedFallback bool
	costUsd      float64

	// notes carries the latest sim_agents.notes value if the agent
	// emitted update_notes during the turn. notesUpdated discriminates
	// "no change" from "explicitly set to empty"; only when true does
	// the commit path persist the value.
	notes        string
	notesUpdated bool

	// Telemetry — populated by chooseDraftPlayer across all attempts.
	// res is the raw agentloop result (nil if the loop never returned
	// one); it carries the full transcript that commitDraftPick records
	// when the pool's record_full_messages flag is on.
	res                      *agentloop.Result
	captures                 []roundCapture
	toolCaptures             []ToolCallCapture
	acceptedToolCaptureIndex int
	startedAt                time.Time
	completedAt              time.Time
	errored                  bool
	errorKind                ErrorKind
	errorDetail              string
}

// MaxDraftToolRounds caps the agentloop rounds for one draft turn.
// Enough for: update_notes → draft_player happy path, plus a few
// retries if the first draft_player attempt picks an invalid ID and
// the executor returns an error the LLM can read in the next round.
const MaxDraftToolRounds = 4

// chooseDraftPlayer drives one draft turn via agentloop.Run. The
// executor handles two tools:
//
//   - update_notes — captures the new notes value in the chooseResult
//     (persisted via commitDraftPick's tx, not in the executor itself).
//     Allowed multiple times per turn; later calls overwrite earlier.
//   - draft_player — captures the chosen player_id + reasoning. The
//     executor signals "drafted" via the response text so the LLM
//     stops calling tools; if the agent emits another draft_player
//     after a successful one, the second is rejected as a duplicate.
//
// Falls back to the workflow-supplied ranked candidates when no
// successful draft_player landed within MaxDraftToolRounds rounds.
func (a *Activities) chooseDraftPlayer(ctx context.Context, in DraftPickInput) (chooseResult, error) {
	agent, err := a.getOrCreateAgent(in.PoolID, in.AgentID, in.AgentConfig, in.PoolConfig.NumTeams)
	if err != nil {
		return chooseResult{}, fmt.Errorf("simulation: build agent: %w", err)
	}

	available := setOf(in.AvailableIDs)
	msgs, err := agent.DraftMessages(in.DraftPrompt)
	if err != nil {
		return chooseResult{}, fmt.Errorf("simulation: build draft messages: %w", err)
	}

	recorder := newLatencyRecordingClient(agent.Client)
	result := chooseResult{
		acceptedToolCaptureIndex: -1,
		startedAt:                time.Now(),
	}

	exec := func(_ context.Context, call llm.ToolCall) (string, error) {
		callStart := time.Now()
		capture := ToolCallCapture{
			ToolName:     call.Function.Name,
			ArgumentsRaw: call.Function.Arguments,
		}
		defer func() {
			capture.Latency = time.Since(callStart)
			result.toolCaptures = append(result.toolCaptures, capture)
		}()

		action, dist, recErr := RecoverAction(call, agent.DraftTools())
		if recErr != nil {
			metrics.IncSimLLMFailure(in.AgentConfig.Provider, in.AgentConfig.Model, metrics.SimFailureParseError)
			capture.Outcome = ToolCallOutcomeParseError
			capture.FailureReason = recErr.Error()
			capture.Result = fmt.Sprintf("error: %v", recErr)
			return capture.Result, nil
		}
		capture.RecoveredName = RecoveredName(call.Function.Name, action, dist)

		switch a := action.(type) {
		case UpdateNotesArgs:
			result.notes = a.Notes
			result.notesUpdated = true
			capture.Outcome = ToolCallOutcomeAccepted
			capture.Result = "ok: notes updated"
			return capture.Result, nil

		case DraftPlayerArgs:
			if result.playerID != 0 {
				// Already drafted earlier in this turn — reject the
				// duplicate so the LLM doesn't try to "change its mind"
				// and clobber the accepted pick.
				capture.Outcome = ToolCallOutcomeUnhandled
				capture.FailureReason = "draft_player already called this turn"
				capture.Result = "error: you already drafted; stop calling tools"
				return capture.Result, nil
			}
			if _, ok := available[a.PlayerID]; !ok {
				metrics.IncSimLLMFailure(in.AgentConfig.Provider, in.AgentConfig.Model, metrics.SimFailureValidation)
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = fmt.Sprintf("LLM picked player %d which is not available", a.PlayerID)
				capture.Result = fmt.Sprintf("error: %s. Pick one of the player_ids from best_available_overall or available_by_position.", capture.FailureReason)
				return capture.Result, nil
			}
			result.playerID = a.PlayerID
			result.reasoning = a.Reason
			result.acceptedToolCaptureIndex = len(result.toolCaptures) // before the defer appends
			capture.Outcome = ToolCallOutcomeAccepted
			capture.Result = "ok: player drafted"
			return capture.Result, nil

		default:
			metrics.IncSimLLMFailure(in.AgentConfig.Provider, in.AgentConfig.Model, metrics.SimFailureToolUseFailure)
			capture.Outcome = ToolCallOutcomeUnhandled
			capture.FailureReason = fmt.Sprintf("LLM called %q, expected draft_player or update_notes", action.ToolName())
			capture.Result = fmt.Sprintf("error: %s", capture.FailureReason)
			return capture.Result, nil
		}
	}

	res, runErr := agentloop.Run(ctx, recorder, msgs, exec, agentloop.Config{
		Tools:         agent.DraftTools(),
		MaxToolRounds: MaxDraftToolRounds,
		MaxTokens:     in.AgentConfig.MaxTokens,
		Temperature:   in.AgentConfig.Temperature,
	})
	result.res = res
	result.captures = recorder.Captures()
	result.completedAt = time.Now()
	result.costUsd = costFromAggregate(in.AgentConfig, res)
	// Fill RoundIndex / Sequence on each capture from agentloop's audit
	// trail. Without this, every capture lands at (turn_id, 0, 0) and
	// the sim_agent_tool_calls PK fires on the second tool call of the
	// same turn. Same fixup PickTeamName + ManageRoster do post-Run.
	if res != nil {
		AssignRoundsFromAudit(result.toolCaptures, res.Audit)
	}
	if runErr != nil {
		// Provider-level failure (network, 5xx, etc.). Fall through to
		// fallback so the draft makes progress.
		activity.GetLogger(ctx).Warn("DraftPick agentloop failed", "err", runErr.Error())
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}

	if result.playerID != 0 {
		return result, nil
	}

	// No accepted draft_player landed → fallback. Uses workflow-supplied
	// ranked candidates rather than the LLM. Failed attempts already
	// billed and were captured as tool_calls.
	fallbackID, err := FallbackDraftPick(
		in.Roster,
		mapPositionCatalog(in.RosterPositions),
		in.RankedSkaters,
		in.RankedGoalies,
		setOf(in.Taken),
	)
	if err != nil {
		result.errored = true
		result.errorKind = ErrorKindToolUseFailure
		result.errorDetail = err.Error()
		return result, fmt.Errorf("simulation: fallback draft pick: %w", err)
	}
	if fallbackID == 0 {
		result.errored = true
		result.errorKind = ErrorKindToolUseFailure
		result.errorDetail = "fallback returned 0 — no draftable player available"
		return result, errors.New("simulation: fallback returned 0 — no draftable player available")
	}
	result.playerID = fallbackID
	result.usedFallback = true
	// Synthetic capture so the fallback pick lands in sim_agent_tool_calls
	// with the picked player_id in its arguments. tool_name = "<fallback>"
	// distinguishes this from LLM-issued calls in dashboards/SQL filters.
	result.toolCaptures = append(result.toolCaptures, ToolCallCapture{
		RoundIndex:   MaxDraftToolRounds, // one past the last LLM round
		Sequence:     0,
		ToolName:     "<fallback>",
		ArgumentsRaw: fmt.Sprintf(`{"player_id":%d}`, fallbackID),
		Result:       "ok: player drafted via fallback",
		Outcome:      ToolCallOutcomeAccepted,
	})
	result.acceptedToolCaptureIndex = len(result.toolCaptures) - 1
	return result, nil
}

// commitDraftPick is the atomic-write step. Three writes in one tx:
// roster row, draft_pick transaction row, cost increment. Slot is BN
// at draft time (PLAN.md > "Slot rules" — drafted players land on
// the bench; the agent sets lineup separately on day 1).
//
// Additionally writes the full turn telemetry (sim_agent_turns +
// children) so the draft attempt is fully auditable: rejected LLM
// picks, fallback firings, per-round token usage and — when
// recordMessages is on — the full transcript all persist in the same
// atomic-commit block as the fantasy-side draft_pick row.
//
// recordMessages is a parameter rather than a read here: DraftPick
// fetches it before the LLM runs (see fetchRecordMessagesFlag).
func (a *Activities) commitDraftPick(ctx context.Context, in DraftPickInput, pick chooseResult, recordMessages bool) error {
	costNum, err := numericFromFloat(pick.costUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode pick cost: %w", err)
	}

	messages := transcriptForRecording(recordMessages, pick.res)

	return a.Tx.InTx(ctx, func(q SimQueries) error {
		if err := q.InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
			PoolID:      in.PoolID,
			AgentID:     in.AgentID,
			PlayerID:    pick.playerID,
			Slot:        string(SlotBN),
			AcquiredAt:  in.SimDate,
			AcquiredVia: string(AcquiredViaDraft),
		}); err != nil {
			return fmt.Errorf("insert roster: %w", err)
		}
		tx, err := q.InsertSimTransactionDraftPick(ctx, sqlcdb.InsertSimTransactionDraftPickParams{
			PoolID:    in.PoolID,
			AgentID:   in.AgentID,
			Date:      in.SimDate,
			PlayerID:  pgtype.Int8{Int64: pick.playerID, Valid: true},
			Reasoning: pick.reasoning,
			Round:     pgtype.Int4{Int32: in.Round, Valid: true},
			Pick:      pgtype.Int4{Int32: in.Pick, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("insert draft_pick tx: %w", err)
		}

		// Backfill applied_transaction_id on the accepted LLM attempt's
		// tool-call capture. Fallback picks leave every capture's
		// applied_transaction_id at zero — the draft_pick row exists
		// but didn't come from a tool call.
		if pick.acceptedToolCaptureIndex >= 0 && pick.acceptedToolCaptureIndex < len(pick.toolCaptures) {
			pick.toolCaptures[pick.acceptedToolCaptureIndex].AppliedTransactionID = tx.ID
		}

		if _, err := q.IncrementSimPoolLLMCost(ctx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              in.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}

		// Persist agent notes if the agent wrote them via update_notes
		// during this turn. Same tx as the draft row so a crash between
		// the two writes can't leave notes referring to a pick that
		// didn't happen.
		if pick.notesUpdated {
			if err := q.UpdateSimAgentNotes(ctx, sqlcdb.UpdateSimAgentNotesParams{
				ID:    in.AgentID,
				Notes: pick.notes,
			}); err != nil {
				return fmt.Errorf("update agent notes: %w", err)
			}
		}

		header := draftTurnHeader(in, pick)
		if _, err := RecordTurnTelemetry(ctx, q, TurnTelemetry{
			Header:         header,
			Captures:       pick.captures,
			ToolCalls:      pick.toolCaptures,
			Messages:       messages,
			RecordMessages: recordMessages,
		}); err != nil {
			return err
		}
		return nil
	})
}

// draftTurnHeader builds the TurnHeader for a draft pick. Status reflects
// the fallback path: errored when the fallback itself failed, ok
// otherwise (including the "LLM failed twice but fallback succeeded"
// path — the activity completed successfully even though the LLM didn't
// drive the pick).
func draftTurnHeader(in DraftPickInput, pick chooseResult) TurnHeader {
	status := TurnStatusOK
	if pick.errored {
		status = TurnStatusErrored
	}
	return TurnHeader{
		PoolID:      in.PoolID,
		AgentID:     in.AgentID,
		SimDate:     in.SimDate,
		Phase:       TurnPhaseDraft,
		PickNumber:  (in.Round-1)*int32(in.PoolConfig.NumTeams) + in.Pick,
		Status:      status,
		ErrorKind:   pick.errorKind,
		ErrorDetail: pick.errorDetail,
		Provider:    in.AgentConfig.Provider,
		Model:       in.AgentConfig.Model,
		Temperature: in.AgentConfig.Temperature,
		MaxTokens:   in.AgentConfig.MaxTokens,
		CostUsd:     pick.costUsd,
		FinalText:   pick.reasoning,
		StartedAt:   pick.startedAt,
		CompletedAt: pick.completedAt,
	}
}

// setOf turns an []int64 into a presence-set. Used to convert the
// AvailableIDs / Taken slices on the input into the lookup form
// the validation and fallback paths want.
func setOf(ids []int64) map[int64]struct{} {
	out := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

// mapPositionCatalog wraps a player-id-to-position map in the
// PlayerCatalog interface FallbackDraftPick wants. The caller
// guarantees coverage for every player in roster.Placements; a miss
// surfaces as a clean error (not a panic).
type mapPositionCatalog map[int64]sqlcdb.PlayerPosition

func (m mapPositionCatalog) Position(playerID int64) (sqlcdb.PlayerPosition, error) {
	pos, ok := m[playerID]
	if !ok {
		return "", fmt.Errorf("no position for rostered player %d", playerID)
	}
	return pos, nil
}

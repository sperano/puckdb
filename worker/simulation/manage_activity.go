package simulation

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/llm/agentloop"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// ManageRosterActivity — one fantasy manager's daily turn.
//
// Lifecycle:
//
//	1. Idempotency probe: ExistsSimDailyTurnMarker on (pool, agent,
//	   sim_date) — matches only the dedicated 'daily_turn_done' marker
//	   row, so unrelated rows (draft_pick, waiver add/drop,
//	   cost_cap_reached) at the same coordinate don't trip it. Hit ⇒
//	   Skipped result (Temporal retry must NOT re-bill the LLM).
//	2. Cost-cap probe: shared with DraftPickActivity. Tripped ⇒ insert
//	   cost_cap_reached row + status=paused + signal workflow with
//	   "pause"; return Skipped.
//	3. agentloop.Run: multi-round LLM tool-use conversation. Tool
//	   executor validates each call against a working copy of the
//	   roster + FA pool, accumulates accepted actions in memory, and
//	   feeds string-formatted errors back to the LLM so it can
//	   correct itself on the next round.
//	4. Apply actions atomically (Transactor.InTx): for each accepted
//	   action, dispatch to the matching sqlc write set, then increment
//	   the per-pool LLM cost. The LLM call is NOT held inside the tx
//	   (would starve the pool for ~60s).
//
// Failure shape (per PLAN.md > "Always returns success"):
//
//   - LLM provider error / agentloop hard failure → log a
//     `pass`-with-error-detail style row; activity returns nil.
//
//   - Zero accepted actions (no tool calls, or all rejected by
//     validation) → log a `pass` row.
//
//   - Validation failures during the loop → returned to the LLM as
//     tool-result strings; the model can adjust on the next round.
//     Only persistent rejection over all rounds becomes "no action".
//
// The activity ALWAYS writes a dedicated `daily_turn_done` marker row
// for (pool, agent, sim_date) at the end of commitDailyTurn so the
// next attempt's idempotency probe fires correctly — regardless of
// whether the turn produced actions, a pass, or an error. The marker
// is decoupled from the audit-log rows (draft_pick, add/drop, pass,
// error, cost_cap_reached) so none of those can spuriously mark a day
// as already-managed.
// ============================================================================

// ManageRosterInput is the per-day, per-agent payload assembled by
// the workflow. Like DraftPickInput, the static-per-pool fields
// (PoolConfig, AgentConfig) are workflow-loaded once; the per-day
// fields are workflow-prepared.
//
// Roster + FreeAgents + OnWaivers + Positions describe the working
// state at TURN START. The activity copies them before running the
// agent loop so each tool call validates against a state that
// reflects prior accepted actions in the same turn.
type ManageRosterInput struct {
	PoolID     int32       `json:"pool_id"`
	AgentID    int32       `json:"agent_id"`
	SimDate    pgtype.Date `json:"sim_date"`
	WorkflowID string      `json:"workflow_id"`

	PoolConfig  PoolConfig  `json:"pool_config"`
	AgentConfig AgentConfig `json:"agent_config"`

	DailyPrompt DailyPromptInput `json:"daily_prompt"`

	// Working state at the start of this turn.
	Roster     RosterState                     `json:"roster"`
	FreeAgents []int64                         `json:"free_agents"`
	OnWaivers  []int64                         `json:"on_waivers"`
	Positions  map[int64]sqlcdb.PlayerPosition `json:"positions"`

	// PendingClaims are player_ids the agent already has a pending waiver
	// claim on. A second claim on the same player violates
	// ux_sim_waiver_claims_pending_one_per_agent_player; ValidateClaimPlayer
	// rejects it as a clean action error so it never reaches the DB.
	PendingClaims []int64 `json:"pending_claims"`
}

// ManageRosterResult summarizes what the activity did. The workflow
// uses ActionsApplied as a coarse "did the agent do something" signal
// for telemetry; CostUsd feeds the per-pool spend dashboard.
type ManageRosterResult struct {
	Skipped        bool    `json:"skipped"`
	SkipReason     string  `json:"skip_reason,omitempty"`
	ActionsApplied int     `json:"actions_applied"`
	Passed         bool    `json:"passed"`
	Errored        bool    `json:"errored"`
	CostUsd        float64 `json:"cost_usd"`
}

// MaxDailyToolRounds is the agentloop iteration cap for the daily
// turn. The realistic agent flow is at most:
//
//	round 1: drop / add / claim (multiple tools in parallel)
//	round 2: set_lineup
//	round 3: update_notes
//	round 4: final text reply (loop terminates naturally)
//
// 5 leaves headroom; agentloop's default of 10 is over-budget for
// what's a constrained tool repertoire.
const MaxDailyToolRounds = 5

// SkipReasonAlreadyManaged is the SkipReason value when the
// idempotency probe finds an existing transaction row for the day.
const SkipReasonAlreadyManaged = "already_managed"

// ManageRoster is the Temporal-activity entry point. Always returns
// nil error on the success paths (including the "agent failed but we
// logged it" path); only DB / signal / build-agent failures abort.
func (a *Activities) ManageRoster(ctx context.Context, in ManageRosterInput) (ManageRosterResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("ManageRoster start",
		"pool_id", in.PoolID,
		"agent_id", in.AgentID,
		"sim_date", in.SimDate.Time,
	)

	exists, err := a.Queries.ExistsSimDailyTurnMarker(ctx, sqlcdb.ExistsSimDailyTurnMarkerParams{
		PoolID:  in.PoolID,
		AgentID: in.AgentID,
		Date:    in.SimDate,
	})
	if err != nil {
		return ManageRosterResult{}, fmt.Errorf("simulation: idempotency probe: %w", err)
	}
	if exists {
		logger.Debug("ManageRoster idempotency hit — skipping LLM")
		return ManageRosterResult{Skipped: true, SkipReason: SkipReasonAlreadyManaged}, nil
	}

	tripped, currentCostUsd, capUsd, err := a.checkCostCap(ctx, in.PoolID, in.PoolConfig.MaxLLMCostUsdPerPool)
	if err != nil {
		return ManageRosterResult{}, err
	}
	if tripped {
		if err := a.runCostCapBranch(ctx, in.PoolID, in.AgentID, in.SimDate, in.WorkflowID, currentCostUsd, capUsd); err != nil {
			return ManageRosterResult{}, err
		}
		return ManageRosterResult{Skipped: true, SkipReason: SkipReasonCostCapReached}, nil
	}

	// Read record_full_messages up-front, alongside the idempotency and
	// cost-cap probes, so every avoidable failure point sits BEFORE the
	// LLM runs (see fetchRecordMessagesFlag for why).
	recordMessages, err := a.fetchRecordMessagesFlag(ctx, in.PoolID)
	if err != nil {
		return ManageRosterResult{}, err
	}

	agent, err := a.getOrCreateAgent(in.PoolID, in.AgentID, in.AgentConfig, in.PoolConfig.NumTeams)
	if err != nil {
		return ManageRosterResult{}, fmt.Errorf("simulation: build agent: %w", err)
	}

	outcome := a.runDailyAgent(ctx, agent, in)

	if err := a.commitDailyTurn(ctx, in, outcome, recordMessages); err != nil {
		return ManageRosterResult{}, err
	}

	logger.Debug("ManageRoster complete",
		"actions_applied", len(outcome.actions),
		"passed", outcome.passed,
		"errored", outcome.errored,
		"cost_usd", outcome.costUsd,
	)
	return ManageRosterResult{
		ActionsApplied: len(outcome.actions),
		Passed:         outcome.passed,
		Errored:        outcome.errored,
		CostUsd:        outcome.costUsd,
	}, nil
}

// dailyOutcome is the internal collection point for everything the
// agent's turn produced. commitDailyTurn consumes this to emit the
// right set of DB writes.
//
// At MOST one of {len(actions) > 0, passed, errored} is true:
//   - Actions present → commit each with its own tx row.
//   - Otherwise → either pass or error (mutually exclusive).
//
// reasoning is the agent's final summary text (Result.Final.Content);
// applied to every action's reasoning column AND to the pass/error
// row when those fire.
type dailyOutcome struct {
	actions     []recordedAction
	notes       *string // pointer so "no update" is distinct from "set to empty"
	reasoning   string
	costUsd     float64
	passed      bool
	errored     bool
	errorKind   ErrorKind
	errorDetail string

	// Turn telemetry — populated by runDailyAgent so commitDailyTurn can
	// write the sim_agent_turns + children rows alongside the per-action
	// sim_transactions rows in the same atomic-commit block.
	captures     []roundCapture    // per-Complete-call capture (response + latency)
	toolCaptures []ToolCallCapture // per tool call (accepted + rejected)
	res          *agentloop.Result // raw agentloop result; nil if loop didn't return
	startedAt    time.Time         // wall clock just before agentloop.Run
	completedAt  time.Time         // wall clock just after agentloop.Run
}

// recordedAction is one accepted tool call. Stored during the
// executor loop and replayed against the DB at commit time.
//
// args is one of {AddPlayerArgs, ClaimPlayerArgs, DropPlayerArgs,
// SetLineupArgs} — update_notes lives in dailyOutcome.notes since it
// has no transaction row of its own.
//
// resolvedLineup is non-nil only when args is a SetLineupArgs;
// captures the post-validation move list (with displacements) so the
// commit path can write sim_lineup_moves children without re-running
// validation.
//
// toolCaptureIndex points back into dailyOutcome.toolCaptures so the
// commit path can backfill applied_transaction_id once the action's
// sim_transactions row lands. -1 indicates "no telemetry capture" (used
// by tests that bypass the executor).
type recordedAction struct {
	args             Action
	resolvedLineup   []ResolvedLineupMove
	toolCaptureIndex int
}

// runDailyAgent executes the agentloop with a tool executor that
// validates calls against a working state copy. Returns the outcome
// (actions, notes, reasoning, cost, telemetry captures, error markers);
// never returns an error itself — agent-side failures land in
// dailyOutcome.errored.
func (a *Activities) runDailyAgent(ctx context.Context, agent *Agent, in ManageRosterInput) dailyOutcome {
	logger := activity.GetLogger(ctx)
	work := newDailyWorkingState(in)

	var outcome dailyOutcome
	exec := a.makeDailyToolExecutor(work, &outcome, agent.DailyTools(), agent.Config)

	msgs, err := agent.DailyMessages(in.DailyPrompt)
	if err != nil {
		logger.Error("ManageRoster build daily messages failed", "err", err)
		outcome.errored = true
		outcome.errorKind = ErrorKindLLMError
		outcome.errorDetail = fmt.Sprintf("build daily messages: %v", err)
		outcome.startedAt = time.Now()
		outcome.completedAt = outcome.startedAt
		return outcome
	}

	recorder := newLatencyRecordingClient(agent.Client)
	outcome.startedAt = time.Now()
	res, err := agentloop.Run(ctx, recorder, msgs, exec, agentloop.Config{
		Tools:         agent.DailyTools(),
		MaxToolRounds: MaxDailyToolRounds,
		MaxTokens:     in.AgentConfig.MaxTokens,
		Temperature:   in.AgentConfig.Temperature,
	})
	outcome.completedAt = time.Now()
	outcome.captures = recorder.Captures()
	outcome.res = res
	if res != nil {
		AssignRoundsFromAudit(outcome.toolCaptures, res.Audit)
	}

	if err != nil {
		logger.Warn("ManageRoster agent loop failed",
			"err", err,
		)
		// agentloop wraps the underlying error; classify into
		// llm_error vs tool_use_failure based on whether the
		// failure originated in the tool executor (we never return
		// errors from the executor in V1, so this branch is
		// effectively always llm_error — but keep the classifier
		// honest for a future "executor returns abort error" path).
		outcome.errored = true
		outcome.errorKind = ErrorKindLLMError
		outcome.errorDetail = err.Error()
		// Even on failure, partial usage may have accumulated; record it.
		outcome.costUsd = costFromAggregate(in.AgentConfig, res)
		return outcome
	}

	outcome.costUsd = costFromAggregate(in.AgentConfig, res)
	if res.Final != nil {
		outcome.reasoning = res.Final.Content
	}

	// "Passed" tracks whether the commit will write a pass marker row,
	// which happens whenever no actions were applied (notes-only turns
	// included — notes have no transaction row of their own, so the
	// pass row carries the day-completion marker for the idempotency
	// probe).
	if len(outcome.actions) == 0 && !outcome.errored {
		outcome.passed = true
	}
	return outcome
}

// costFromAggregate composes the agentloop aggregate usage into a
// dollar cost via the pricing table. Defensive against a nil Result
// (only happens when agentloop's first Complete call errored before
// any usage was reported).
func costFromAggregate(cfg AgentConfig, res *agentloop.Result) float64 {
	if res == nil {
		return 0
	}
	usage := llm.Usage{
		PromptTokens:             res.Usage.PromptTokens,
		CompletionTokens:         res.Usage.CompletionTokens,
		TotalTokens:              res.Usage.TotalTokens,
		CacheCreationInputTokens: res.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     res.Usage.CacheReadInputTokens,
	}
	return EstimateCost(cfg.Provider, cfg.Model, usage)
}

// dailyWorkingState is the mutable copy of (roster, FA, on-waivers)
// that the tool executor maintains across rounds. Each accepted
// action mutates it so subsequent calls see the post-prior-actions
// view: drop_player(A) followed by add_player(B) sees a roster with
// A removed when validating B's drop slot, etc.
type dailyWorkingState struct {
	roster    RosterState
	fa        PoolFreeAgentState
	positions mapPositionCatalog
	// pendingClaims tracks players the agent already has an open claim on
	// (turn-start state plus any claim filed earlier this same turn), so a
	// duplicate claim is rejected before it hits the unique index.
	pendingClaims map[int64]struct{}
}

// newDailyWorkingState clones the input maps so the activity input
// stays untouched (callers shouldn't see their map mutated by the
// activity even on a stub Transactor that doesn't roll back).
func newDailyWorkingState(in ManageRosterInput) *dailyWorkingState {
	placements := make(map[int64]RosterSlot, len(in.Roster.Placements))
	maps.Copy(placements, in.Roster.Placements)
	limits := make(map[RosterSlot]int, len(in.Roster.Limits))
	maps.Copy(limits, in.Roster.Limits)
	freeAgents := make(map[int64]struct{}, len(in.FreeAgents))
	for _, id := range in.FreeAgents {
		freeAgents[id] = struct{}{}
	}
	onWaivers := make(map[int64]struct{}, len(in.OnWaivers))
	for _, id := range in.OnWaivers {
		onWaivers[id] = struct{}{}
	}
	positions := make(mapPositionCatalog, len(in.Positions))
	maps.Copy(positions, in.Positions)
	pendingClaims := make(map[int64]struct{}, len(in.PendingClaims))
	for _, id := range in.PendingClaims {
		pendingClaims[id] = struct{}{}
	}
	return &dailyWorkingState{
		roster:        RosterState{Placements: placements, Limits: limits},
		fa:            PoolFreeAgentState{FreeAgents: freeAgents, OnWaivers: onWaivers},
		positions:     positions,
		pendingClaims: pendingClaims,
	}
}

// makeDailyToolExecutor returns the agentloop ToolExecutor closure.
//
// The executor resolves the tool call (RecoverAction handles fuzzy
// names + lenient JSON), runs the matching validator, and on success
// records the action + mutates the working state. Validation
// failures are reported to the LLM as tool-result strings; the
// executor itself NEVER returns a non-nil error (which would abort
// the loop).
//
// V1 design choice: an executor error would also abort and lose
// every prior accepted action. Returning string-formatted errors
// preserves all good actions and lets the agent self-correct on the
// next round.
//
// Each failure path additionally increments
// puckdb_sim_llm_failures_total with the appropriate reason label
// — RecoverAction failures = parse_error, validator rejections =
// tool_use_failure. agentCfg supplies the provider+model labels.
func (a *Activities) makeDailyToolExecutor(work *dailyWorkingState, outcome *dailyOutcome, tools []llm.Tool, agentCfg AgentConfig) agentloop.ToolExecutor {
	failTUF := func() {
		metrics.IncSimLLMFailure(agentCfg.Provider, agentCfg.Model, metrics.SimFailureToolUseFailure)
	}

	// recordCall is the shared post-amble that finalizes a ToolCallCapture
	// and appends it to outcome.toolCaptures. Returns the result string
	// (so callers can `return recordCall(...)` in one line) and the
	// capture's index in the slice (used by accepted actions to backfill
	// applied_transaction_id at commit time).
	recordCall := func(capture ToolCallCapture, callStart time.Time, result string) (string, int) {
		capture.Result = result
		capture.Latency = time.Since(callStart)
		outcome.toolCaptures = append(outcome.toolCaptures, capture)
		return result, len(outcome.toolCaptures) - 1
	}

	return func(_ context.Context, call llm.ToolCall) (string, error) {
		callStart := time.Now()
		capture := ToolCallCapture{
			ToolName:     call.Function.Name,
			ArgumentsRaw: call.Function.Arguments,
		}

		action, dist, err := RecoverAction(call, tools)
		if err != nil {
			metrics.IncSimLLMFailure(agentCfg.Provider, agentCfg.Model, metrics.SimFailureParseError)
			capture.Outcome = ToolCallOutcomeParseError
			capture.FailureReason = err.Error()
			result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
			return result, nil
		}
		capture.RecoveredName = RecoveredName(call.Function.Name, action, dist)

		switch act := action.(type) {
		case AddPlayerArgs:
			if err := ValidateAddPlayer(act, work.roster, work.fa); err != nil {
				failTUF()
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = err.Error()
				result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
				return result, nil
			}
			applyAddToWorkingState(work, act)
			capture.Outcome = ToolCallOutcomeAccepted
			result, idx := recordCall(capture, callStart, "ok: player added")
			outcome.actions = append(outcome.actions, recordedAction{args: act, toolCaptureIndex: idx})
			return result, nil

		case ClaimPlayerArgs:
			if err := ValidateClaimPlayer(act, work.roster, work.fa, work.pendingClaims); err != nil {
				failTUF()
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = err.Error()
				result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
				return result, nil
			}
			// Claim doesn't mutate roster (the drop is conditional
			// on winning the claim) — only mark the player out of
			// OnWaivers so the agent can't double-claim within the
			// same turn, and record the pending claim so a repeat
			// claim_player on the same player this turn is rejected.
			delete(work.fa.OnWaivers, act.PlayerID)
			work.pendingClaims[act.PlayerID] = struct{}{}
			capture.Outcome = ToolCallOutcomeAccepted
			result, idx := recordCall(capture, callStart, "ok: claim filed")
			outcome.actions = append(outcome.actions, recordedAction{args: act, toolCaptureIndex: idx})
			return result, nil

		case DropPlayerArgs:
			if err := ValidateDropPlayer(act, work.roster); err != nil {
				failTUF()
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = err.Error()
				result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
				return result, nil
			}
			delete(work.roster.Placements, act.PlayerID)
			delete(work.positions, act.PlayerID)
			capture.Outcome = ToolCallOutcomeAccepted
			result, idx := recordCall(capture, callStart, "ok: player dropped")
			outcome.actions = append(outcome.actions, recordedAction{args: act, toolCaptureIndex: idx})
			return result, nil

		case SetLineupArgs:
			resolved, err := ValidateAndResolveLineup(act, work.roster, work.positions)
			if err != nil {
				failTUF()
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = err.Error()
				result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
				return result, nil
			}
			applyLineupToWorkingState(work, resolved)
			capture.Outcome = ToolCallOutcomeAccepted
			result, idx := recordCall(capture, callStart, "ok: lineup set")
			outcome.actions = append(outcome.actions, recordedAction{
				args:             act,
				resolvedLineup:   resolved,
				toolCaptureIndex: idx,
			})
			return result, nil

		case UpdateNotesArgs:
			if err := ValidateUpdateNotes(act); err != nil {
				failTUF()
				capture.Outcome = ToolCallOutcomeValidationRejected
				capture.FailureReason = err.Error()
				result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %v", err))
				return result, nil
			}
			notes := act.Notes
			outcome.notes = &notes
			capture.Outcome = ToolCallOutcomeAccepted
			result, _ := recordCall(capture, callStart, "ok: notes updated")
			return result, nil

		default:
			failTUF()
			capture.Outcome = ToolCallOutcomeUnhandled
			capture.FailureReason = fmt.Sprintf("tool %q not allowed in daily turn", action.ToolName())
			result, _ := recordCall(capture, callStart, fmt.Sprintf("error: %s", capture.FailureReason))
			return result, nil
		}
	}
}

// applyAddToWorkingState applies an add (with optional drop) to the
// working roster + FA pool so the next tool call validates against
// the post-add state. New players land in BN (the daily turn doesn't
// auto-promote into active slots; that's set_lineup's job).
func applyAddToWorkingState(work *dailyWorkingState, args AddPlayerArgs) {
	if args.DropPlayerID != nil {
		delete(work.roster.Placements, *args.DropPlayerID)
		delete(work.positions, *args.DropPlayerID)
	}
	work.roster.Placements[args.PlayerID] = SlotBN
	delete(work.fa.FreeAgents, args.PlayerID)
}

// applyLineupToWorkingState rewrites work.roster.Placements based on
// the resolved move list. Each move is (playerID, fromSlot, toSlot,
// optionalDisplacement); applying them in order matches what the
// commit path will do. ResolvedLineupMove uses 0 as the
// "no-displacement" sentinel.
func applyLineupToWorkingState(work *dailyWorkingState, resolved []ResolvedLineupMove) {
	for _, m := range resolved {
		work.roster.Placements[m.PlayerID] = m.ToSlot
		if m.DisplacedPlayerID != 0 {
			work.roster.Placements[m.DisplacedPlayerID] = SlotBN
		}
	}
}

// commitDailyTurn writes everything from dailyOutcome to the DB in
// one transaction.
//
// Order of operations within the tx:
//  1. Per-action inserts/updates. SetLineup → InsertSimTransactionLineupSet
//     first (returns the parent ID), then sim_lineup_moves children.
//     Add/Drop/Claim → mutate sim_rosters, then insert their tx row.
//  2. UpdateSimAgentNotes if notes were set.
//  3. InsertSimTransactionPass / InsertSimTransactionError if no
//     actions ran.
//  4. IncrementSimPoolLLMCost.
//
// At least one of {actions, pass, error, notes-only} writes a tx row
// — the idempotency probe for the next attempt depends on it. Notes-
// only turns ALSO write a `pass` row to mark the day processed.
// commitDailyTurn takes recordMessages (the sim_pools.record_full_messages
// flag) as a parameter rather than reading it here: the read happens
// up-front in ManageRoster, before the LLM runs, so a flaky read can't
// discard a completed, paid-for turn (see ManageRoster).
func (a *Activities) commitDailyTurn(ctx context.Context, in ManageRosterInput, outcome dailyOutcome, recordMessages bool) error {
	costNum, err := numericFromFloat(outcome.costUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode cost: %w", err)
	}

	messages := transcriptForRecording(recordMessages, outcome.res)

	return a.Tx.InTx(ctx, func(q SimQueries) error {
		// Commit-time revalidation: an add validated against the morning
		// FA snapshot can lose to another agent who took the same player
		// earlier today. Re-check claimability now and drop conflicting
		// adds (and any lineup move depending on them) so the conflict
		// becomes a clean rejected action instead of a UNIQUE-constraint
		// violation that rolls back the whole turn and re-bills the LLM.
		actions, err := reviseActionsForCommitConflicts(ctx, q, in, outcome.actions, outcome.toolCaptures)
		if err != nil {
			return err
		}

		// Apply each accepted action and feed the resulting tx id back
		// into the matching ToolCallCapture entry so the telemetry row
		// can link applied_transaction_id ↔ sim_transactions.id.
		for _, act := range actions {
			txID, err := applyAction(ctx, q, in, act)
			if err != nil {
				return err
			}
			if act.toolCaptureIndex >= 0 && act.toolCaptureIndex < len(outcome.toolCaptures) {
				outcome.toolCaptures[act.toolCaptureIndex].AppliedTransactionID = txID
			}
		}

		if outcome.notes != nil {
			if err := q.UpdateSimAgentNotes(ctx, sqlcdb.UpdateSimAgentNotesParams{
				ID:    in.AgentID,
				Notes: *outcome.notes,
			}); err != nil {
				return fmt.Errorf("update notes: %w", err)
			}
		}

		// Audit row when the agent didn't commit anything actionable: a
		// `pass` (or `error`) row recording the no-action outcome for the
		// log. Uses the post-revision count so a turn whose only actions
		// all lost the commit-time recheck still gets a pass marker.
		if len(actions) == 0 {
			if err := writeMarkerRow(ctx, q, in, outcome); err != nil {
				return err
			}
		}

		// Dedicated idempotency marker — written on every turn so
		// ExistsSimDailyTurnMarker fires on retry. Decoupled from the
		// audit rows above so an unrelated transaction at this coordinate
		// can't spuriously skip a future turn.
		if err := q.InsertSimTransactionDailyTurnDone(ctx, sqlcdb.InsertSimTransactionDailyTurnDoneParams{
			PoolID:  in.PoolID,
			AgentID: in.AgentID,
			Date:    in.SimDate,
		}); err != nil {
			return fmt.Errorf("insert daily turn marker: %w", err)
		}

		if _, err := q.IncrementSimPoolLLMCost(ctx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              in.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}

		// Turn telemetry — landed inside the same tx as the action rows
		// so a crash mid-commit leaves all-or-nothing state.
		header := dailyTurnHeader(in, outcome)
		if _, err := RecordTurnTelemetry(ctx, q, TurnTelemetry{
			Header:         header,
			Captures:       outcome.captures,
			ToolCalls:      outcome.toolCaptures,
			Messages:       messages,
			RecordMessages: recordMessages,
		}); err != nil {
			return err
		}
		return nil
	})
}

// dailyTurnHeader builds the TurnHeader for a daily ManageRoster turn.
// status is derived from the dailyOutcome's flags: errored if the agent
// loop or marker write classified it that way, else ok (passed turns
// still produced a turn — they just produced a pass marker, not actions).
func dailyTurnHeader(in ManageRosterInput, outcome dailyOutcome) TurnHeader {
	status := TurnStatusOK
	if outcome.errored {
		status = TurnStatusErrored
	}
	return TurnHeader{
		PoolID:      in.PoolID,
		AgentID:     in.AgentID,
		SimDate:     in.SimDate,
		Phase:       TurnPhaseDaily,
		Status:      status,
		ErrorKind:   outcome.errorKind,
		ErrorDetail: outcome.errorDetail,
		Provider:    in.AgentConfig.Provider,
		Model:       in.AgentConfig.Model,
		Temperature: in.AgentConfig.Temperature,
		MaxTokens:   in.AgentConfig.MaxTokens,
		CostUsd:     outcome.costUsd,
		FinalText:   outcome.reasoning,
		StartedAt:   outcome.startedAt,
		CompletedAt: outcome.completedAt,
	}
}

// writeMarkerRow emits the no-action transaction row: `error` if
// dailyOutcome.errored is set, otherwise `pass`. Either way, the
// (pool, agent, date) coordinate gets a row so subsequent retries
// short-circuit on the idempotency probe.
func writeMarkerRow(ctx context.Context, q SimQueries, in ManageRosterInput, outcome dailyOutcome) error {
	if outcome.errored {
		_, err := q.InsertSimTransactionError(ctx, sqlcdb.InsertSimTransactionErrorParams{
			PoolID:      in.PoolID,
			AgentID:     in.AgentID,
			Date:        in.SimDate,
			Reasoning:   outcome.reasoning,
			ErrorKind:   pgtype.Text{String: string(outcome.errorKind), Valid: outcome.errorKind != ""},
			ErrorDetail: pgtype.Text{String: outcome.errorDetail, Valid: outcome.errorDetail != ""},
		})
		if err != nil {
			return fmt.Errorf("insert error tx: %w", err)
		}
		return nil
	}
	if _, err := q.InsertSimTransactionPass(ctx, sqlcdb.InsertSimTransactionPassParams{
		PoolID:    in.PoolID,
		AgentID:   in.AgentID,
		Date:      in.SimDate,
		Reasoning: outcome.reasoning,
	}); err != nil {
		return fmt.Errorf("insert pass tx: %w", err)
	}
	return nil
}

// reviseActionsForCommitConflicts re-validates the turn's accepted
// adds against live roster state inside the commit tx and returns the
// actions that should actually be applied.
//
// The free-agent pool is built once per day and shared across agents,
// so an add validated at turn start can collide with another agent who
// took the same player earlier today — the UNIQUE (pool_id, player_id)
// constraint on sim_rosters would then abort the whole tx on retry.
// Per the add_player tool contract ("first-processed agent wins"), the
// loser's add is dropped here and recorded as commit_rejected in the
// telemetry capture rather than failing the turn.
//
// A dropped add also invalidates any set_lineup move that placed the
// now-unowned player; those moves are stripped. A lineup action left
// with no moves is itself dropped (and marked commit_rejected).
func reviseActionsForCommitConflicts(
	ctx context.Context,
	q SimQueries,
	in ManageRosterInput,
	actions []recordedAction,
	captures []ToolCallCapture,
) ([]recordedAction, error) {
	rejectedPlayers := make(map[int64]struct{})
	kept := make([]recordedAction, 0, len(actions))

	markRejected := func(captureIdx int, reason string) {
		if captureIdx >= 0 && captureIdx < len(captures) {
			captures[captureIdx].Outcome = ToolCallOutcomeCommitRejected
			captures[captureIdx].FailureReason = reason
			captures[captureIdx].Result = "error: " + reason
		}
	}

	for _, act := range actions {
		add, isAdd := act.args.(AddPlayerArgs)
		if !isAdd {
			kept = append(kept, act)
			continue
		}
		exists, err := q.ExistsSimRosterPlayer(ctx, sqlcdb.ExistsSimRosterPlayerParams{
			PoolID:   in.PoolID,
			PlayerID: add.PlayerID,
		})
		if err != nil {
			return nil, fmt.Errorf("commit-time roster check (player %d): %w", add.PlayerID, err)
		}
		if exists {
			rejectedPlayers[add.PlayerID] = struct{}{}
			markRejected(act.toolCaptureIndex, fmt.Sprintf(
				"add_player(%d): player already on a roster (another agent won the add first)", add.PlayerID))
			continue
		}
		kept = append(kept, act)
	}

	if len(rejectedPlayers) == 0 {
		return kept, nil
	}

	// Strip lineup moves that reference a rejected player. A lineup
	// action with no surviving moves is dropped entirely.
	revised := make([]recordedAction, 0, len(kept))
	for _, act := range kept {
		if _, isLineup := act.args.(SetLineupArgs); !isLineup {
			revised = append(revised, act)
			continue
		}
		moves := make([]ResolvedLineupMove, 0, len(act.resolvedLineup))
		for _, m := range act.resolvedLineup {
			if _, rejected := rejectedPlayers[m.PlayerID]; rejected {
				continue
			}
			moves = append(moves, m)
		}
		if len(moves) == 0 {
			markRejected(act.toolCaptureIndex,
				"set_lineup: all moves referenced players whose add lost the commit-time recheck")
			continue
		}
		act.resolvedLineup = moves
		revised = append(revised, act)
	}
	return revised, nil
}

// applyAction is the per-action commit dispatcher. The action types
// are pairwise disjoint, so a switch fully covers the recorded set;
// an unhandled type is a programming bug (the executor would have
// rejected anything not in the daily tool set).
//
// Returns the new sim_transactions.id so commitDailyTurn can backfill
// the corresponding ToolCallCapture's applied_transaction_id.
func applyAction(ctx context.Context, q SimQueries, in ManageRosterInput, act recordedAction) (int32, error) {
	switch a := act.args.(type) {
	case AddPlayerArgs:
		return applyAdd(ctx, q, in, a.Reason, a)
	case ClaimPlayerArgs:
		return applyClaim(ctx, q, in, a.Reason, a)
	case DropPlayerArgs:
		return applyDrop(ctx, q, in, a.Reason, a)
	case SetLineupArgs:
		return applyLineupSet(ctx, q, in, a.Reason, act.resolvedLineup)
	default:
		return 0, fmt.Errorf("simulation: unhandled action type %T", act.args)
	}
}

// applyAdd: optional drop first, then insert the new roster row in
// BN, then the add transaction row. Order matters within the tx —
// the tx row's drop_player_id column references the dropped player
// and we want that row to land last so a future query that joins
// drop_player_id back to sim_rosters sees a consistent absence.
func applyAdd(ctx context.Context, q SimQueries, in ManageRosterInput, reasoning string, args AddPlayerArgs) (int32, error) {
	if args.DropPlayerID != nil {
		if err := q.DeleteSimRoster(ctx, sqlcdb.DeleteSimRosterParams{
			PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: *args.DropPlayerID,
		}); err != nil {
			return 0, fmt.Errorf("delete dropped roster row: %w", err)
		}
	}
	if err := q.InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
		PoolID:      in.PoolID,
		AgentID:     in.AgentID,
		PlayerID:    args.PlayerID,
		Slot:        string(SlotBN),
		AcquiredAt:  in.SimDate,
		AcquiredVia: string(AcquiredViaFreeAgent),
	}); err != nil {
		return 0, fmt.Errorf("insert added roster row: %w", err)
	}
	tx, err := q.InsertSimTransactionAdd(ctx, sqlcdb.InsertSimTransactionAddParams{
		PoolID:       in.PoolID,
		AgentID:      in.AgentID,
		Date:         in.SimDate,
		PlayerID:     pgtype.Int8{Int64: args.PlayerID, Valid: true},
		Reasoning:    reasoning,
		DropPlayerID: nullableInt8(args.DropPlayerID),
	})
	if err != nil {
		return 0, fmt.Errorf("insert add tx: %w", err)
	}
	return tx.ID, nil
}

// applyClaim: insert sim_waiver_claims row + claim transaction.
// The drop is NOT applied yet — it's contingent on winning the
// claim, which ProcessWaiversActivity resolves on the process date.
//
// process_date = sim_date + waiver_days; waiver_days lives in
// PoolConfig (PLAN.md > "Waivers"). If WaiverDays is 0 (config
// default), the claim processes the next day — same-day processing
// would race with this turn's other adds, so we bump by 1 minimum.
func applyClaim(ctx context.Context, q SimQueries, in ManageRosterInput, reasoning string, args ClaimPlayerArgs) (int32, error) {
	processDate := addDays(in.SimDate, claimProcessOffset(in.PoolConfig.WaiverDays))
	if _, err := q.InsertSimWaiverClaim(ctx, sqlcdb.InsertSimWaiverClaimParams{
		PoolID:       in.PoolID,
		AgentID:      in.AgentID,
		PlayerID:     args.PlayerID,
		DropPlayerID: nullableInt8(args.DropPlayerID),
		FiledDate:    in.SimDate,
		ProcessDate:  processDate,
	}); err != nil {
		return 0, fmt.Errorf("insert waiver claim: %w", err)
	}
	tx, err := q.InsertSimTransactionClaim(ctx, sqlcdb.InsertSimTransactionClaimParams{
		PoolID:       in.PoolID,
		AgentID:      in.AgentID,
		Date:         in.SimDate,
		PlayerID:     pgtype.Int8{Int64: args.PlayerID, Valid: true},
		Reasoning:    reasoning,
		DropPlayerID: nullableInt8(args.DropPlayerID),
	})
	if err != nil {
		return 0, fmt.Errorf("insert claim tx: %w", err)
	}
	return tx.ID, nil
}

// applyDrop: delete the roster row, log the drop transaction.
// The dropped player enters waivers via the (pool, drop_date,
// waiver_days) chain — ProcessWaiversActivity discovers them by
// joining sim_transactions.type='drop' rows.
func applyDrop(ctx context.Context, q SimQueries, in ManageRosterInput, reasoning string, args DropPlayerArgs) (int32, error) {
	if err := q.DeleteSimRoster(ctx, sqlcdb.DeleteSimRosterParams{
		PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: args.PlayerID,
	}); err != nil {
		return 0, fmt.Errorf("delete dropped roster row: %w", err)
	}
	tx, err := q.InsertSimTransactionDrop(ctx, sqlcdb.InsertSimTransactionDropParams{
		PoolID:    in.PoolID,
		AgentID:   in.AgentID,
		Date:      in.SimDate,
		PlayerID:  pgtype.Int8{Int64: args.PlayerID, Valid: true},
		Reasoning: reasoning,
	})
	if err != nil {
		return 0, fmt.Errorf("insert drop tx: %w", err)
	}
	return tx.ID, nil
}

// applyLineupSet: insert the parent lineup_set transaction (returns
// the new transaction.id), then one sim_lineup_moves child per
// resolved move. The parent + children land in the same tx so the
// FK is always satisfiable.
//
// Sequence is the move's index in the resolved list — preserves the
// agent's intended ordering for displaced-player tracing.
func applyLineupSet(ctx context.Context, q SimQueries, in ManageRosterInput, reasoning string, resolved []ResolvedLineupMove) (int32, error) {
	parent, err := q.InsertSimTransactionLineupSet(ctx, sqlcdb.InsertSimTransactionLineupSetParams{
		PoolID:    in.PoolID,
		AgentID:   in.AgentID,
		Date:      in.SimDate,
		Reasoning: reasoning,
	})
	if err != nil {
		return 0, fmt.Errorf("insert lineup_set tx: %w", err)
	}
	for _, m := range resolved {
		// Roster slot also has to update — the resolved move's
		// to_slot is where the player lands.
		if err := q.UpdateSimRosterSlot(ctx, sqlcdb.UpdateSimRosterSlotParams{
			PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: m.PlayerID, Slot: string(m.ToSlot),
		}); err != nil {
			return 0, fmt.Errorf("update lineup slot for player %d: %w", m.PlayerID, err)
		}
		if m.DisplacedPlayerID != 0 {
			// Displaced player flushed to BN.
			if err := q.UpdateSimRosterSlot(ctx, sqlcdb.UpdateSimRosterSlotParams{
				PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: m.DisplacedPlayerID, Slot: string(SlotBN),
			}); err != nil {
				return 0, fmt.Errorf("update displaced slot for player %d: %w", m.DisplacedPlayerID, err)
			}
		}
		var displacedPgx pgtype.Int8
		if m.DisplacedPlayerID != 0 {
			displacedPgx = pgtype.Int8{Int64: m.DisplacedPlayerID, Valid: true}
		}
		if err := q.InsertSimLineupMove(ctx, sqlcdb.InsertSimLineupMoveParams{
			TransactionID:     parent.ID,
			Sequence:          int32(m.Sequence),
			PlayerID:          m.PlayerID,
			FromSlot:          string(m.FromSlot),
			ToSlot:            string(m.ToSlot),
			DisplacedPlayerID: displacedPgx,
		}); err != nil {
			return 0, fmt.Errorf("insert lineup_move seq=%d: %w", m.Sequence, err)
		}
	}
	return parent.ID, nil
}

// nullableInt8 turns an *int64 (the args struct's drop_player_id
// representation) into a pgtype.Int8 with Valid set to false when
// the source is nil. Same shape DraftPickActivity uses for
// optional-int columns; lifted here to avoid duplicating the
// 4-line conversion in 5 places.
func nullableInt8(p *int64) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *p, Valid: true}
}

// claimProcessOffset is the calendar-day offset between filing a
// claim and processing it. PoolConfig.WaiverDays is the operator-
// configured value; we floor at 1 so a misconfigured "0 day waivers"
// pool still gets a tomorrow-not-today processing window —
// processing same-day races with the daily turn's adds.
func claimProcessOffset(waiverDays int) int {
	if waiverDays < 1 {
		return 1
	}
	return waiverDays
}

// addDays returns base + n calendar days. pgtype.Date wraps a
// time.Time; AddDate handles month/year rollover correctly.
func addDays(base pgtype.Date, n int) pgtype.Date {
	if !base.Valid {
		return base
	}
	return pgtype.Date{Time: base.Time.AddDate(0, 0, n), Valid: true}
}

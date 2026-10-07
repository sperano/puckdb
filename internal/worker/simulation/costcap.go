package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ============================================================================
// Cost-cap pre-flight helpers — shared by every LLM-driven activity.
//
// Both DraftPickActivity and ManageRosterActivity must consult
// sim_pools.total_llm_cost_usd before billing the operator's account
// for an LLM call (PLAN.md > "Cost cap > Enforcement"). The check
// (cap reached?) and the tripped-branch action (insert
// cost_cap_reached row + status=paused + workflow signal) are
// identical across activities, so they live here as receiver methods
// on *Activities rather than duplicated per-activity helpers.
//
// SignalPause + the runCostCapBranch retry semantics are documented
// in draft_activity.go; this file just lifts the shared
// implementations.
// ============================================================================

// SignalPause is the signal name LLM activities send to the parent
// workflow when the cost cap is reached. Matches the workflow's
// "pause" signal handler (Phase 3.2).
const SignalPause = "pause"

// checkCostCap reads sim_pools.total_llm_cost_usd and compares it to
// capUsd. Returns whether the cap has been reached, the current
// running total, and the cap echoed back (callers pass it on to
// runCostCapBranch unchanged when tripped).
//
// capUsd <= 0 short-circuits to "no cap" — a missing-field config
// (JSON zero value) must NOT lock the pool out forever. The intent
// of the cap field is "set high to disable" per PLAN.md; honoring
// that for both "high" and "missing" is the safe interpretation.
//
// The `current >= capUsd` comparison is a cumulative post-hoc soft
// ceiling: a call's token cost is unknowable until the call is made,
// so one call can overshoot the cap before the next check trips it.
// That one-call overshoot is inherent and expected — the cap bounds
// spend within one call's cost, not to the dollar.
//
// Any DB error aborts the activity so Temporal retries; we'd rather
// retry than silently re-bill the operator on a flaky read.
func (a *Activities) checkCostCap(ctx context.Context, poolID int32, capUsd float64) (tripped bool, currentCost, cap float64, err error) {
	if capUsd <= 0 {
		return false, 0, 0, nil
	}
	pool, err := a.Queries.GetSimPool(ctx, poolID)
	if err != nil {
		return false, 0, 0, fmt.Errorf("simulation: cost-cap probe (get pool): %w", err)
	}
	current, err := NumericToFloat(pool.TotalLLMCostUSD)
	if err != nil {
		return false, 0, 0, fmt.Errorf("simulation: decode total_llm_cost_usd: %w", err)
	}
	return current >= capUsd, current, capUsd, nil
}

// runCostCapBranch is the cost-cap-tripped path: insert the
// cost_cap_reached audit row + flip sim_pools.status to paused in one
// DB transaction, THEN signal the parent workflow with "pause".
//
// Order matters:
//
//   - DB writes are atomic — a half-applied state (row inserted but
//     status not flipped, or vice-versa) would corrupt the next
//     attempt's idempotency-probe + status assumptions.
//
//   - Signal goes out AFTER the DB tx commits. If the signal fails,
//     the activity returns an error so Temporal retries; on retry the
//     cap is still tripped, the row insert duplicates (audit trail
//     accepts duplicates — the row count of cost_cap_reached events
//     is itself a useful metric), and the signal retries. The
//     workflow's pause-signal handler is idempotent (PLAN.md >
//     "Signal interaction matrix"), so duplicate signals are no-ops.
//
// A nil Signaler is a configuration error (the production wiring in
// cmd/worker.go must inject one); we surface that loudly rather than
// silently dropping the pause signal.
func (a *Activities) runCostCapBranch(
	ctx context.Context,
	poolID, agentID int32,
	simDate pgtype.Date,
	workflowID string,
	currentCostUsd, capUsd float64,
) error {
	costNum, err := numericFromFloat(currentCostUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode current cost: %w", err)
	}
	capNum, err := numericFromFloat(capUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode cap: %w", err)
	}

	err = a.Tx.InTx(ctx, func(q SimQueries) error {
		if _, err := q.InsertSimTransactionCostCapReached(ctx, sqlcdb.InsertSimTransactionCostCapReachedParams{
			PoolID:  poolID,
			AgentID: agentID,
			Date:    simDate,
			CostUSD: costNum,
			CapUSD:  capNum,
		}); err != nil {
			return fmt.Errorf("simulation: insert cost_cap_reached: %w", err)
		}
		if err := q.UpdateSimPoolStatus(ctx, sqlcdb.UpdateSimPoolStatusParams{
			ID:     poolID,
			Status: string(PoolStatusPaused),
		}); err != nil {
			return fmt.Errorf("simulation: update pool status to paused: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.evictPoolAgents(poolID)

	if a.Signaler == nil {
		return fmt.Errorf("simulation: cost-cap tripped but no Signaler wired (cannot pause workflow %q)", workflowID)
	}
	if err := a.Signaler.SignalWorkflow(ctx, workflowID, "", SignalPause, nil); err != nil {
		return fmt.Errorf("simulation: signal workflow %q with pause: %w", workflowID, err)
	}
	return nil
}

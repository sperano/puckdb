package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// Cost-cap pre-flight helpers — shared by every LLM-driven activity.
//
// Both DraftPickActivity and ManageRosterActivity must consult
// sim_pools.total_llm_cost_usd before billing the operator's account
// for an LLM call (PLAN.md > "Cost cap > Enforcement"). The check
// (cap reached?) and the tripped-branch action (the durable pause
// transition) are identical across activities, so they live here as
// functions over costCapQueries rather than duplicated per-activity
// helpers.
//
// The workflow learns about a tripped cap from the activity result
// alone (SkipReasonCostCapReached) and stops there; no signal is sent.
// ============================================================================

// costCapQueries is the cost-cap pre-flight: the pool's running LLM
// spend and the durable pause transition (status paused + one
// cost_cap_reached row per pool).
type costCapQueries interface {
	GetSimPool(ctx context.Context, id int32) (sqlcdb.SimPool, error)
	PauseSimPoolForCostCap(ctx context.Context, arg sqlcdb.PauseSimPoolForCostCapParams) (int64, error)
}

// checkCostCap reads sim_pools.total_llm_cost_usd and compares it to
// capUsd. Returns whether the cap has been reached, the current
// running total, and the cap echoed back (callers pass it on to
// pauseForCostCap unchanged when tripped).
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
func checkCostCap(ctx context.Context, q costCapQueries, poolID int32, capUsd float64) (tripped bool, currentCost, cap float64, err error) {
	if capUsd <= 0 {
		return false, 0, 0, nil
	}
	pool, err := q.GetSimPool(ctx, poolID)
	if err != nil {
		return false, 0, 0, fmt.Errorf("simulation: cost-cap probe (get pool): %w", err)
	}
	current, err := pgconv.NumericToFloat(pool.TotalLLMCostUSD)
	if err != nil {
		return false, 0, 0, fmt.Errorf("simulation: decode total_llm_cost_usd: %w", err)
	}
	return current >= capUsd, current, capUsd, nil
}

// pauseForCostCap is the cost-cap-tripped path: the one durable
// transition that flips sim_pools.status to paused and writes the
// cost_cap_reached audit row (PauseSimPoolForCostCap, one statement, so
// the two land together or not at all). The caller then returns
// SkipReasonCostCapReached, which is what stops the workflow.
//
// Retry semantics: the transition only fires while the pool has not
// ended (PoolStatus.ended), so a serial retry (the activity response was
// lost after the commit) and an overlapping attempt (a timed-out attempt
// still running next to its retry; the second waits on the pool row
// lock) both write nothing and still return the cost-cap result. One
// pause, one audit row, however many attempts reach this branch; a late
// attempt never turns a completed or cancelled pool into a paused one.
//
// agents is this worker's agent cache, which drops the pool's agents.
func pauseForCostCap(
	ctx context.Context,
	q costCapQueries,
	agents agentSource,
	poolID, agentID int32,
	simDate pgtype.Date,
	currentCostUsd, capUsd float64,
) error {
	costNum, err := pgconv.NumericFromFloat(currentCostUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode current cost: %w", err)
	}
	capNum, err := pgconv.NumericFromFloat(capUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode cap: %w", err)
	}
	written, err := q.PauseSimPoolForCostCap(ctx, sqlcdb.PauseSimPoolForCostCapParams{
		PoolID:  poolID,
		AgentID: agentID,
		Date:    simDate,
		CostUSD: costNum,
		CapUSD:  capNum,
	})
	if err != nil {
		return fmt.Errorf("simulation: pause pool for cost cap: %w", err)
	}
	if written > 0 {
		activity.GetLogger(ctx).Info("Cost cap reached; pool paused",
			"pool_id", poolID, "agent_id", agentID, "cost_usd", currentCostUsd, "cap_usd", capUsd)
	}
	// Paused is an ended status; drop the pool's cached agents. Also on
	// a retry that wrote nothing: this worker may still hold them.
	agents.evictPoolAgents(poolID)
	return nil
}

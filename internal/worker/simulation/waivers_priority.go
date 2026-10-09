package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// errWaiverPriorityIncomplete reports a pool whose sim_waiver_priority rows
// do not rank every agent exactly once as 1..N. Resolution refuses to run on
// it: a claimant without a row would silently lose every contested claim, and
// a duplicated or missing priority number makes the winner arbitrary.
var errWaiverPriorityIncomplete = errors.New("waiver priority incomplete")

// loadWaiverPriority returns the pool's waiver priority rows, locked FOR
// UPDATE, after initializing them on the pool's first resolution and checking
// that they are complete. initialized reports whether this call created them.
//
// Initialization (InitSimWaiverPriority) ranks every agent in reverse draft
// order from sim_agents.draft_position, which RecordDraftOrder persisted
// before the first pick, so the season phase always finds it final. It is a
// no-op once the pool has rows, so a retry neither duplicates nor resets an
// order that winners have since rotated. The caller must hold LockSimPool:
// that makes the empty-table check and the insert atomic against another
// resolution of the same pool.
func loadWaiverPriority(ctx context.Context, q SimQueries, poolID int32) (priorities []sqlcdb.SimWaiverPriority, initialized bool, err error) {
	inserted, err := q.InitSimWaiverPriority(ctx, poolID)
	if err != nil {
		return nil, false, fmt.Errorf("simulation: initialize waiver priority (pool %d): %w", poolID, err)
	}
	priorities, err = q.ListSimWaiverPriorityByPool(ctx, poolID)
	if err != nil {
		return nil, false, fmt.Errorf("simulation: list waiver priority (pool %d): %w", poolID, err)
	}
	agents, err := q.ListSimAgentsByPool(ctx, poolID)
	if err != nil {
		return nil, false, fmt.Errorf("simulation: list agents (pool %d): %w", poolID, err)
	}
	if err := checkWaiverPriorityComplete(agents, priorities); err != nil {
		return nil, false, fmt.Errorf("simulation: pool %d: %w", poolID, err)
	}
	return priorities, inserted > 0, nil
}

// checkWaiverPriorityComplete verifies that priorities rank each agent of the
// pool exactly once and that the priority numbers are exactly 1..N. A pool
// with no rows fails here too: initialization inserts nothing while an agent
// has no recorded draft position.
func checkWaiverPriorityComplete(agents []sqlcdb.SimAgent, priorities []sqlcdb.SimWaiverPriority) error {
	if len(priorities) != len(agents) {
		return fmt.Errorf("%w: %d agents but %d priority rows (draft order not recorded?)",
			errWaiverPriorityIncomplete, len(agents), len(priorities))
	}
	inPool := make(map[int32]struct{}, len(agents))
	for _, a := range agents {
		inPool[a.ID] = struct{}{}
	}
	ranked := make(map[int32]struct{}, len(priorities))
	numbers := make(map[int32]struct{}, len(priorities))
	for _, p := range priorities {
		if _, ok := inPool[p.AgentID]; !ok {
			return fmt.Errorf("%w: agent %d has a priority row but is not in the pool", errWaiverPriorityIncomplete, p.AgentID)
		}
		if _, dup := ranked[p.AgentID]; dup {
			return fmt.Errorf("%w: agent %d has more than one priority row", errWaiverPriorityIncomplete, p.AgentID)
		}
		ranked[p.AgentID] = struct{}{}
		if p.Priority < 1 || int(p.Priority) > len(priorities) {
			return fmt.Errorf("%w: priority %d of agent %d is outside 1..%d",
				errWaiverPriorityIncomplete, p.Priority, p.AgentID, len(priorities))
		}
		if _, dup := numbers[p.Priority]; dup {
			return fmt.Errorf("%w: priority %d is held by more than one agent", errWaiverPriorityIncomplete, p.Priority)
		}
		numbers[p.Priority] = struct{}{}
	}
	return nil
}

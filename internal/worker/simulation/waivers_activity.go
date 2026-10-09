package simulation

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// missingWaiverPriority ranks a claimant with no sim_waiver_priority row
// behind every ranked agent, so a map miss (zero value) can't award them
// first dibs. ProcessWaivers refuses to resolve a pool whose priority rows
// are incomplete (checkWaiverPriorityComplete), so this only guards
// resolveGroup against a caller that passes a partial priority list.
const missingWaiverPriority int32 = math.MaxInt32

// ============================================================================
// ProcessWaiversActivity — resolve every claim due today.
//
// Lifecycle, all in one transaction (Transactor.InTx):
//
//	1. LockSimPool(pool) — serializes resolution per pool. A second
//	   attempt (a Temporal retry that overlaps a still-running first
//	   attempt after a start-to-close timeout) waits here, then sees
//	   what the first one committed.
//	2. loadWaiverPriority — on the pool's first run, insert one priority
//	   row per agent in reverse draft order; then lock the rows and check
//	   they rank every agent exactly once as 1..N.
//	3. ListSimWaiverClaimsForDuePlayers(pool, today) — pending claims on
//	   any player with a claim due today, read and locked after the pool
//	   lock. Empty → Skipped result (any priority initialization from step
//	   2 still commits); common when the day-loop iterates a calendar day
//	   with no waiver activity.
//	4. Group by player_id. Single-claim groups are uncontested and the
//	   lone claimant wins. Multi-claim groups are contested; the agent
//	   with the lowest priority NUMBER wins (PLAN.md > "Waivers" — Yahoo
//	   convention: priority 1 = first dibs).
//	5. For each (player, claims) group:
//	     - commit-time revalidation (read-only): player still free,
//	       winner's roster fits the add after any still-valid drop;
//	       otherwise the whole group resolves lost with no roster write
//	     - winner gets the player added to BN
//	     - winner's drop_player_id (if set) gets removed from roster
//	     - winning claim status → 'won', losing claims → 'lost', each
//	       only from 'pending': a claim that is no longer pending fails
//	       the transaction instead of being rewritten
//	     - sim_transactions logs an `add` (and optionally `drop`)
//	       for the winner so the daily transaction log reflects
//	       the resolved-claim outcome
//	   after all groups: re-rank waiver priority — winners move to
//	   the bottom in their original priority order; non-winners
//	   keep relative order, compacted up.
//
// "Unclaimed waiver players become free agents" is implicit: the
// FA-pool query (`ListSimFreeAgentCandidates`) excludes only players
// dropped within `waiver_days`. Once a player's window expires
// without a winning claim, they reappear in the FA pool naturally —
// no active step here.
//
// PLAN.md > "Waivers" and PLAN.md > "Day Loop" step 1 are the
// canonical specs.
// ============================================================================

// ProcessWaiversInput is the per-day payload. SimDate is "today" —
// the day the workflow is processing. Resolution date for the
// claim status update is the same day.
type ProcessWaiversInput struct {
	PoolID  int32       `json:"pool_id"`
	SimDate pgtype.Date `json:"sim_date"`
	// RosterCapacity is the pool's total roster size (sum of every slot
	// limit). Resolution re-checks the winner's roster against this — a
	// roster grown to capacity between filing and process_date can't
	// absorb the add unless a valid drop frees a spot.
	RosterCapacity int32 `json:"roster_capacity"`
}

// ProcessWaiversResult summarizes the activity's effects. The
// workflow uses ContestedGroups to expose "X agents fought over Y
// players" telemetry; ClaimsWon / ClaimsLost back the per-pool
// waiver dashboard.
type ProcessWaiversResult struct {
	Skipped         bool `json:"skipped"`
	ClaimsResolved  int  `json:"claims_resolved"`
	ClaimsWon       int  `json:"claims_won"`
	ClaimsLost      int  `json:"claims_lost"`
	ContestedGroups int  `json:"contested_groups"`
	// PriorityInitialized is true when this run created the pool's
	// waiver priority rows (its first resolution).
	PriorityInitialized bool `json:"priority_initialized"`
}

// ProcessWaivers is the Temporal-activity entry point.
//
// Returns nil error on the no-op path (no due claims). DB failures and
// an incomplete or stale priority/claim state abort, rolling back.
func (a *Activities) ProcessWaivers(ctx context.Context, in ProcessWaiversInput) (ProcessWaiversResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("ProcessWaivers start",
		"pool_id", in.PoolID,
		"sim_date", in.SimDate.Time,
	)

	var result ProcessWaiversResult
	err := a.Tx.InTx(ctx, func(q SimQueries) error {
		var err error
		result, err = processDueWaivers(ctx, q, in)
		return err
	})
	if err != nil {
		return ProcessWaiversResult{}, err
	}

	if result.PriorityInitialized {
		logger.Info("ProcessWaivers initialized waiver priority from reverse draft order", "pool_id", in.PoolID)
	}
	if result.Skipped {
		logger.Debug("ProcessWaivers skipped — no due claims")
		return result, nil
	}
	logger.Debug("ProcessWaivers complete",
		"resolved", result.ClaimsResolved,
		"won", result.ClaimsWon,
		"lost", result.ClaimsLost,
		"contested", result.ContestedGroups,
	)
	return result, nil
}

// processDueWaivers is ProcessWaivers' transaction body; q must be scoped
// to the open transaction. Every read happens after LockSimPool, so the
// claims and priorities it resolves are the ones any earlier attempt for
// this pool committed, never a snapshot taken before that commit.
func processDueWaivers(ctx context.Context, q SimQueries, in ProcessWaiversInput) (ProcessWaiversResult, error) {
	if _, err := q.LockSimPool(ctx, in.PoolID); err != nil {
		return ProcessWaiversResult{}, fmt.Errorf("simulation: lock pool %d: %w", in.PoolID, err)
	}
	priorities, initialized, err := loadWaiverPriority(ctx, q, in.PoolID)
	if err != nil {
		return ProcessWaiversResult{}, err
	}
	result := ProcessWaiversResult{PriorityInitialized: initialized}

	// Group ALL pending claims per player whose earliest claim is due
	// today — cross-day claims on the same player resolve together so
	// waiver priority is honored and a later-dated claim can't sneak
	// through as an uncontested win after the player was already taken.
	claims, err := q.ListSimWaiverClaimsForDuePlayers(ctx, sqlcdb.ListSimWaiverClaimsForDuePlayersParams{
		PoolID:      in.PoolID,
		ProcessDate: in.SimDate,
	})
	if err != nil {
		return ProcessWaiversResult{}, fmt.Errorf("simulation: list waiver claims for due players: %w", err)
	}
	if len(claims) == 0 {
		result.Skipped = true
		return result, nil
	}

	finalPriorities, err := resolveClaimGroups(ctx, q, in, groupClaimsByPlayer(claims), priorities, &result)
	if err != nil {
		return ProcessWaiversResult{}, err
	}
	// Write the final priority order. finalPriorities reflects every
	// per-group rotation applied during this run.
	for _, p := range finalPriorities {
		if err := q.UpdateSimWaiverPriority(ctx, sqlcdb.UpdateSimWaiverPriorityParams{
			PoolID: in.PoolID, AgentID: p.AgentID, Priority: p.Priority,
		}); err != nil {
			return ProcessWaiversResult{}, fmt.Errorf("update waiver priority for agent %d: %w", p.AgentID, err)
		}
	}
	return result, nil
}

// resolveClaimGroups applies each group's resolution in order, tallying
// into result, and returns the priority order after the run's rotations.
//
// The live order is updated between groups: each win rotates the winner to
// the bottom before the next group resolves, so a priority-1 agent doesn't
// win every contested player claimed on the same day — matching the Yahoo
// convention cited in the migration notes.
func resolveClaimGroups(
	ctx context.Context,
	q SimQueries,
	in ProcessWaiversInput,
	groups [][]sqlcdb.SimWaiverClaim,
	priorities []sqlcdb.SimWaiverPriority,
	result *ProcessWaiversResult,
) ([]sqlcdb.SimWaiverPriority, error) {
	current := clonePriorities(priorities)
	for _, group := range groups {
		r := resolveGroup(group, current)

		won, err := applyWaiverResolution(ctx, q, in, r)
		if err != nil {
			return nil, err
		}
		if won {
			result.ClaimsWon++
			result.ClaimsLost += len(r.losers)
			current = demoteWinners(current, []int32{r.winner.AgentID})
		} else {
			// Player wasn't claimable at resolution (already
			// rostered, or the winner's roster couldn't fit the
			// add): every claim in the group resolves as lost.
			result.ClaimsLost += 1 + len(r.losers)
		}
		result.ClaimsResolved += 1 + len(r.losers)
		if len(r.losers) > 0 {
			result.ContestedGroups++
		}
	}
	return current, nil
}

// claimResolution is the in-memory plan for one player_id's worth of
// claims: who wins, who loses, what to write.
type claimResolution struct {
	playerID int64
	winner   sqlcdb.SimWaiverClaim
	losers   []sqlcdb.SimWaiverClaim
}

// groupClaimsByPlayer groups claims by player_id and returns them as
// a slice of claim slices in deterministic player_id-ascending order.
// Each inner slice holds all claims for one player.
func groupClaimsByPlayer(claims []sqlcdb.SimWaiverClaim) [][]sqlcdb.SimWaiverClaim {
	byPlayer := map[int64][]sqlcdb.SimWaiverClaim{}
	playerOrder := []int64{}
	for _, c := range claims {
		if _, ok := byPlayer[c.PlayerID]; !ok {
			playerOrder = append(playerOrder, c.PlayerID)
		}
		byPlayer[c.PlayerID] = append(byPlayer[c.PlayerID], c)
	}
	slices.Sort(playerOrder)

	groups := make([][]sqlcdb.SimWaiverClaim, 0, len(byPlayer))
	for _, pid := range playerOrder {
		groups = append(groups, byPlayer[pid])
	}
	return groups
}

// resolveGroup picks the winner from a single player's claim group
// using the provided priority list. The priority list reflects the
// current live order (updated between groups so earlier wins rotate
// the winner to the bottom before subsequent groups are resolved).
//
// Sort direction: lower priority number = higher waiver priority =
// wins. Tie-break by claim ID ascending for determinism (ties
// shouldn't happen in a well-configured pool but are cheap to guard).
// An agent with no priority row sorts last (missingWaiverPriority) —
// a map miss must not hand them Go's zero value, which would rank
// them ahead of every seeded agent.
func resolveGroup(group []sqlcdb.SimWaiverClaim, priorities []sqlcdb.SimWaiverPriority) claimResolution {
	priorityOf := make(map[int32]int32, len(priorities))
	for _, p := range priorities {
		priorityOf[p.AgentID] = p.Priority
	}
	rank := func(agentID int32) int32 {
		if p, ok := priorityOf[agentID]; ok {
			return p
		}
		return missingWaiverPriority
	}

	sorted := make([]sqlcdb.SimWaiverClaim, len(group))
	copy(sorted, group)
	slices.SortStableFunc(sorted, func(a, b sqlcdb.SimWaiverClaim) int {
		pa := rank(a.AgentID)
		pb := rank(b.AgentID)
		if pa != pb {
			return cmp.Compare(pa, pb)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return claimResolution{
		playerID: sorted[0].PlayerID,
		winner:   sorted[0],
		losers:   sorted[1:],
	}
}

// clonePriorities returns a fresh copy of the priority slice so
// in-place updates (via demoteWinners) don't mutate the original.
func clonePriorities(priorities []sqlcdb.SimWaiverPriority) []sqlcdb.SimWaiverPriority {
	out := make([]sqlcdb.SimWaiverPriority, len(priorities))
	copy(out, priorities)
	return out
}

// resolveClaims groups claims by player_id and picks the winner per
// group via priority lookup. Returns one claimResolution per claimed
// player, in deterministic player_id order so tests can assert on
// the output without sorting.
//
// NOTE: this function resolves ALL groups using a single priority
// snapshot, which means the winner of group 1 doesn't rotate to the
// bottom before group 2 is resolved. Use groupClaimsByPlayer +
// resolveGroup sequentially when per-group rotation matters (the
// ProcessWaivers activity path). This function is kept for tests that
// exercise the pure grouping + priority-selection logic in isolation.
//
// The priority map: agent_id → priority number. An agent without a
// row in priorities is treated as missingWaiverPriority (lowest).
func resolveClaims(claims []sqlcdb.SimWaiverClaim, priorities []sqlcdb.SimWaiverPriority) []claimResolution {
	groups := groupClaimsByPlayer(claims)
	resolutions := make([]claimResolution, 0, len(groups))
	for _, group := range groups {
		resolutions = append(resolutions, resolveGroup(group, priorities))
	}
	return resolutions
}

// applyWaiverResolution writes the DB effects of one resolution and
// reports whether the player was actually awarded (true) or the whole
// group resolved as lost (false).
//
// The verdict comes from planWaiverResolution before any write, so the
// drop/add pair is applied only on the winning path — a rejected claim
// leaves the roster untouched.
//
// On a win, all other still-pending claims on the player (the losers in
// this group plus any cross-day claim not in the due window) are voided
// so they can't later resolve as phantom uncontested wins.
func applyWaiverResolution(ctx context.Context, q SimQueries, in ProcessWaiversInput, r claimResolution) (bool, error) {
	w := r.winner

	plan, err := planWaiverResolution(ctx, q, in, w)
	if err != nil {
		return false, err
	}
	if !plan.claimable {
		return false, markGroupLost(ctx, q, in, r)
	}
	if err := markGroupWon(ctx, q, in, r); err != nil {
		return false, err
	}

	reasoning := fmt.Sprintf("waiver claim resolution (claim %d)", w.ID)

	// Apply and log the drop only when the plan saw the player on the
	// roster — a vanished drop player must not produce a phantom drop.
	dropTxID := pgtype.Int8{}
	if plan.dropPresent {
		if err := applyWaiverDrop(ctx, q, in, w, reasoning); err != nil {
			return false, err
		}
		dropTxID = w.DropPlayerID
	}

	// Add the claimed player to the winner's roster (BN). Even though
	// the originating sim_transactions row is type='claim' (filed at
	// claim time), we ALSO log an `add` row at resolution time so the
	// agent's daily-transaction log shows what landed today.
	if err := q.InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
		PoolID:      in.PoolID,
		AgentID:     w.AgentID,
		PlayerID:    w.PlayerID,
		Slot:        string(SlotBN),
		AcquiredAt:  in.SimDate,
		AcquiredVia: string(AcquiredViaFreeAgent),
	}); err != nil {
		return false, fmt.Errorf("insert winner roster row: %w", err)
	}
	if _, err := q.InsertSimTransactionAdd(ctx, sqlcdb.InsertSimTransactionAddParams{
		PoolID:       in.PoolID,
		AgentID:      w.AgentID,
		Date:         in.SimDate,
		PlayerID:     pgtype.Int8{Int64: w.PlayerID, Valid: true},
		Reasoning:    reasoning,
		DropPlayerID: dropTxID,
	}); err != nil {
		return false, fmt.Errorf("insert winner add tx: %w", err)
	}
	return true, nil
}

// demoteWinners returns the new priority order with every winner
// moved to the tail in their pre-resolution priority sequence.
//
// Algorithm:
//  1. Mark winners in a set (dedup repeated agent_ids).
//  2. Walk the input priorities (already sorted by priority asc):
//     - non-winners go into "kept" preserving order
//     - winners go into "demoted" preserving order
//  3. Concatenate kept + demoted → restamp priority numbers 1..N.
//
// Result is the new full ordering; callers UPDATE row-by-row.
func demoteWinners(priorities []sqlcdb.SimWaiverPriority, winners []int32) []sqlcdb.SimWaiverPriority {
	winSet := make(map[int32]struct{}, len(winners))
	for _, w := range winners {
		winSet[w] = struct{}{}
	}

	// priorities is sorted ASC by priority number — walk in that order
	// to preserve relative order within each bucket.
	sorted := make([]sqlcdb.SimWaiverPriority, len(priorities))
	copy(sorted, priorities)
	slices.SortStableFunc(sorted, func(a, b sqlcdb.SimWaiverPriority) int {
		return cmp.Compare(a.Priority, b.Priority)
	})

	kept := make([]sqlcdb.SimWaiverPriority, 0, len(sorted))
	demoted := make([]sqlcdb.SimWaiverPriority, 0, len(winSet))
	for _, p := range sorted {
		if _, won := winSet[p.AgentID]; won {
			demoted = append(demoted, p)
		} else {
			kept = append(kept, p)
		}
	}

	out := make([]sqlcdb.SimWaiverPriority, 0, len(sorted))
	for i, p := range append(kept, demoted...) {
		p.Priority = int32(i + 1) // 1-indexed
		out = append(out, p)
	}
	return out
}

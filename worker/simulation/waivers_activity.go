package simulation

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// ProcessWaiversActivity — resolve every claim due today.
//
// Lifecycle:
//
//	1. ListSimWaiverClaimsDue(pool, today) — pending claims with
//	   process_date <= today. Empty → Skipped result; common when the
//	   day-loop iterates a calendar day with no waiver activity.
//	2. Group by player_id. Single-claim groups are uncontested and
//	   the lone claimant wins. Multi-claim groups are contested; the
//	   agent with the lowest priority NUMBER wins (PLAN.md > "Waivers"
//	   — Yahoo convention: priority 1 = first dibs).
//	3. Atomic commit (Transactor.InTx):
//	     for each (player, claims) group:
//	       - winner gets the player added to BN
//	       - winner's drop_player_id (if set) gets removed from roster
//	       - winning claim status → 'won', losing claims → 'lost'
//	       - sim_transactions logs an `add` (and optionally `drop`)
//	         for the winner so the daily transaction log reflects
//	         the resolved-claim outcome
//	     after all groups: re-rank waiver priority — winners move to
//	     the bottom in their original priority order; non-winners
//	     keep relative order, compacted up.
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
}

// ProcessWaivers is the Temporal-activity entry point.
//
// Returns nil error on the no-op path (no due claims). Only DB
// failures abort.
func (a *Activities) ProcessWaivers(ctx context.Context, in ProcessWaiversInput) (ProcessWaiversResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("ProcessWaivers start",
		"pool_id", in.PoolID,
		"sim_date", in.SimDate.Time,
	)

	// Group ALL pending claims per player whose earliest claim is due
	// today — cross-day claims on the same player resolve together so
	// waiver priority is honored and a later-dated claim can't sneak
	// through as an uncontested win after the player was already taken.
	claims, err := a.Queries.ListSimWaiverClaimsForDuePlayers(ctx, sqlcdb.ListSimWaiverClaimsForDuePlayersParams{
		PoolID:      in.PoolID,
		ProcessDate: in.SimDate,
	})
	if err != nil {
		return ProcessWaiversResult{}, fmt.Errorf("simulation: list waiver claims for due players: %w", err)
	}
	if len(claims) == 0 {
		logger.Debug("ProcessWaivers skipped — no due claims")
		return ProcessWaiversResult{Skipped: true}, nil
	}

	priorities, err := a.Queries.ListSimWaiverPriorityByPool(ctx, in.PoolID)
	if err != nil {
		return ProcessWaiversResult{}, fmt.Errorf("simulation: list waiver priority: %w", err)
	}

	// groupedClaims groups by player_id in deterministic player_id order
	// so the processing sequence is reproducible.
	groupedClaims := groupClaimsByPlayer(claims)

	var result ProcessWaiversResult
	err = a.Tx.InTx(ctx, func(q SimQueries) error {
		// currentPriorities tracks the live priority order as groups are
		// resolved. Each contested win rotates the winner to the bottom
		// before the next group resolves, so a priority-1 agent doesn't
		// win every contested player claimed on the same day — matching
		// the Yahoo convention cited in the migration notes.
		currentPriorities := clonePriorities(priorities)

		for _, group := range groupedClaims {
			r := resolveGroup(group, currentPriorities)

			won, err := applyWaiverResolution(ctx, q, in, r)
			if err != nil {
				return err
			}
			if won {
				result.ClaimsWon++
				result.ClaimsLost += len(r.losers)
				// Rotate the winner to the bottom immediately so the
				// next contested group sees the updated priority order.
				currentPriorities = demoteWinners(currentPriorities, []int32{r.winner.AgentID})
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

		// Write the final priority order to the DB. currentPriorities
		// now reflects all per-group rotations applied during this run.
		for _, p := range currentPriorities {
			if err := q.UpdateSimWaiverPriority(ctx, sqlcdb.UpdateSimWaiverPriorityParams{
				PoolID: in.PoolID, AgentID: p.AgentID, Priority: p.Priority,
			}); err != nil {
				return fmt.Errorf("update waiver priority for agent %d: %w", p.AgentID, err)
			}
		}
		return nil
	})
	if err != nil {
		return ProcessWaiversResult{}, err
	}

	logger.Debug("ProcessWaivers complete",
		"resolved", result.ClaimsResolved,
		"won", result.ClaimsWon,
		"lost", result.ClaimsLost,
		"contested", result.ContestedGroups,
	)
	return result, nil
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
	sort.Slice(playerOrder, func(i, j int) bool { return playerOrder[i] < playerOrder[j] })

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
func resolveGroup(group []sqlcdb.SimWaiverClaim, priorities []sqlcdb.SimWaiverPriority) claimResolution {
	priorityOf := make(map[int32]int32, len(priorities))
	for _, p := range priorities {
		priorityOf[p.AgentID] = p.Priority
	}

	sorted := make([]sqlcdb.SimWaiverClaim, len(group))
	copy(sorted, group)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi := priorityOf[sorted[i].AgentID]
		pj := priorityOf[sorted[j].AgentID]
		if pi != pj {
			return pi < pj
		}
		return sorted[i].ID < sorted[j].ID
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
// row in priorities is treated as priority MaxInt32 (lowest) — that's
// a defensive default for a misconfigured pool, not an expected case.
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
// Commit-time revalidation (the state validated at filing time can be
// stale at process_date):
//
//   - If the player is already on a roster in the pool, the add would
//     hit UNIQUE (pool_id, player_id) — resolve the whole group lost.
//   - The winner's designated drop may have vanished (left the roster
//     since filing). DeleteSimRosterRows reports rows-affected: 0 means
//     no drop happened, so no phantom drop tx row is written and the
//     add must fit on its own.
//   - Re-check roster capacity: if adding the player (after any valid
//     drop) would exceed RosterCapacity, resolve the group as lost.
//
// On a win, all other still-pending claims on the player (the losers in
// this group plus any cross-day claim not in the due window) are voided
// so they can't later resolve as phantom uncontested wins.
func applyWaiverResolution(ctx context.Context, q SimQueries, in ProcessWaiversInput, r claimResolution) (bool, error) {
	w := r.winner

	// Commit-time claimability: player already rostered anywhere in the
	// pool → nobody can win them. Resolve the whole group as lost.
	rostered, err := q.ExistsSimRosterPlayer(ctx, sqlcdb.ExistsSimRosterPlayerParams{
		PoolID: in.PoolID, PlayerID: w.PlayerID,
	})
	if err != nil {
		return false, fmt.Errorf("commit-time roster check (player %d): %w", w.PlayerID, err)
	}

	// Apply the winner's drop first (if still valid) so the capacity
	// check sees the freed spot. A vanished drop (0 rows) means no spot
	// is freed and no drop tx row is logged.
	dropApplied := false
	if !rostered && w.DropPlayerID.Valid {
		affected, err := q.DeleteSimRosterRows(ctx, sqlcdb.DeleteSimRosterRowsParams{
			PoolID: in.PoolID, AgentID: w.AgentID, PlayerID: w.DropPlayerID.Int64,
		})
		if err != nil {
			return false, fmt.Errorf("delete drop player roster row: %w", err)
		}
		dropApplied = affected > 0
	}

	// Capacity recheck: count the winner's current roster (post-drop).
	// If the add wouldn't fit, the group resolves as lost.
	overCapacity := false
	if !rostered {
		rosterRows, err := q.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{
			PoolID: in.PoolID, AgentID: w.AgentID,
		})
		if err != nil {
			return false, fmt.Errorf("list winner roster (agent %d): %w", w.AgentID, err)
		}
		if int32(len(rosterRows))+1 > in.RosterCapacity {
			overCapacity = true
		}
	}

	if rostered || overCapacity {
		// Nobody wins — mark every claim in the group lost.
		if err := markClaimLost(ctx, q, in, w.ID); err != nil {
			return false, err
		}
		for _, l := range r.losers {
			if err := markClaimLost(ctx, q, in, l.ID); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	// Winning claim → 'won'; in-group losers → 'lost'.
	if err := q.UpdateSimWaiverClaimStatus(ctx, sqlcdb.UpdateSimWaiverClaimStatusParams{
		ID: w.ID, Status: string(WaiverClaimStatusWon), ResolvedAt: in.SimDate,
	}); err != nil {
		return false, fmt.Errorf("mark claim %d won: %w", w.ID, err)
	}
	for _, l := range r.losers {
		if err := markClaimLost(ctx, q, in, l.ID); err != nil {
			return false, err
		}
	}
	// Void any OTHER still-pending claim on this player (e.g. a cross-day
	// claim whose process_date hasn't arrived) so it can't later resolve
	// as a phantom uncontested win against the now-rostered player.
	if err := q.CancelSimWaiverClaimsForPlayer(ctx, sqlcdb.CancelSimWaiverClaimsForPlayerParams{
		PoolID: in.PoolID, PlayerID: w.PlayerID, ResolvedAt: in.SimDate, ID: w.ID,
	}); err != nil {
		return false, fmt.Errorf("cancel other pending claims (player %d): %w", w.PlayerID, err)
	}

	reasoning := fmt.Sprintf("waiver claim resolution (claim %d)", w.ID)

	// Log the drop tx row only if a roster row was actually removed —
	// a vanished drop player must not produce a phantom drop.
	dropTxID := w.DropPlayerID
	if !dropApplied {
		dropTxID = pgtype.Int8{}
	}
	if dropApplied {
		if _, err := q.InsertSimTransactionDrop(ctx, sqlcdb.InsertSimTransactionDropParams{
			PoolID:    in.PoolID,
			AgentID:   w.AgentID,
			Date:      in.SimDate,
			PlayerID:  w.DropPlayerID,
			Reasoning: reasoning,
		}); err != nil {
			return false, fmt.Errorf("insert waiver-drop tx: %w", err)
		}
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

// markClaimLost flips a single claim to 'lost' at the resolution date.
func markClaimLost(ctx context.Context, q SimQueries, in ProcessWaiversInput, claimID int32) error {
	if err := q.UpdateSimWaiverClaimStatus(ctx, sqlcdb.UpdateSimWaiverClaimStatusParams{
		ID: claimID, Status: string(WaiverClaimStatusLost), ResolvedAt: in.SimDate,
	}); err != nil {
		return fmt.Errorf("mark claim %d lost: %w", claimID, err)
	}
	return nil
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
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })

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

package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// errWaiverClaimNotPending reports that a claim this resolution read as
// pending had already been resolved when its status update ran. Under
// LockSimPool that cannot happen, so it fails the transaction: rewriting
// the claim (won → lost) would contradict the roster another attempt
// committed.
var errWaiverClaimNotPending = errors.New("waiver claim is no longer pending")

// waiverPlan is the read-only commit-time verdict for one resolution,
// computed before any roster write so a rejected claim never leaves a
// partial mutation behind.
type waiverPlan struct {
	// claimable is false when the group must resolve as lost: the
	// player is already rostered in the pool, or the winner's bench
	// can't absorb the add even after a valid drop.
	claimable bool
	// dropPresent is true when the winner's designated drop player is
	// still on their roster, i.e. the drop will actually happen.
	dropPresent bool
}

// planWaiverResolution re-validates the state that was checked at filing
// time (it can be stale at process_date) without mutating anything:
//
//   - If the player is already on a roster in the pool, the add would
//     hit UNIQUE (pool_id, player_id) — resolve the whole group lost.
//   - The winner's designated drop may have vanished (left the roster
//     since filing). A vanished drop frees no spot and logs no drop tx.
//   - Prospective capacity: the winner's roster minus the drop (when
//     present) plus the claimed player in BN must pass
//     checkAcquisitionCapacity against the pool's per-slot limits — the
//     same policy add_player and claim_player apply at turn time.
func planWaiverResolution(ctx context.Context, q SimQueries, in ProcessWaiversInput, limits map[RosterSlot]int, w sqlcdb.SimWaiverClaim) (waiverPlan, error) {
	rostered, err := q.ExistsSimRosterPlayer(ctx, sqlcdb.ExistsSimRosterPlayerParams{
		PoolID: in.PoolID, PlayerID: w.PlayerID,
	})
	if err != nil {
		return waiverPlan{}, fmt.Errorf("commit-time roster check (player %d): %w", w.PlayerID, err)
	}
	if rostered {
		return waiverPlan{}, nil
	}

	rosterRows, err := q.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{
		PoolID: in.PoolID, AgentID: w.AgentID,
	})
	if err != nil {
		return waiverPlan{}, fmt.Errorf("list winner roster (agent %d): %w", w.AgentID, err)
	}
	roster := RosterState{Placements: make(map[int64]RosterSlot, len(rosterRows)), Limits: limits}
	for _, row := range rosterRows {
		roster.Placements[row.PlayerID] = RosterSlot(row.Slot)
	}

	var dropID *int64
	dropPresent := false
	if w.DropPlayerID.Valid {
		dropID = &w.DropPlayerID.Int64
		_, dropPresent = roster.Placements[*dropID]
	}
	return waiverPlan{
		claimable:   checkAcquisitionCapacity(roster, dropID) == nil,
		dropPresent: dropPresent,
	}, nil
}

// applyWaiverDrop removes the winner's designated drop player and logs
// the drop tx row through removeAndLogDrop, the same path as an
// explicit drop. planWaiverResolution already saw the row inside this
// transaction, so a delete that affects no rows means the roster changed
// underneath us and the capacity verdict is void — removeAndLogDrop
// fails the transaction rather than commit an add the roster may not fit.
func applyWaiverDrop(ctx context.Context, q SimQueries, in ProcessWaiversInput, w sqlcdb.SimWaiverClaim, reasoning string) error {
	if _, err := removeAndLogDrop(ctx, q, rosterDrop{
		PoolID: in.PoolID, AgentID: w.AgentID, Date: in.SimDate,
		PlayerID: w.DropPlayerID.Int64, Reasoning: reasoning,
	}); err != nil {
		return fmt.Errorf("waiver claim %d drop: %w", w.ID, err)
	}
	return nil
}

// markGroupWon flips the winning claim to 'won', the in-group losers to
// 'lost', and voids any OTHER still-pending claim on the player (e.g. a
// cross-day claim whose process_date hasn't arrived) so it can't later
// resolve as a phantom uncontested win against the now-rostered player.
func markGroupWon(ctx context.Context, q SimQueries, in ProcessWaiversInput, r claimResolution) error {
	w := r.winner
	if err := resolveClaim(ctx, q, in, w.ID, WaiverClaimStatusWon); err != nil {
		return err
	}
	for _, l := range r.losers {
		if err := resolveClaim(ctx, q, in, l.ID, WaiverClaimStatusLost); err != nil {
			return err
		}
	}
	if err := q.CancelSimWaiverClaimsForPlayer(ctx, sqlcdb.CancelSimWaiverClaimsForPlayerParams{
		PoolID: in.PoolID, PlayerID: w.PlayerID, ResolvedAt: in.SimDate, ID: w.ID,
	}); err != nil {
		return fmt.Errorf("cancel other pending claims (player %d): %w", w.PlayerID, err)
	}
	return nil
}

// markGroupLost resolves every claim in the group — the would-be winner
// and the in-group losers — as 'lost'. Used when the plan rejects the
// group; it performs no roster write.
func markGroupLost(ctx context.Context, q SimQueries, in ProcessWaiversInput, r claimResolution) error {
	if err := resolveClaim(ctx, q, in, r.winner.ID, WaiverClaimStatusLost); err != nil {
		return err
	}
	for _, l := range r.losers {
		if err := resolveClaim(ctx, q, in, l.ID, WaiverClaimStatusLost); err != nil {
			return err
		}
	}
	return nil
}

// resolveClaim moves a single pending claim to status at the resolution
// date. A claim that is no longer pending is not touched and fails the
// transaction with errWaiverClaimNotPending.
func resolveClaim(ctx context.Context, q SimQueries, in ProcessWaiversInput, claimID int32, status WaiverClaimStatus) error {
	affected, err := q.ResolveSimWaiverClaim(ctx, sqlcdb.ResolveSimWaiverClaimParams{
		ID: claimID, Status: string(status), ResolvedAt: in.SimDate,
	})
	if err != nil {
		return fmt.Errorf("mark claim %d %s: %w", claimID, status, err)
	}
	if affected == 0 {
		return fmt.Errorf("mark claim %d %s: %w", claimID, status, errWaiverClaimNotPending)
	}
	return nil
}

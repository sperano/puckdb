package simulation

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sperano/puckdb/sqlcdb"
)

// errWaiverDropVanished reports that a designated drop player the plan
// saw on the roster was gone by the time the delete ran. It surfaces a
// broken transactional invariant, not a normal "vanished since filing"
// case (which the plan handles by simply not dropping).
var errWaiverDropVanished = errors.New("drop player vanished from roster mid-transaction")

// waiverPlan is the read-only commit-time verdict for one resolution,
// computed before any roster write so a rejected claim never leaves a
// partial mutation behind.
type waiverPlan struct {
	// claimable is false when the group must resolve as lost: the
	// player is already rostered in the pool, or the winner's roster
	// can't absorb the add even after a valid drop.
	claimable bool
	// dropPresent is true when the winner's designated drop player is
	// still on their roster, i.e. the drop will actually free a spot.
	dropPresent bool
}

// planWaiverResolution re-validates the state that was checked at filing
// time (it can be stale at process_date) without mutating anything:
//
//   - If the player is already on a roster in the pool, the add would
//     hit UNIQUE (pool_id, player_id) — resolve the whole group lost.
//   - The winner's designated drop may have vanished (left the roster
//     since filing). A vanished drop frees no spot and logs no drop tx.
//   - Prospective capacity: current roster size, minus one if the drop
//     is present, plus the add must fit in RosterCapacity.
func planWaiverResolution(ctx context.Context, q SimQueries, in ProcessWaiversInput, w sqlcdb.SimWaiverClaim) (waiverPlan, error) {
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
	dropPresent := w.DropPlayerID.Valid && slices.ContainsFunc(rosterRows, func(row sqlcdb.SimRoster) bool {
		return row.PlayerID == w.DropPlayerID.Int64
	})

	prospective := int32(len(rosterRows)) + 1
	if dropPresent {
		prospective--
	}
	return waiverPlan{claimable: prospective <= in.RosterCapacity, dropPresent: dropPresent}, nil
}

// applyWaiverDrop removes the winner's designated drop player and logs
// the drop tx row. planWaiverResolution already saw the row inside this
// transaction, so a delete that affects no rows means the roster changed
// underneath us and the capacity verdict is void — fail the transaction
// rather than commit an add the roster may not fit.
func applyWaiverDrop(ctx context.Context, q SimQueries, in ProcessWaiversInput, w sqlcdb.SimWaiverClaim, reasoning string) error {
	affected, err := q.DeleteSimRosterRows(ctx, sqlcdb.DeleteSimRosterRowsParams{
		PoolID: in.PoolID, AgentID: w.AgentID, PlayerID: w.DropPlayerID.Int64,
	})
	if err != nil {
		return fmt.Errorf("delete drop player roster row: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("waiver claim %d (agent %d, drop player %d): %w",
			w.ID, w.AgentID, w.DropPlayerID.Int64, errWaiverDropVanished)
	}
	if _, err := q.InsertSimTransactionDrop(ctx, sqlcdb.InsertSimTransactionDropParams{
		PoolID:    in.PoolID,
		AgentID:   w.AgentID,
		Date:      in.SimDate,
		PlayerID:  w.DropPlayerID,
		Reasoning: reasoning,
	}); err != nil {
		return fmt.Errorf("insert waiver-drop tx: %w", err)
	}
	return nil
}

// markGroupWon flips the winning claim to 'won', the in-group losers to
// 'lost', and voids any OTHER still-pending claim on the player (e.g. a
// cross-day claim whose process_date hasn't arrived) so it can't later
// resolve as a phantom uncontested win against the now-rostered player.
func markGroupWon(ctx context.Context, q SimQueries, in ProcessWaiversInput, r claimResolution) error {
	w := r.winner
	if err := q.UpdateSimWaiverClaimStatus(ctx, sqlcdb.UpdateSimWaiverClaimStatusParams{
		ID: w.ID, Status: string(WaiverClaimStatusWon), ResolvedAt: in.SimDate,
	}); err != nil {
		return fmt.Errorf("mark claim %d won: %w", w.ID, err)
	}
	for _, l := range r.losers {
		if err := markClaimLost(ctx, q, in, l.ID); err != nil {
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
	if err := markClaimLost(ctx, q, in, r.winner.ID); err != nil {
		return err
	}
	for _, l := range r.losers {
		if err := markClaimLost(ctx, q, in, l.ID); err != nil {
			return err
		}
	}
	return nil
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

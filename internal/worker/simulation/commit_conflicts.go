package simulation

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

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
		reason, err := commitConflict(ctx, q, in, act.args, rejectedPlayers)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			kept = append(kept, act)
			continue
		}
		if add, isAdd := act.args.(AddPlayerArgs); isAdd {
			rejectedPlayers[add.PlayerID] = struct{}{}
		}
		markRejected(act.toolCaptureIndex, reason)
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

// commitConflict returns why act must be rejected at commit time, or ""
// to keep it. rejected holds the players whose add this commit already
// rejected, in action order:
//
//   - an add whose player another agent rostered first loses;
//   - an add that drops, or a drop_player that names, a rejected player
//     is rejected too: that player never joined the roster, so there is
//     nothing to remove, and removeAndLogDrop would fail the whole turn.
//     Rejecting the dependent add is the conservative choice: its
//     capacity check counted the drop.
func commitConflict(ctx context.Context, q SimQueries, in ManageRosterInput, args Action, rejected map[int64]struct{}) (string, error) {
	switch a := args.(type) {
	case AddPlayerArgs:
		if a.DropPlayerID != nil {
			if _, gone := rejected[*a.DropPlayerID]; gone {
				return fmt.Sprintf("add_player(%d): drop_player_id %d was never added (its add lost the commit-time recheck)",
					a.PlayerID, *a.DropPlayerID), nil
			}
		}
		exists, err := q.ExistsSimRosterPlayer(ctx, sqlcdb.ExistsSimRosterPlayerParams{
			PoolID:   in.PoolID,
			PlayerID: a.PlayerID,
		})
		if err != nil {
			return "", fmt.Errorf("commit-time roster check (player %d): %w", a.PlayerID, err)
		}
		if exists {
			return fmt.Sprintf("add_player(%d): player already on a roster (another agent won the add first)", a.PlayerID), nil
		}
	case DropPlayerArgs:
		if _, gone := rejected[a.PlayerID]; gone {
			return fmt.Sprintf("drop_player(%d): player was never added (its add lost the commit-time recheck)", a.PlayerID), nil
		}
	}
	return "", nil
}

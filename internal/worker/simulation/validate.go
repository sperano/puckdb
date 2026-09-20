package simulation

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// MaxNotesBytes mirrors the CHECK (octet_length(notes) <= 50000)
// constraint on sim_agents in the schema. Validating in the
// application layer too lets the activity surface a clean tool-call
// error to the LLM ("notes too long, retry shorter") rather than
// letting a Postgres error bubble up.
const MaxNotesBytes = 50_000

// ============================================================================
// Validation errors.
//
// Sentinels rather than fmt.Errorf strings so activities can branch on
// errors.Is(err, ErrXxx) when classifying tool_use_failure rows in
// sim_transactions. Wrapping with fmt.Errorf("...: %w", err) preserves
// the sentinel via errors.Is.
// ============================================================================

var (
	ErrPlayerNotOnRoster          = errors.New("simulation: player not on agent's roster")
	ErrPlayerNotFreeAgent         = errors.New("simulation: player is not a free agent")
	ErrPlayerNotOnWaivers         = errors.New("simulation: player is not on waivers")
	ErrRosterFullNeedsDrop        = errors.New("simulation: roster is full and no drop_player_id specified")
	ErrDropPlayerNotOnRoster      = errors.New("simulation: drop_player_id is not on the agent's roster")
	ErrSlotCapacityExceeded       = errors.New("simulation: slot capacity exceeded with no available displacement target")
	ErrSlotNotEligibleForPosition = errors.New("simulation: slot not eligible for player's position")
	ErrNotesTooLarge              = errors.New("simulation: notes exceed 50000 bytes")
	ErrDuplicatePendingClaim      = errors.New("simulation: agent already has a pending claim on this player")
)

// ============================================================================
// Validation inputs.
// ============================================================================

// RosterState is the current placement of one agent's roster, keyed by
// player_id. Players absent from Placements are not on the roster.
//
// Limits is the per-slot capacity from PoolConfig.RosterPositions.
// Validators read both maps without mutating them; the lineup
// resolver works on a defensive copy.
type RosterState struct {
	Placements map[int64]RosterSlot
	Limits     map[RosterSlot]int
}

// PoolFreeAgentState describes the league-wide availability that
// add_player and claim_player validate against.
type PoolFreeAgentState struct {
	// FreeAgents are players never owned in this pool, OR dropped
	// more than waiver_days ago. Pickable instantly via add_player.
	FreeAgents map[int64]struct{}
	// OnWaivers are recently-dropped players still in their waiver
	// window. Targetable via claim_player; resolved later by
	// ProcessWaiversActivity.
	OnWaivers map[int64]struct{}
}

// PlayerCatalog returns the NHL position for a player ID. Implemented
// by the activity layer over a sqlc query; tests use a small inline
// map. The error path covers "player not in DB" — which should never
// happen for IDs that came out of an FA/roster query, but we'd rather
// surface a clear error than panic.
type PlayerCatalog interface {
	Position(playerID int64) (sqlcdb.PlayerPosition, error)
}

// ============================================================================
// add_player / claim_player / drop_player / update_notes — stateless validators
// ============================================================================

// ValidateAddPlayer enforces the add_player invariants:
//   - args.PlayerID is a free agent (in fa.FreeAgents).
//   - The BN slot has remaining capacity (adds always land in BN). If BN is
//     full, args.DropPlayerID is required to vacate a slot first.
//   - args.DropPlayerID, when set, names a player on the agent's roster.
//
// Per PLAN.md "Free agents", waiver-window players are NOT free
// agents; the LLM must use claim_player for those. This validator
// rejects them rather than silently letting an add slip past — a real
// bug surfaces as a clean tool_use_failure.
func ValidateAddPlayer(args AddPlayerArgs, roster RosterState, fa PoolFreeAgentState) error {
	if _, isFA := fa.FreeAgents[args.PlayerID]; !isFA {
		return fmt.Errorf("add_player(%d): %w", args.PlayerID, ErrPlayerNotFreeAgent)
	}

	// Adds always land in BN. Enforce BN capacity directly rather than
	// total roster capacity: rosterFull() counts IR slots in the total,
	// so the bench can silently exceed its limit while total capacity
	// still shows headroom (when IR has vacancies).
	if benchFull(roster) {
		if args.DropPlayerID == nil {
			return fmt.Errorf("add_player(%d): %w", args.PlayerID, ErrRosterFullNeedsDrop)
		}
	}
	if args.DropPlayerID != nil {
		if _, owned := roster.Placements[*args.DropPlayerID]; !owned {
			return fmt.Errorf("add_player(%d) drop=%d: %w", args.PlayerID, *args.DropPlayerID, ErrDropPlayerNotOnRoster)
		}
	}
	return nil
}

// ValidateClaimPlayer enforces claim_player invariants. The drop is
// applied later (when the waiver resolves), so the "roster full"
// check considers the FUTURE state at claim resolution: full + 1
// without a drop is rejected.
//
// pendingClaims is the set of players the agent already has an open
// claim on; a second claim on the same player would violate
// ux_sim_waiver_claims_pending_one_per_agent_player and roll back the
// whole turn, so it's rejected here as a clean tool_use_failure.
func ValidateClaimPlayer(args ClaimPlayerArgs, roster RosterState, fa PoolFreeAgentState, pendingClaims map[int64]struct{}) error {
	if _, onWaivers := fa.OnWaivers[args.PlayerID]; !onWaivers {
		return fmt.Errorf("claim_player(%d): %w", args.PlayerID, ErrPlayerNotOnWaivers)
	}
	if _, dup := pendingClaims[args.PlayerID]; dup {
		return fmt.Errorf("claim_player(%d): %w", args.PlayerID, ErrDuplicatePendingClaim)
	}
	if rosterFull(roster) && args.DropPlayerID == nil {
		return fmt.Errorf("claim_player(%d): %w", args.PlayerID, ErrRosterFullNeedsDrop)
	}
	if args.DropPlayerID != nil {
		if _, owned := roster.Placements[*args.DropPlayerID]; !owned {
			return fmt.Errorf("claim_player(%d) drop=%d: %w", args.PlayerID, *args.DropPlayerID, ErrDropPlayerNotOnRoster)
		}
	}
	return nil
}

// ValidateDropPlayer enforces drop_player invariants: the player must
// currently be on the agent's roster.
func ValidateDropPlayer(args DropPlayerArgs, roster RosterState) error {
	if _, owned := roster.Placements[args.PlayerID]; !owned {
		return fmt.Errorf("drop_player(%d): %w", args.PlayerID, ErrPlayerNotOnRoster)
	}
	return nil
}

// ValidateUpdateNotes rejects oversized writes early so the agent can
// retry with shorter notes within the same tool-use round, rather
// than waiting for the DB CHECK constraint to fire.
func ValidateUpdateNotes(args UpdateNotesArgs) error {
	if len(args.Notes) > MaxNotesBytes {
		return fmt.Errorf("update_notes (%d bytes > %d): %w", len(args.Notes), MaxNotesBytes, ErrNotesTooLarge)
	}
	return nil
}

// rosterFull reports whether the agent's roster has no remaining
// capacity across ALL slot types — i.e., every player slot defined in
// Limits is filled.
func rosterFull(r RosterState) bool {
	total := 0
	for _, n := range r.Limits {
		total += n
	}
	return len(r.Placements) >= total
}

// benchFull reports whether the BN slot has reached its configured
// limit. Adds always land in BN, so this is the correct capacity gate
// for add_player (rosterFull counts IR slots in the total which allows
// the bench to permanently exceed its own cap when IR has vacancies).
func benchFull(r RosterState) bool {
	limit, ok := r.Limits[SlotBN]
	if !ok {
		return false // no BN limit configured → unlimited
	}
	count := 0
	for _, s := range r.Placements {
		if s == SlotBN {
			count++
		}
	}
	return count >= limit
}

// ============================================================================
// set_lineup — stateful resolver with auto-displacement
// ============================================================================

// ResolvedLineupMove is one row destined for sim_lineup_moves. A single
// input move produces one row when the target slot has free capacity,
// and two rows when displacement fires (one for the explicit move,
// one for the displaced player's transition to BN).
//
// Sequence preserves the LLM's array order from set_lineup.moves and
// is written verbatim into sim_lineup_moves.sequence.
type ResolvedLineupMove struct {
	Sequence          int
	PlayerID          int64
	FromSlot          RosterSlot
	ToSlot            RosterSlot
	DisplacedPlayerID int64 // 0 when no displacement was needed
}

// ValidateAndResolveLineup applies set_lineup moves in array order,
// returning the resolved per-move slot transitions for sim_lineup_moves
// or the first validation error.
//
// Behavior:
//   - Each move's PlayerID must be on the roster.
//   - Each move's Slot must be eligible for the player's NHL position
//     (uses IsEligibleSlot — the 5-position table from PLAN.md).
//   - If the player is already in the target slot, the move is a no-op
//     (no row emitted, no displacement).
//   - If the target slot has free capacity, the player moves in.
//   - If the target slot is at capacity, the lowest-player_id occupant
//     is displaced to BN. If BN itself is full, the move errors —
//     the LLM should drop a player first to free a BN slot.
//
// The resolver mutates a defensive copy of roster.Placements; the
// caller's map is not modified.
func ValidateAndResolveLineup(args SetLineupArgs, roster RosterState, catalog PlayerCatalog) ([]ResolvedLineupMove, error) {
	working := maps.Clone(roster.Placements)
	if working == nil {
		working = make(map[int64]RosterSlot)
	}

	// batchPlaced holds every player whose slot was assigned by an
	// EARLIER move in this batch (movers and their displaced collateral).
	// A full target slot must be freed by evicting an ORIGINAL occupant;
	// evicting a batch-placed player would silently undo an earlier move
	// (move[0]→D then move[1]→D-when-full must not push move[0]'s player
	// back to BN). pickDisplacement refuses batch-placed occupants and the
	// move is rejected via ErrSlotCapacityExceeded instead.
	batchPlaced := make(map[int64]struct{})

	resolved := make([]ResolvedLineupMove, 0, len(args.Moves))

	for i, mv := range args.Moves {
		fromSlot, owned := working[mv.PlayerID]
		if !owned {
			return nil, fmt.Errorf("set_lineup[%d] player=%d: %w", i, mv.PlayerID, ErrPlayerNotOnRoster)
		}

		// No-op: the player is already in the target slot. Don't
		// emit a row; don't trigger displacement.
		if fromSlot == mv.Slot {
			continue
		}

		// Slot eligibility check.
		pos, err := catalog.Position(mv.PlayerID)
		if err != nil {
			return nil, fmt.Errorf("set_lineup[%d] player=%d: position lookup: %w", i, mv.PlayerID, err)
		}
		ok, err := IsEligibleSlot(pos, mv.Slot)
		if err != nil {
			return nil, fmt.Errorf("set_lineup[%d] player=%d: %w", i, mv.PlayerID, err)
		}
		if !ok {
			return nil, fmt.Errorf("set_lineup[%d] player=%d position=%s slot=%s: %w",
				i, mv.PlayerID, pos, mv.Slot, ErrSlotNotEligibleForPosition)
		}

		// Capacity check on the target slot. Count current occupants
		// of mv.Slot — excluding the moving player (who isn't there
		// yet, but might be there in the no-op branch above).
		var displaced int64
		if needsDisplacement(working, mv.Slot, roster.Limits) {
			displaced = pickDisplacement(working, mv.Slot, batchPlaced)
			if displaced == 0 {
				return nil, fmt.Errorf("set_lineup[%d] player=%d slot=%s: %w",
					i, mv.PlayerID, mv.Slot, ErrSlotCapacityExceeded)
			}
			// Verify BN has room for the displaced player. When the
			// mover is coming FROM BN, they are vacating a BN spot at
			// the same moment the displaced player occupies one — the
			// net change is zero, so the BN-full check must exclude the
			// moving player's current BN slot from the occupancy count.
			if needsDisplacementExcluding(working, SlotBN, roster.Limits, mv.PlayerID) {
				return nil, fmt.Errorf("set_lineup[%d] player=%d slot=%s: BN is full, %w",
					i, mv.PlayerID, mv.Slot, ErrSlotCapacityExceeded)
			}
		}

		// Apply.
		if displaced != 0 {
			working[displaced] = SlotBN
			batchPlaced[displaced] = struct{}{}
			resolved = append(resolved, ResolvedLineupMove{
				Sequence:          i,
				PlayerID:          mv.PlayerID,
				FromSlot:          fromSlot,
				ToSlot:            mv.Slot,
				DisplacedPlayerID: displaced,
			})
		} else {
			resolved = append(resolved, ResolvedLineupMove{
				Sequence: i,
				PlayerID: mv.PlayerID,
				FromSlot: fromSlot,
				ToSlot:   mv.Slot,
			})
		}
		working[mv.PlayerID] = mv.Slot
		batchPlaced[mv.PlayerID] = struct{}{}
	}

	return resolved, nil
}

// needsDisplacement reports whether placing one more player into slot
// would exceed its limit. A slot with no entry in Limits is treated as
// unlimited (returns false) — matches the semantics of "if you didn't
// configure a cap, there is no cap."
func needsDisplacement(placements map[int64]RosterSlot, slot RosterSlot, limits map[RosterSlot]int) bool {
	return needsDisplacementExcluding(placements, slot, limits, 0)
}

// needsDisplacementExcluding is needsDisplacement with one player
// excluded from the occupancy count. Pass excludeID=0 to skip
// exclusion (same behavior as needsDisplacement). Used by the BN-full
// check when the mover is FROM BN: they vacate a BN spot in the same
// logical step that displacement fills one, so they must not be
// counted as a current occupant for the purpose of the overflow check.
func needsDisplacementExcluding(placements map[int64]RosterSlot, slot RosterSlot, limits map[RosterSlot]int, excludeID int64) bool {
	limit, ok := limits[slot]
	if !ok {
		return false
	}
	count := 0
	for id, s := range placements {
		if s == slot && id != excludeID {
			count++
		}
	}
	return count >= limit
}

// pickDisplacement chooses which player to evict from a full slot.
// V1 rule: lowest player_id among the slot's ORIGINAL occupants —
// players NOT placed into the slot by an earlier move in the same
// batch (batchPlaced). Displacing a batch-placed player would silently
// undo that earlier move, so those are never candidates; when every
// occupant is batch-placed the function returns 0 and the caller
// surfaces ErrSlotCapacityExceeded. Also returns 0 when the slot has no
// occupants at all.
//
// Lowest-ID is deterministic and invariant under re-runs, which matters
// for Temporal idempotency: the same lineup-set call from a retry
// produces the same displaced row in sim_lineup_moves.
//
// A future improvement might be "displace least-recently-rostered"
// or "displace player with the worst recent stats", but those need
// state we don't have at this layer; bringing them in would couple
// validation to game-state logic. Lowest-ID is the right V1 default.
func pickDisplacement(placements map[int64]RosterSlot, slot RosterSlot, batchPlaced map[int64]struct{}) int64 {
	var original []int64
	for id, s := range placements {
		if s != slot {
			continue
		}
		if _, placed := batchPlaced[id]; placed {
			continue
		}
		original = append(original, id)
	}
	if len(original) == 0 {
		return 0
	}
	slices.Sort(original)
	return original[0]
}

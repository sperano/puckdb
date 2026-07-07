package simulation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Test fixtures
// ============================================================================

// stubCatalog implements PlayerCatalog from a static map. Position
// lookups for IDs not in the map error; tests pin that behavior since
// real activities will surface "player not in DB" as a tool_use_failure
// rather than a panic.
type stubCatalog map[int64]sqlcdb.PlayerPosition

func (c stubCatalog) Position(playerID int64) (sqlcdb.PlayerPosition, error) {
	p, ok := c[playerID]
	if !ok {
		return "", fmt.Errorf("test catalog: no entry for player %d", playerID)
	}
	return p, nil
}

// defaultLimits returns the V1 roster sizes from PLAN.md > "Roster Slots".
func defaultLimits() map[RosterSlot]int {
	return map[RosterSlot]int{
		SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2, SlotUtil: 1,
		SlotBN: 6, SlotIR: 3,
	}
}

// fullRoster returns a 21-player roster with every slot at capacity,
// matching the V1 roster-positions config. Used to exercise the
// "roster full" branch of add_player / claim_player.
func fullRoster() RosterState {
	limits := defaultLimits()
	placements := map[int64]RosterSlot{
		1: SlotC, 2: SlotC,
		3: SlotLW, 4: SlotLW,
		5: SlotRW, 6: SlotRW,
		7: SlotD, 8: SlotD, 9: SlotD,
		10: SlotG, 11: SlotG,
		12: SlotUtil,
		13: SlotBN, 14: SlotBN, 15: SlotBN, 16: SlotBN, 17: SlotBN, 18: SlotBN,
		19: SlotIR, 20: SlotIR, 21: SlotIR,
	}
	return RosterState{Placements: placements, Limits: limits}
}

func intp(v int64) *int64 { return &v }

// ============================================================================
// ValidateAddPlayer
// ============================================================================

func TestValidateAddPlayer_Success_FreeAgentRosterHasRoom(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC},
		Limits:     defaultLimits(),
	}
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{42: {}}}
	require.NoError(t, ValidateAddPlayer(AddPlayerArgs{PlayerID: 42}, roster, fa))
}

func TestValidateAddPlayer_RejectsNonFreeAgent(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{}, Limits: defaultLimits()}
	fa := PoolFreeAgentState{
		FreeAgents: map[int64]struct{}{},
		OnWaivers:  map[int64]struct{}{42: {}}, // on waivers, not a free agent
	}
	err := ValidateAddPlayer(AddPlayerArgs{PlayerID: 42}, roster, fa)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlayerNotFreeAgent,
		"a player on waivers must NOT be addable via add_player — that's claim_player's job")
}

func TestValidateAddPlayer_FullRosterRejectsMissingDrop(t *testing.T) {
	roster := fullRoster()
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{99: {}}}
	err := ValidateAddPlayer(AddPlayerArgs{PlayerID: 99}, roster, fa)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRosterFullNeedsDrop)
}

func TestValidateAddPlayer_FullRoster_AcceptsValidDrop(t *testing.T) {
	roster := fullRoster()
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{99: {}}}
	require.NoError(t, ValidateAddPlayer(
		AddPlayerArgs{PlayerID: 99, DropPlayerID: intp(13)},
		roster, fa,
	))
}

func TestValidateAddPlayer_DropPlayerNotOnRosterRejected(t *testing.T) {
	roster := fullRoster()
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{99: {}}}
	err := ValidateAddPlayer(
		AddPlayerArgs{PlayerID: 99, DropPlayerID: intp(7777)}, // not rostered
		roster, fa,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDropPlayerNotOnRoster)
}

// Finding #8 aggravator: adds always land in BN. A roster with full BN but
// empty IR slots has total-roster headroom, yet an add must still require a
// drop because the add lands in BN (not IR). The old rosterFull check would
// have accepted this incorrectly; benchFull enforces the right per-slot cap.
func TestValidateAddPlayer_BenchFullIRVacant_RequiresDrop(t *testing.T) {
	// BN at 6/6, IR at 1/3 (2 vacancies), active slots partially full.
	// Total roster = 6+1 + some actives < total capacity, so rosterFull
	// would have returned false — but the bench is full, so an add needs a drop.
	limits := defaultLimits() // BN cap = 6
	placements := map[int64]RosterSlot{
		1: SlotC,
		13: SlotBN, 14: SlotBN, 15: SlotBN, 16: SlotBN, 17: SlotBN, 18: SlotBN, // BN full
		19: SlotIR, // IR has 2 vacancies
	}
	roster := RosterState{Placements: placements, Limits: limits}
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{99: {}}}

	err := ValidateAddPlayer(AddPlayerArgs{PlayerID: 99}, roster, fa)
	require.Error(t, err, "add must be rejected when BN is full, even if total roster has IR vacancies")
	assert.ErrorIs(t, err, ErrRosterFullNeedsDrop)
}

// Finding #8: add is accepted when BN has room, regardless of IR/active fill.
func TestValidateAddPlayer_BenchHasRoom_AcceptsNoDrop(t *testing.T) {
	limits := defaultLimits() // BN cap = 6
	placements := map[int64]RosterSlot{
		1: SlotC, 2: SlotC,
		13: SlotBN, 14: SlotBN, 15: SlotBN, 16: SlotBN, 17: SlotBN, // BN at 5/6
		19: SlotIR, 20: SlotIR, 21: SlotIR,                          // IR full
	}
	roster := RosterState{Placements: placements, Limits: limits}
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{99: {}}}

	require.NoError(t, ValidateAddPlayer(AddPlayerArgs{PlayerID: 99}, roster, fa),
		"add must succeed when BN has one vacancy")
}

// ============================================================================
// ValidateClaimPlayer
// ============================================================================

func TestValidateClaimPlayer_Success(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotC}, Limits: defaultLimits()}
	fa := PoolFreeAgentState{OnWaivers: map[int64]struct{}{42: {}}}
	require.NoError(t, ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: 42}, roster, fa, nil))
}

func TestValidateClaimPlayer_RejectsFreeAgent(t *testing.T) {
	// A free agent should be added via add_player, not claimed via
	// claim_player. Pin the symmetric rejection.
	roster := RosterState{Placements: map[int64]RosterSlot{}, Limits: defaultLimits()}
	fa := PoolFreeAgentState{
		FreeAgents: map[int64]struct{}{42: {}},
		OnWaivers:  map[int64]struct{}{},
	}
	err := ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: 42}, roster, fa, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlayerNotOnWaivers)
}

func TestValidateClaimPlayer_FullRosterNeedsDrop(t *testing.T) {
	roster := fullRoster()
	fa := PoolFreeAgentState{OnWaivers: map[int64]struct{}{99: {}}}
	err := ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: 99}, roster, fa, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRosterFullNeedsDrop)
}

func TestValidateClaimPlayer_RejectsDuplicatePendingClaim(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotC}, Limits: defaultLimits()}
	fa := PoolFreeAgentState{OnWaivers: map[int64]struct{}{42: {}}}
	pending := map[int64]struct{}{42: {}}
	err := ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: 42}, roster, fa, pending)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicatePendingClaim)
}

// ============================================================================
// ValidateDropPlayer
// ============================================================================

func TestValidateDropPlayer_Success(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{42: SlotBN}, Limits: defaultLimits()}
	require.NoError(t, ValidateDropPlayer(DropPlayerArgs{PlayerID: 42}, roster))
}

func TestValidateDropPlayer_NotOnRoster(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotC}, Limits: defaultLimits()}
	err := ValidateDropPlayer(DropPlayerArgs{PlayerID: 9999}, roster)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlayerNotOnRoster)
}

// ============================================================================
// ValidateUpdateNotes
// ============================================================================

func TestValidateUpdateNotes_BoundaryCases(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"empty", 0, false},
		{"under cap", 49_999, false},
		{"exactly at cap", 50_000, false},
		{"one over cap", 50_001, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notes := make([]byte, tc.size)
			for i := range notes {
				notes[i] = 'a'
			}
			err := ValidateUpdateNotes(UpdateNotesArgs{Notes: string(notes)})
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrNotesTooLarge)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Multibyte characters: the cap is BYTES, not runes. Pin so a future
// "use utf8.RuneCountInString" change is an explicit decision rather
// than a silent loosening of the constraint (which would diverge from
// the DB CHECK octet_length(...) <= 50000).
func TestValidateUpdateNotes_BytesNotRunes(t *testing.T) {
	// Each "é" is 2 bytes in UTF-8. 25001 of them = 50002 bytes,
	// which is over the cap, but only 25001 runes.
	notes := ""
	for range 25_001 {
		notes += "é"
	}
	require.Greater(t, len(notes), MaxNotesBytes)
	err := ValidateUpdateNotes(UpdateNotesArgs{Notes: notes})
	require.Error(t, err, "cap is octet_length, not rune count — multibyte content over the byte cap must reject")
	assert.ErrorIs(t, err, ErrNotesTooLarge)
}

// ============================================================================
// ValidateAndResolveLineup
// ============================================================================

func TestValidateAndResolveLineup_NoOpWhenAlreadyInTargetSlot(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC}

	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 1, Slot: SlotC}}},
		roster, catalog,
	)
	require.NoError(t, err)
	assert.Empty(t, resolved, "moving a player to its current slot must produce no rows")
}

func TestValidateAndResolveLineup_PlayerNotOnRoster(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotBN}, Limits: defaultLimits()}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC}

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 9999, Slot: SlotC}}},
		roster, catalog,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlayerNotOnRoster)
}

// Position eligibility: a goalie can't fill Util (per the 5-position
// table). EligibleSlots returns the spec; this test pins that the
// validator actually consults it.
func TestValidateAndResolveLineup_GoalieIntoUtilRejected(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotG}, Limits: defaultLimits()}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionG}

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 1, Slot: SlotUtil}}},
		roster, catalog,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSlotNotEligibleForPosition)
}

// Free capacity → simple placement, no displacement.
func TestValidateAndResolveLineup_FreeCapacityNoDisplacement(t *testing.T) {
	// C-slot has capacity 2, currently holding only player 1. Move
	// player 2 (currently on BN) into C — should land cleanly.
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC, 2: SlotBN},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC}

	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 2, Slot: SlotC}}},
		roster, catalog,
	)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, ResolvedLineupMove{
		Sequence: 0, PlayerID: 2, FromSlot: SlotBN, ToSlot: SlotC,
	}, resolved[0])
}

// Displacement: target slot at capacity, lowest-ID occupant goes to BN.
func TestValidateAndResolveLineup_DisplacementToBN(t *testing.T) {
	// C-slot is full ([1, 2]); move benched player 3 in.
	// Lowest-ID rule → player 1 is displaced.
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC, 2: SlotC, 3: SlotBN},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{
		1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC, 3: sqlcdb.PlayerPositionC,
	}

	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 3, Slot: SlotC}}},
		roster, catalog,
	)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, ResolvedLineupMove{
		Sequence: 0, PlayerID: 3, FromSlot: SlotBN, ToSlot: SlotC, DisplacedPlayerID: 1,
	}, resolved[0])
}

// Lowest-ID displacement is deterministic regardless of map iteration
// order. Pin it explicitly — go map iteration is randomized, so a
// less-careful "first player found" rule would silently yield
// different displaced rows on Temporal retries.
func TestValidateAndResolveLineup_DisplacementIsDeterministic(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{
			500: SlotC, 100: SlotC, // 100 is lowest, must be the one displaced
			999: SlotBN,
		},
		Limits: defaultLimits(),
	}
	catalog := stubCatalog{
		100: sqlcdb.PlayerPositionC, 500: sqlcdb.PlayerPositionC, 999: sqlcdb.PlayerPositionC,
	}

	for range 25 { // map randomness — run many times
		resolved, err := ValidateAndResolveLineup(
			SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 999, Slot: SlotC}}},
			roster, catalog,
		)
		require.NoError(t, err)
		require.Len(t, resolved, 1)
		assert.Equal(t, int64(100), resolved[0].DisplacedPlayerID,
			"lowest-ID rule must be stable under map iteration randomness")
	}
}

// Sequential application: PLAN.md's "swap via displacement" pattern.
// Setup uses two C-position players sharing C-slot + Util-slot
// because Util is the only single-capacity active slot, so it
// exercises the unambiguous "single occupant displaced" path the
// PLAN's example assumes.
func TestValidateAndResolveLineup_SwapViaSequentialMoves(t *testing.T) {
	// Player 1 in C (capacity 2; lone occupant), player 2 in Util
	// (capacity 1). Both are C position, so both are eligible for
	// C and Util. Swap by:
	//   move 0: player 1 → Util (displaces 2 → BN)
	//   move 1: player 2 → C    (BN → C, free capacity)
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC, 2: SlotUtil},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC}

	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{
			{PlayerID: 1, Slot: SlotUtil},
			{PlayerID: 2, Slot: SlotC},
		}},
		roster, catalog,
	)
	require.NoError(t, err)
	require.Len(t, resolved, 2, "swap-via-displacement must emit one row per move")

	assert.Equal(t, ResolvedLineupMove{
		Sequence: 0, PlayerID: 1, FromSlot: SlotC, ToSlot: SlotUtil, DisplacedPlayerID: 2,
	}, resolved[0], "move 0: 1 → Util displaces 2 to BN")
	assert.Equal(t, ResolvedLineupMove{
		Sequence: 1, PlayerID: 2, FromSlot: SlotBN, ToSlot: SlotC,
	}, resolved[1], "move 1: 2 → C lands cleanly because move 0 freed capacity")
}

// Position eligibility under the 5-position table: a C-position
// player CANNOT fill an LW slot. Pin the rejection and the wrapped
// sentinel so a future "F position resurfaces" change is loud.
func TestValidateAndResolveLineup_PositionMismatchRejected(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotC},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC}

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 1, Slot: SlotLW}}},
		roster, catalog,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSlotNotEligibleForPosition)
}

func TestValidateAndResolveLineup_MultipleMovesAllResolve(t *testing.T) {
	// Player 1 (BN, C) → C. Player 2 (BN, LW) → LW.
	// Both target slots have capacity; both moves emit exactly one row.
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotBN, 2: SlotBN},
		Limits:     defaultLimits(),
	}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionLW}

	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{
			{PlayerID: 1, Slot: SlotC},
			{PlayerID: 2, Slot: SlotLW},
		}},
		roster, catalog,
	)
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	assert.Equal(t, 0, resolved[0].Sequence)
	assert.Equal(t, 1, resolved[1].Sequence)
}

// BN↔active swap with full BN must succeed: mover vacates a BN slot
// at the same moment the displaced player occupies one — net BN count
// is unchanged, so the move is valid even when BN is at its limit.
// (Finding #8: pre-fix, this was wrongly rejected as "BN is full".)
func TestValidateAndResolveLineup_BNFull_BNToActiveSwapSucceeds(t *testing.T) {
	limits := defaultLimits()
	placements := map[int64]RosterSlot{
		1: SlotC, 2: SlotC,
		13: SlotBN, 14: SlotBN, 15: SlotBN, 16: SlotBN, 17: SlotBN, 18: SlotBN,
	}
	roster := RosterState{Placements: placements, Limits: limits}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC, 13: sqlcdb.PlayerPositionC}

	// Player 13 (BN) → C: displaces player 1 (lowest-ID C occupant) → BN.
	// Net BN change: -1 (13 leaves) +1 (1 arrives) = 0. Must succeed.
	resolved, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 13, Slot: SlotC}}},
		roster, catalog,
	)
	require.NoError(t, err, "BN↔active swap must succeed even when BN is at its limit")
	require.Len(t, resolved, 1)
	assert.Equal(t, int64(13), resolved[0].PlayerID)
	assert.Equal(t, int64(1), resolved[0].DisplacedPlayerID, "lowest-ID C occupant must be displaced")
}

// BN full, active-slot displacement triggered by a player NOT currently
// on BN — the displaced occupant needs a free BN slot and none exists.
// This remains an error because the mover (non-BN) doesn't vacate a
// BN slot, so BN would overflow after the displacement.
func TestValidateAndResolveLineup_BNFull_NonBNDisplacementFails(t *testing.T) {
	limits := defaultLimits()
	// Player 3 is in D (not BN). D has capacity 3 but only 2 occupants — free.
	// C is full (2/2). BN is full (6/6). Move player 3 (D) into C.
	// Displacement: player 1 (lowest-ID C) goes to BN. But BN is full and
	// player 3 is NOT vacating a BN slot → overflow. Must fail.
	placements := map[int64]RosterSlot{
		1: SlotC, 2: SlotC,
		3: SlotD, 4: SlotD,
		13: SlotBN, 14: SlotBN, 15: SlotBN, 16: SlotBN, 17: SlotBN, 18: SlotBN,
	}
	roster := RosterState{Placements: placements, Limits: limits}
	catalog := stubCatalog{
		1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC, 3: sqlcdb.PlayerPositionC,
	}

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 3, Slot: SlotC}}},
		roster, catalog,
	)
	require.Error(t, err, "non-BN-origin displacement with full BN must still fail")
	assert.ErrorIs(t, err, ErrSlotCapacityExceeded)
}

// Defensive copy: caller's roster.Placements is not mutated.
func TestValidateAndResolveLineup_DoesNotMutateInputRoster(t *testing.T) {
	original := map[int64]RosterSlot{1: SlotBN, 2: SlotC}
	roster := RosterState{Placements: original, Limits: defaultLimits()}
	catalog := stubCatalog{1: sqlcdb.PlayerPositionC, 2: sqlcdb.PlayerPositionC}

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 1, Slot: SlotC}}},
		roster, catalog,
	)
	require.NoError(t, err)
	assert.Equal(t, SlotBN, original[1], "input roster must not be mutated — caller owns the map")
	assert.Equal(t, SlotC, original[2])
}

// Catalog lookup error path: surface the underlying error rather
// than panic. Real activities will translate "player not in DB" into
// a clear tool_use_failure transaction row.
func TestValidateAndResolveLineup_CatalogErrorSurfaces(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{1: SlotBN}, Limits: defaultLimits()}
	emptyCatalog := stubCatalog{} // no entry for player 1

	_, err := ValidateAndResolveLineup(
		SetLineupArgs{Moves: []LineupMoveArg{{PlayerID: 1, Slot: SlotC}}},
		roster, emptyCatalog,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position lookup")
}

// Cross-validator check: errors.Is sees through fmt.Errorf wrapping
// for every sentinel. Activities will branch on errors.Is to
// classify tool_use_failure rows; pin every sentinel/wrapper pair.
func TestSentinels_ArePreservedThroughWrapping(t *testing.T) {
	roster := RosterState{Placements: map[int64]RosterSlot{}, Limits: defaultLimits()}
	emptyFA := PoolFreeAgentState{}

	checks := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"add_player no FA", ValidateAddPlayer(AddPlayerArgs{PlayerID: 1}, roster, emptyFA), ErrPlayerNotFreeAgent},
		{"claim_player not on waivers", ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: 1}, roster, emptyFA, nil), ErrPlayerNotOnWaivers},
		{"drop_player not on roster", ValidateDropPlayer(DropPlayerArgs{PlayerID: 1}, roster), ErrPlayerNotOnRoster},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, c.err)
			assert.True(t, errors.Is(c.err, c.sentinel),
				"errors.Is(%v, %v) must hold for activity classification", c.err, c.sentinel)
		})
	}
}

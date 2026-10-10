package simulation

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

// Replacement capacity — one policy for add_player, claim_player and
// waiver resolution (checkAcquisitionCapacity). The same table runs
// through all three (waivers_plan_test.go runs it through
// planWaiverResolution).

// replacementLimits is a tight pool: one active slot, one bench slot and
// one IR slot, so a single BN player fills the bench while total roster
// capacity still shows headroom.
func replacementLimits() map[RosterSlot]int {
	return map[RosterSlot]int{SlotC: 1, SlotBN: 1, SlotIR: 1}
}

// replacementCandidate is the free agent / waiver player each case tries
// to acquire.
const replacementCandidate int64 = 50

// replacementMissingDrop is never on any case's roster.
const replacementMissingDrop int64 = 99

type replacementCase struct {
	name       string
	placements map[int64]RosterSlot
	drop       *int64
	// wantErr is what add_player and claim_player return.
	wantErr error
	// waiverClaimable is the waiver plan's verdict: a drop that is not on
	// the roster there is a drop that vanished since filing, so it frees
	// nothing rather than failing validation.
	waiverClaimable bool
}

func replacementCases() []replacementCase {
	return []replacementCase{
		{
			name:       "full bench, active-slot drop leaves bench full",
			placements: map[int64]RosterSlot{1: SlotBN, 2: SlotC},
			drop:       intp(2),
			wantErr:    ErrDropLeavesBenchFull,
		},
		{
			name:            "full bench, bench drop makes room",
			placements:      map[int64]RosterSlot{1: SlotBN, 2: SlotC},
			drop:            intp(1),
			waiverClaimable: true,
		},
		{
			name:       "full bench, IR drop leaves bench full",
			placements: map[int64]RosterSlot{1: SlotBN, 3: SlotIR},
			drop:       intp(3),
			wantErr:    ErrDropLeavesBenchFull,
		},
		{
			name:       "full bench with empty IR and active headroom needs a drop",
			placements: map[int64]RosterSlot{1: SlotBN},
			wantErr:    ErrRosterFullNeedsDrop,
		},
		{
			name:       "full bench, drop not on roster",
			placements: map[int64]RosterSlot{1: SlotBN, 2: SlotC},
			drop:       intp(replacementMissingDrop),
			wantErr:    ErrDropPlayerNotOnRoster,
		},
		{
			name:            "bench has room, drop not on roster",
			placements:      map[int64]RosterSlot{2: SlotC},
			drop:            intp(replacementMissingDrop),
			wantErr:         ErrDropPlayerNotOnRoster,
			waiverClaimable: true,
		},
		{
			name:       "already overfull bench, one bench drop is not enough",
			placements: map[int64]RosterSlot{1: SlotBN, 4: SlotBN, 2: SlotC},
			drop:       intp(1),
			wantErr:    ErrDropLeavesBenchFull,
		},
		{
			name:       "already overfull bench, no drop",
			placements: map[int64]RosterSlot{1: SlotBN, 4: SlotBN},
			wantErr:    ErrRosterFullNeedsDrop,
		},
		{
			name:            "bench has room, no drop",
			placements:      map[int64]RosterSlot{2: SlotC},
			waiverClaimable: true,
		},
		{
			name:            "bench has room, optional active-slot drop",
			placements:      map[int64]RosterSlot{2: SlotC},
			drop:            intp(2),
			waiverClaimable: true,
		},
	}
}

func (c replacementCase) roster() RosterState {
	return RosterState{Placements: maps.Clone(c.placements), Limits: replacementLimits()}
}

func TestValidateAddPlayer_ReplacementCapacity(t *testing.T) {
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{replacementCandidate: {}}}
	for _, tc := range replacementCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAddPlayer(AddPlayerArgs{PlayerID: replacementCandidate, DropPlayerID: tc.drop}, tc.roster(), fa)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestValidateClaimPlayer_ReplacementCapacity(t *testing.T) {
	fa := PoolFreeAgentState{OnWaivers: map[int64]struct{}{replacementCandidate: {}}}
	for _, tc := range replacementCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateClaimPlayer(ClaimPlayerArgs{PlayerID: replacementCandidate, DropPlayerID: tc.drop}, tc.roster(), fa, nil)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// SIM-I6 minimal case: applying the accepted add to the working state
// must never leave BN above its limit. Before the fix the validator
// accepted this add and the result held two BN players with capacity 1.
func TestValidateAddPlayer_ActiveDropCannotOverfillBench(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotBN, 2: SlotC},
		Limits:     map[RosterSlot]int{SlotBN: 1, SlotC: 1},
	}
	fa := PoolFreeAgentState{FreeAgents: map[int64]struct{}{3: {}}}
	err := ValidateAddPlayer(AddPlayerArgs{PlayerID: 3, DropPlayerID: intp(2)}, roster, fa)
	require.ErrorIs(t, err, ErrDropLeavesBenchFull)
}

func TestCheckAcquisitionCapacity_NoBenchLimitIsUnlimited(t *testing.T) {
	roster := RosterState{
		Placements: map[int64]RosterSlot{1: SlotBN, 2: SlotBN},
		Limits:     map[RosterSlot]int{SlotC: 1},
	}
	require.NoError(t, checkAcquisitionCapacity(roster, nil))
}

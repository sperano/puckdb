package simulation

import (
	"testing"

	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEligibleSlots(t *testing.T) {
	tests := []struct {
		name     string
		position sqlcdb.PlayerPosition
		want     []RosterSlot
	}{
		{"center", sqlcdb.PlayerPositionC, []RosterSlot{SlotC, SlotUtil, SlotBN, SlotIR}},
		{"left wing", sqlcdb.PlayerPositionLW, []RosterSlot{SlotLW, SlotUtil, SlotBN, SlotIR}},
		{"right wing", sqlcdb.PlayerPositionRW, []RosterSlot{SlotRW, SlotUtil, SlotBN, SlotIR}},
		{"defense", sqlcdb.PlayerPositionD, []RosterSlot{SlotD, SlotUtil, SlotBN, SlotIR}},
		{"goalie excludes Util", sqlcdb.PlayerPositionG, []RosterSlot{SlotG, SlotBN, SlotIR}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EligibleSlots(tt.position)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEligibleSlots_RejectsForward(t *testing.T) {
	// PLAN.md V1 simplification: F is a valid Postgres enum value but
	// explicitly unsupported by the simulator — must surface as a loud error.
	_, err := EligibleSlots(sqlcdb.PlayerPositionF)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "F")
	assert.Contains(t, err.Error(), "not supported")
}

func TestEligibleSlots_RejectsUnknown(t *testing.T) {
	_, err := EligibleSlots(sqlcdb.PlayerPosition(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized")

	_, err = EligibleSlots(sqlcdb.PlayerPosition("XYZ"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "XYZ")
}

func TestIsEligibleSlot(t *testing.T) {
	tests := []struct {
		name     string
		position sqlcdb.PlayerPosition
		slot     RosterSlot
		want     bool
	}{
		// Skaters can fill their own slot, Util, BN, IR.
		{"C in C", sqlcdb.PlayerPositionC, SlotC, true},
		{"C in Util", sqlcdb.PlayerPositionC, SlotUtil, true},
		{"C in BN", sqlcdb.PlayerPositionC, SlotBN, true},
		{"C in IR", sqlcdb.PlayerPositionC, SlotIR, true},
		{"C cannot fill LW", sqlcdb.PlayerPositionC, SlotLW, false},
		{"C cannot fill G", sqlcdb.PlayerPositionC, SlotG, false},

		// Goalies can fill G, BN, IR — but not Util.
		{"G in G", sqlcdb.PlayerPositionG, SlotG, true},
		{"G in BN", sqlcdb.PlayerPositionG, SlotBN, true},
		{"G in IR", sqlcdb.PlayerPositionG, SlotIR, true},
		{"G cannot fill Util", sqlcdb.PlayerPositionG, SlotUtil, false},
		{"G cannot fill C", sqlcdb.PlayerPositionG, SlotC, false},

		// Defense.
		{"D in D", sqlcdb.PlayerPositionD, SlotD, true},
		{"D in Util", sqlcdb.PlayerPositionD, SlotUtil, true},
		{"D cannot fill C", sqlcdb.PlayerPositionD, SlotC, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsEligibleSlot(tt.position, tt.slot)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsEligibleSlot_PropagatesPositionError(t *testing.T) {
	_, err := IsEligibleSlot(sqlcdb.PlayerPositionF, SlotC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

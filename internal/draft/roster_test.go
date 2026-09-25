package draft

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unknownSlot = "XYZ"

func TestSlotAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		slot     string
		eligible []string
		want     bool
	}{
		{"center fits center", PositionCenter, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW fits LW", PositionLeftWing, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW fits Util flex", SlotUtility, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW fits F flex", SlotForward, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW fits W flex", SlotWing, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW fits bench", SlotBench, []string{PositionCenter, PositionLeftWing}, true},
		{"center-LW does not fit RW", PositionRightWing, []string{PositionCenter, PositionLeftWing}, false},
		{"center-LW does not fit D", PositionDefense, []string{PositionCenter, PositionLeftWing}, false},
		{"center-LW does not fit G", PositionGoalie, []string{PositionCenter, PositionLeftWing}, false},
		{"center-LW does not fit IR+ unless listed", SlotInjuredPlus, []string{PositionCenter, PositionLeftWing}, false},
		{"goalie fits G", PositionGoalie, []string{PositionGoalie}, true},
		{"goalie fits bench", SlotBench, []string{PositionGoalie}, true},
		{"goalie does not fit Util", SlotUtility, []string{PositionGoalie}, false},
		{"IR+ fits when Yahoo lists it", SlotInjuredPlus, []string{PositionRightWing, SlotInjuredPlus}, true},
		{"IR+ refused when not listed", SlotInjuredPlus, []string{PositionRightWing}, false},
		{"NA fits when Yahoo lists it", SlotNotActive, []string{PositionCenter, SlotNotActive}, true},
		{"NA refused when not listed", SlotNotActive, []string{PositionCenter}, false},
		{"unknown slot fits when Yahoo lists it", unknownSlot, []string{unknownSlot}, true},
		{"unknown slot refused when not listed", unknownSlot, []string{PositionCenter}, false},
		{"Util fits when Yahoo explicitly lists it", SlotUtility, []string{SlotUtility}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SlotAccepts(tt.slot, tt.eligible))
		})
	}
}

func TestRosterSlot_Kind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		position string
		want     SlotKind
	}{
		{PositionCenter, SlotKindStarting},
		{SlotUtility, SlotKindStarting},
		{SlotForward, SlotKindStarting},
		{SlotWing, SlotKindStarting},
		{SlotBench, SlotKindBench},
		{SlotInjuredReserve, SlotKindReserve},
		{SlotInjuredPlus, SlotKindReserve},
		{SlotNotActive, SlotKindReserve},
		{unknownSlot, SlotKindUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.position, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RosterSlot{Position: tt.position}.Kind())
		})
	}
}

func assignmentFor(f Feasibility, slot string) (SlotAssignment, bool) {
	for _, a := range f.Assignments {
		if a.Slot == slot {
			return a, true
		}
	}
	return SlotAssignment{}, false
}

func TestCheckRoster_MultiPositionReassignment(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: PositionLeftWing, Count: 1, Starting: true},
	}
	flexPlayer := RosterPlayer{YahooPlayerID: 1, Name: "A", EligiblePositions: []string{PositionCenter, PositionLeftWing}}
	centerOnly := RosterPlayer{YahooPlayerID: 2, Name: "B", EligiblePositions: []string{PositionCenter}}

	orders := map[string][]RosterPlayer{
		"flex first":        {flexPlayer, centerOnly},
		"center-only first": {centerOnly, flexPlayer},
	}
	for name, players := range orders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := CheckRoster(slots, players)
			require.True(t, result.Feasible())
			lw, found := assignmentFor(result, PositionLeftWing)
			require.True(t, found)
			assert.Equal(t, flexPlayer.YahooPlayerID, lw.YahooPlayerID, "the multi-position player moves to LW")
			c, found := assignmentFor(result, PositionCenter)
			require.True(t, found)
			assert.Equal(t, centerOnly.YahooPlayerID, c.YahooPlayerID, "the center-only player keeps C")
		})
	}
}

func TestCheckRoster_FlexSlotAcceptsBothCenters(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: SlotUtility, Count: 1, Starting: true},
	}
	players := []RosterPlayer{
		{YahooPlayerID: 1, Name: "X", EligiblePositions: []string{PositionCenter}},
		{YahooPlayerID: 2, Name: "Y", EligiblePositions: []string{PositionCenter}},
	}
	result := CheckRoster(slots, players)
	assert.True(t, result.Feasible())
	assert.Empty(t, result.OpenSlots)
	assert.Len(t, result.Assignments, 2)
}

func TestCheckRoster_ReserveSlotNeedsYahooEligibility(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{
		{Position: PositionRightWing, Count: 1, Starting: true},
		{Position: SlotInjuredPlus, Count: 1, Starting: false},
	}
	injured := RosterPlayer{YahooPlayerID: 1, Name: "Injured", EligiblePositions: []string{PositionRightWing, SlotInjuredPlus}}
	healthy := RosterPlayer{YahooPlayerID: 2, Name: "Healthy", EligiblePositions: []string{PositionRightWing}}

	t.Run("both placed when IR+ eligible", func(t *testing.T) {
		t.Parallel()
		result := CheckRoster(slots, []RosterPlayer{injured, healthy})
		assert.True(t, result.Feasible())
		assert.Len(t, result.Assignments, 2)
	})

	t.Run("second RW unplaced without IR+ eligibility", func(t *testing.T) {
		t.Parallel()
		injuredNoReserve := RosterPlayer{YahooPlayerID: 1, Name: "Injured", EligiblePositions: []string{PositionRightWing}}
		result := CheckRoster(slots, []RosterPlayer{injuredNoReserve, healthy})
		assert.False(t, result.Feasible())
		require.Len(t, result.Unplaced, 1)
		assert.Equal(t, 1, result.OpenSlots[SlotInjuredPlus])
	})
}

func TestCheckRoster_UnresolvedPlayer(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	noPositions := RosterPlayer{YahooPlayerID: 1, Name: "Ghost"}

	result := CheckRoster(slots, []RosterPlayer{noPositions})
	require.Len(t, result.Unresolved, 1)
	assert.Empty(t, result.Assignments)
	assert.False(t, result.Feasible())
	require.Len(t, result.Warnings(), 1)
	assert.Contains(t, result.Warnings()[0], "no Yahoo eligibility")
}

func TestCheckRoster_OpenSlotsCountsUnfilledUnits(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{{Position: PositionCenter, Count: 2, Starting: true}}
	players := []RosterPlayer{{YahooPlayerID: 1, Name: "A", EligiblePositions: []string{PositionCenter}}}

	result := CheckRoster(slots, players)
	assert.Equal(t, 1, result.OpenSlots[PositionCenter])
	assert.True(t, result.Feasible(), "an open slot with no unplaced player is still feasible")
}

func TestCheckRoster_StartersFilledBeforeBench(t *testing.T) {
	t.Parallel()
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: SlotBench, Count: 1, Starting: false},
	}
	player := RosterPlayer{YahooPlayerID: 1, Name: "A", EligiblePositions: []string{PositionCenter}}

	result := CheckRoster(slots, []RosterPlayer{player})
	c, found := assignmentFor(result, PositionCenter)
	require.True(t, found, "the sole player must start rather than sit on the bench")
	assert.Equal(t, player.YahooPlayerID, c.YahooPlayerID)
	assert.Equal(t, 1, result.OpenSlots[SlotBench])
}

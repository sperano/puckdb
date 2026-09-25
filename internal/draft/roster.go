package draft

import (
	"fmt"
	"slices"
)

// Yahoo NHL roster positions.
const (
	PositionCenter      = "C"
	PositionLeftWing    = "LW"
	PositionRightWing   = "RW"
	PositionDefense     = "D"
	PositionGoalie      = "G"
	SlotUtility         = "Util"
	SlotForward         = "F"
	SlotWing            = "W"
	SlotBench           = "BN"
	SlotInjuredReserve  = "IR"
	SlotInjuredPlus     = "IR+"
	SlotNotActive       = "NA"
	unassignedSlotIndex = -1
)

// basePositions are the positions a player can hold on the ice.
var basePositions = []string{PositionCenter, PositionLeftWing, PositionRightWing, PositionDefense, PositionGoalie}

// flexSlotPositions lists the base positions each flex slot accepts.
var flexSlotPositions = map[string][]string{
	SlotUtility: {PositionCenter, PositionLeftWing, PositionRightWing, PositionDefense},
	SlotForward: {PositionCenter, PositionLeftWing, PositionRightWing},
	SlotWing:    {PositionLeftWing, PositionRightWing},
}

// reserveSlots only take players Yahoo itself lists as eligible for them
// (injury or not-active status); the model never infers that from a status.
var reserveSlots = []string{SlotInjuredReserve, SlotInjuredPlus, SlotNotActive}

// SlotKind groups roster slots by how the roster uses them.
type SlotKind int

const (
	SlotKindStarting SlotKind = iota
	SlotKindBench
	SlotKindReserve
	// SlotKindUnsupported is a slot name the model does not know.
	SlotKindUnsupported
)

// Kind classifies a roster slot.
func (s RosterSlot) Kind() SlotKind {
	switch {
	case slices.Contains(reserveSlots, s.Position):
		return SlotKindReserve
	case s.Position == SlotBench:
		return SlotKindBench
	case slices.Contains(basePositions, s.Position), flexSlotPositions[s.Position] != nil:
		return SlotKindStarting
	default:
		return SlotKindUnsupported
	}
}

// SlotAccepts reports whether a player with Yahoo's eligible positions may
// fill the slot: Yahoo listing the slot always counts; a flex slot also takes
// any of its base positions; the bench takes anyone with a base position.
// Reserve slots and unknown slots take only players Yahoo lists for them.
func SlotAccepts(slot string, eligible []string) bool {
	if slices.Contains(eligible, slot) {
		return true
	}
	if slot == SlotBench {
		return hasBasePosition(eligible)
	}
	for _, position := range flexSlotPositions[slot] {
		if slices.Contains(eligible, position) {
			return true
		}
	}
	return false
}

func hasBasePosition(eligible []string) bool {
	return slices.ContainsFunc(eligible, func(p string) bool { return slices.Contains(basePositions, p) })
}

// RosterPlayer is a player to place on a roster.
type RosterPlayer struct {
	YahooPlayerID     int
	Name              string
	EligiblePositions []string
}

// Resolved reports whether the player has usable Yahoo eligibility.
func (p RosterPlayer) Resolved() bool {
	return hasBasePosition(p.EligiblePositions)
}

// SlotAssignment places one player in one unit of a slot.
type SlotAssignment struct {
	Slot          string
	Index         int
	YahooPlayerID int
}

// Feasibility is the outcome of placing players on a league's roster.
type Feasibility struct {
	Assignments []SlotAssignment
	// Unplaced players have eligibility but no legal slot left.
	Unplaced []RosterPlayer
	// Unresolved players have no Yahoo eligibility and were not placed.
	Unresolved []RosterPlayer
	// OpenSlots counts unfilled units per slot.
	OpenSlots map[string]int
}

// Feasible reports whether every player was placed.
func (f Feasibility) Feasible() bool {
	return len(f.Unplaced) == 0 && len(f.Unresolved) == 0
}

// Warnings describes every player the check could not place.
func (f Feasibility) Warnings() []string {
	var warnings []string
	for _, p := range f.Unresolved {
		warnings = append(warnings, fmt.Sprintf("%s (Yahoo %d) has no Yahoo eligibility; not placed", p.Name, p.YahooPlayerID))
	}
	for _, p := range f.Unplaced {
		warnings = append(warnings, fmt.Sprintf("%s (Yahoo %d) fits no open slot", p.Name, p.YahooPlayerID))
	}
	return warnings
}

// slotUnit is one seat of a slot with Count > 1.
type slotUnit struct {
	slot  string
	index int
}

// CheckRoster places players on the league's roster slots. It computes a
// maximum bipartite matching between players and slot units, filling
// starting slots first, then reserve slots, then the bench, so a
// multi-position player (C/LW) moves to whichever of his slots lets the most
// players start. Players are considered in the order given.
func CheckRoster(slots []RosterSlot, players []RosterPlayer) Feasibility {
	result := Feasibility{OpenSlots: make(map[string]int)}
	var resolved []RosterPlayer
	for _, p := range players {
		if p.Resolved() {
			resolved = append(resolved, p)
		} else {
			result.Unresolved = append(result.Unresolved, p)
		}
	}
	units := orderedUnits(slots)
	owner := matchUnits(units, resolved)
	placed := make([]bool, len(resolved))
	for u, playerIdx := range owner {
		if playerIdx == unassignedSlotIndex {
			result.OpenSlots[units[u].slot]++
			continue
		}
		placed[playerIdx] = true
		result.Assignments = append(result.Assignments, SlotAssignment{
			Slot: units[u].slot, Index: units[u].index, YahooPlayerID: resolved[playerIdx].YahooPlayerID,
		})
	}
	for i, p := range resolved {
		if !placed[i] {
			result.Unplaced = append(result.Unplaced, p)
		}
	}
	return result
}

// orderedUnits expands slots into units: starting, then reserve, then bench
// (unsupported slots last), each group in the league's slot order.
func orderedUnits(slots []RosterSlot) []slotUnit {
	var units []slotUnit
	for _, kind := range []SlotKind{SlotKindStarting, SlotKindReserve, SlotKindBench, SlotKindUnsupported} {
		for _, slot := range slots {
			if slot.Kind() != kind {
				continue
			}
			for i := range slot.Count {
				units = append(units, slotUnit{slot: slot.Position, index: i})
			}
		}
	}
	return units
}

// matchUnits runs Kuhn's augmenting-path matching, one unit at a time in
// priority order. A matched unit never becomes unmatched, so higher-priority
// units keep the maximum coverage they reached. It returns, per unit, the
// index of the player placed there or unassignedSlotIndex.
func matchUnits(units []slotUnit, players []RosterPlayer) []int {
	owner := make([]int, len(units))
	for u := range owner {
		owner[u] = unassignedSlotIndex
	}
	unitOf := make([]int, len(players))
	for p := range unitOf {
		unitOf[p] = unassignedSlotIndex
	}
	for u := range units {
		visited := make([]bool, len(players))
		augment(u, units, players, owner, unitOf, visited)
	}
	return owner
}

// augment tries to give unit u a player. A candidate already placed
// elsewhere is taken when the unit holding him can be refilled by another
// player (recursively), which is how a C/LW moves from C to LW to let a pure
// center start.
func augment(u int, units []slotUnit, players []RosterPlayer, owner, unitOf []int, visited []bool) bool {
	for p, player := range players {
		if visited[p] || !SlotAccepts(units[u].slot, player.EligiblePositions) {
			continue
		}
		visited[p] = true
		if unitOf[p] == unassignedSlotIndex || augment(unitOf[p], units, players, owner, unitOf, visited) {
			owner[u] = p
			unitOf[p] = u
			return true
		}
	}
	return false
}

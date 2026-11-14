package simulation

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The supported pool shape. The system prompt states one category list
// and one roster (v1FixedCategories, v1FixedRoster in prompts.go) and the
// standings score the same categories whatever a pool stores, so a pool
// created with any other shape would tell its agents about slots the
// validators reject. Pool creation refuses such a pool (ValidateCategories,
// ValidateRosterPositions). Whether categories and the roster become
// configurable per pool is an open product decision (wiki
// puckdb/simulation.md > "Open decisions"); until it is taken, these
// checks keep configured pools and prompts identical.

// rosterSlotOrder is the display order of the roster slots, the order
// of the prompt's "Roster:" line.
var rosterSlotOrder = []RosterSlot{SlotC, SlotLW, SlotRW, SlotD, SlotUtil, SlotG, SlotBN, SlotIR}

// ErrUnsupportedPoolShape marks a category list or roster other than the
// supported one.
var ErrUnsupportedPoolShape = errors.New("unsupported pool shape")

// SupportedCategories returns the roto categories the simulator scores,
// in prompt order.
func SupportedCategories() []string {
	return append([]string(nil), v1FixedCategories...)
}

// SupportedRosterPositions returns the roster slot counts the simulator
// supports.
func SupportedRosterPositions() map[RosterSlot]int {
	out := make(map[RosterSlot]int, len(v1FixedRoster))
	for slot, n := range v1FixedRoster {
		out[slot] = n
	}
	return out
}

// IsRosterSlot reports whether s names a roster slot.
func IsRosterSlot(s RosterSlot) bool {
	for _, slot := range rosterSlotOrder {
		if slot == s {
			return true
		}
	}
	return false
}

// RosterCapacity is the number of players a roster with these slot counts
// holds, every slot (bench and IR included) counted.
func RosterCapacity(positions map[RosterSlot]int) int {
	total := 0
	for _, n := range positions {
		total += n
	}
	return total
}

// ValidateCategories accepts exactly the supported categories, each once,
// in any order.
func ValidateCategories(categories []string) error {
	supported := make(map[string]bool, len(v1FixedCategories))
	for _, c := range v1FixedCategories {
		supported[c] = true
	}
	seen := make(map[string]bool, len(categories))
	var problems []string
	for _, c := range categories {
		switch {
		case !supported[c]:
			problems = append(problems, fmt.Sprintf("unknown category %q", c))
		case seen[c]:
			problems = append(problems, fmt.Sprintf("duplicate category %q", c))
		}
		seen[c] = true
	}
	for _, c := range v1FixedCategories {
		if !seen[c] {
			problems = append(problems, fmt.Sprintf("missing category %q", c))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s; the simulator scores exactly [%s]",
		ErrUnsupportedPoolShape, strings.Join(problems, ", "), strings.Join(v1FixedCategories, ", "))
}

// ValidateRosterPositions accepts exactly the supported slot counts. A
// slot absent from positions counts as 0.
func ValidateRosterPositions(positions map[RosterSlot]int) error {
	var problems []string
	var unknown []string
	for slot := range positions {
		if !IsRosterSlot(slot) {
			unknown = append(unknown, string(slot))
		}
	}
	sort.Strings(unknown)
	for _, slot := range unknown {
		problems = append(problems, fmt.Sprintf("unknown slot %q", slot))
	}
	for _, slot := range rosterSlotOrder {
		if got, want := positions[slot], v1FixedRoster[slot]; got != want {
			problems = append(problems, fmt.Sprintf("%s count %d, want %d", slot, got, want))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s; the simulator supports exactly %s",
		ErrUnsupportedPoolShape, strings.Join(problems, ", "), formatRoster(v1FixedRoster))
}

// formatRoster renders slot counts in display order, as "2 C, 2 LW, ...".
func formatRoster(positions map[RosterSlot]int) string {
	parts := make([]string, 0, len(rosterSlotOrder))
	for _, slot := range rosterSlotOrder {
		parts = append(parts, fmt.Sprintf("%d %s", positions[slot], slot))
	}
	return strings.Join(parts, ", ")
}

// ValidateProvider reports whether an agent of this provider can be
// built (the same check NewAgent makes when the workflow first runs it).
func ValidateProvider(provider string) error {
	_, err := resolveProvider(provider)
	return err
}

package draftrank

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/draft"
)

const (
	rulesChangedMessage       = "league rules were imported again with changes since this snapshot; refresh to rank under them"
	rosterSlotsChangedMessage = "roster slots are now %s but this snapshot was ranked with %s; recommendations are withheld until a refresh"
	scoringChangedMessage     = "scoring (type, team count, categories, directions or weights) changed since this snapshot; recommendations are withheld until a refresh"
)

// rulesChangeIssues compares the rules a snapshot was ranked with to the
// league's current rules. Any difference is RULES_CHANGED. The rules hash
// also covers settings that do not shape a ranking (the league name, the
// draft status and time), so the roster slots and scoring the snapshot
// recorded are compared too: a difference there means the snapshot's values
// and slot allocation no longer describe the league.
func rulesChangeIssues(ranked League, current draft.Snapshot) []Issue {
	if RulesHash(current) == ranked.RulesHash {
		return nil
	}
	issues := []Issue{{Code: IssueRulesChanged, Message: rulesChangedMessage}}
	league := LeagueOf(current)
	if !slices.Equal(ranked.RosterSlots, league.RosterSlots) {
		issues = append(issues, Issue{Code: IssueRosterSlotsChanged,
			Message: fmt.Sprintf(rosterSlotsChangedMessage, slotsLabel(league.RosterSlots), slotsLabel(ranked.RosterSlots))})
	}
	if !sameScoring(ranked, league) {
		issues = append(issues, Issue{Code: IssueScoringChanged, Message: scoringChangedMessage})
	}
	return issues
}

// sameScoring compares what values are computed from. Display names and
// abbreviations are left out: renaming a category changes no value.
func sameScoring(ranked, current League) bool {
	if ranked.ScoringType != current.ScoringType || ranked.NumTeams != current.NumTeams ||
		ranked.Format != current.Format || ranked.Objective != current.Objective {
		return false
	}
	return slices.EqualFunc(ranked.Categories, current.Categories, func(a, b Category) bool {
		return a.StatID == b.StatID && a.Direction == b.Direction && a.Weight == b.Weight &&
			slices.Equal(a.PositionTypes, b.PositionTypes)
	})
}

func slotsLabel(slots []draft.RosterSlot) string {
	if len(slots) == 0 {
		return "none"
	}
	labels := make([]string, len(slots))
	for i, slot := range slots {
		labels[i] = fmt.Sprintf("%s×%d", slot.Position, slot.Count)
	}
	return strings.Join(labels, ", ")
}

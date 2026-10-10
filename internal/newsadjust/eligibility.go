package newsadjust

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Event eligibility is decided once per event and scenario, before any
// lifecycle link or claim is built: an exclude_event override removes the
// event from the scenarios it covers, so there the event neither changes a
// projection nor supersedes or closes another event. The links themselves
// are per scenario, so an exclusion scoped to one scenario leaves the
// others exactly as they were.

// resolveExclusions finds, for every visible event and scenario, the
// active exclusion that removes the event there. An exclusion naming
// another player's event removes nothing and alerts both players.
func (s *selector) resolveExclusions(active []Override) {
	byID := make(map[string]Event, len(s.visible))
	for _, e := range s.visible {
		byID[e.ID] = e
		target := overrideTarget(e.PlayerKey, OverrideExcludeEvent, e.ID, "")
		for _, sc := range Scenarios {
			if o, exists := matchOverride(active, target, sc); exists {
				setScenario(s.exclusions, e.ID, sc, o)
			}
		}
	}
	for _, o := range active {
		excluded, visible := byID[o.EventID]
		switch {
		case o.Kind != OverrideExcludeEvent || !visible:
		case excluded.PlayerKey != o.PlayerKey:
			message := fmt.Sprintf("override %s excludes event %s for %s, but the event is about %s; the exclusion is ignored",
				o.ID, o.EventID, o.PlayerKey, excluded.PlayerKey)
			s.alert(o.PlayerKey, message)
			s.alert(excluded.PlayerKey, message)
		default:
			s.alertStoredResolutions(o, excluded, byID)
		}
	}
}

// alertStoredResolutions warns when an excluded return names an event
// that records its own resolution: that end stays, so the exclusion does
// not reopen the absence. Storage refuses such exclusions; this covers
// ones recorded before it did.
func (s *selector) alertStoredResolutions(o Override, ret Event, byID map[string]Event) {
	for _, id := range ret.Supersedes {
		if target, visible := byID[id]; visible && target.Lifecycle == LifecycleResolved && target.PlayerKey == ret.PlayerKey {
			s.alert(ret.PlayerKey, fmt.Sprintf("override %s excludes return %s, but event %s records its own end at %s; "+
				"the exclusion does not reopen it, set a missed_games override instead",
				o.ID, ret.ID, id, target.EffectiveUntil.UTC().Format(time.RFC3339)))
		}
	}
}

// eligible returns the scenarios where no exclusion removes the event.
func (s *selector) eligible(e Event) []Scenario {
	return slices.DeleteFunc(slices.Clone(Scenarios), func(sc Scenario) bool {
		_, excluded := s.exclusions[e.ID][sc]
		return excluded
	})
}

// linkScenarios returns the scenarios where an event may supersede or
// close others: it counts (see counts) and is eligible there.
func (s *selector) linkScenarios(e Event) []Scenario {
	if !counts(e) {
		return nil
	}
	return s.eligible(e)
}

// openScenarios returns the scenarios where nothing removes the event,
// and the reason it is removed from the others: superseded by a counting
// correction, else excluded by an override.
func (s *selector) openScenarios(e Event) ([]Scenario, string) {
	var open []Scenario
	removed := make(map[string][]Scenario)
	var causes []string
	for _, sc := range Scenarios {
		var cause string
		if by := s.supersededBy[e.ID][sc]; by != "" {
			cause = "superseded by event " + by
		} else if o, excluded := s.exclusions[e.ID][sc]; excluded {
			cause = fmt.Sprintf("excluded by override %s: %s", o.ID, o.Reason)
		}
		if cause == "" {
			open = append(open, sc)
			continue
		}
		if _, seen := removed[cause]; !seen {
			causes = append(causes, cause)
		}
		removed[cause] = append(removed[cause], sc)
	}
	reasons := make([]string, len(causes))
	for i, cause := range causes {
		reasons[i] = inScenarios(cause, removed[cause])
	}
	return open, strings.Join(reasons, "; ")
}

// inScenarios qualifies a reason that holds in only some scenarios.
func inScenarios(reason string, scenarios []Scenario) string {
	if len(scenarios) == len(Scenarios) {
		return reason
	}
	names := make([]string, len(scenarios))
	for i, sc := range scenarios {
		names[i] = string(sc)
	}
	return reason + " in " + strings.Join(names, ", ")
}

// setScenario records a per-scenario link of an event.
func setScenario[V any](links map[string]map[Scenario]V, eventID string, sc Scenario, value V) {
	if links[eventID] == nil {
		links[eventID] = make(map[Scenario]V, len(Scenarios))
	}
	links[eventID][sc] = value
}

// earliestClosure is the first closure of an event in any scenario.
func earliestClosure(closures map[Scenario]closure) (closure, bool) {
	var first closure
	for _, sc := range Scenarios {
		if c, exists := closures[sc]; exists && (first.by == "" || c.at.Before(first.at)) {
			first = c
		}
	}
	return first, first.by != ""
}

// describeClosures names the returns that closed a claim, with the
// scenarios each one closed it in when that is not all of them. A return
// always closes at its own start, so its ID identifies the closure.
func describeClosures(closures map[Scenario]closure) string {
	byReturn := make(map[string][]Scenario)
	var order []closure
	for _, sc := range Scenarios {
		c, exists := closures[sc]
		if !exists {
			continue
		}
		if _, seen := byReturn[c.by]; !seen {
			order = append(order, c)
		}
		byReturn[c.by] = append(byReturn[c.by], sc)
	}
	parts := make([]string, len(order))
	for i, c := range order {
		parts[i] = inScenarios(fmt.Sprintf("closed by return %s at %s", c.by, c.at.UTC().Format(time.RFC3339)), byReturn[c.by])
	}
	return strings.Join(parts, "; ")
}

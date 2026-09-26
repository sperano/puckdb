package newsadjust

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/projection"
)

// Outcome says what an event version did to the adjustment.
type Outcome string

const (
	// OutcomeApplied: the event changes at least one scenario, or (for a
	// return) closes earlier events.
	OutcomeApplied Outcome = "applied"
	// OutcomeMerged: another report of an applied incident; it adds no
	// effect of its own.
	OutcomeMerged Outcome = "merged"
	// OutcomeAlert: shown to the manager without changing a projection.
	OutcomeAlert Outcome = "alert"
	// OutcomeSkipped: no effect, with the reason.
	OutcomeSkipped Outcome = "skipped"
)

// Decision is the audit record of one event version at the as-of time.
type Decision struct {
	EventID    string     `json:"event_id"`
	Version    int        `json:"version"`
	PlayerKey  string     `json:"player_key"`
	IncidentID int64      `json:"incident_id,omitempty"`
	Outcome    Outcome    `json:"outcome"`
	Reason     string     `json:"reason"`
	Scenarios  []Scenario `json:"scenarios,omitempty"`
}

// closure is when a later return (or the event's own end) closed an event.
type closure struct {
	at time.Time
	by string
}

// selector decides, for one as-of time, which event versions count.
type selector struct {
	policy       Policy
	season       Season
	players      map[string]projection.PlayerProjection
	exclusions   map[string]Override
	visible      []Event
	supersededBy map[string]string
	closedBy     map[string]closure
	decisions    map[string]Decision
	alerts       map[string][]string
}

// visibleEvents keeps the latest version of each event recorded by asOf.
// Two different contents for one version, or one event moving between
// players, are extraction errors and stop the adjustment.
func visibleEvents(events []Event, asOf time.Time) ([]Event, error) {
	latest := make(map[string]Event, len(events))
	contents := make(map[string]string, len(events))
	for _, e := range events {
		e = e.normalized()
		if err := e.Validate(); err != nil {
			return nil, err
		}
		versionKey := fmt.Sprintf("%s#%d", e.ID, e.Version)
		if previous, exists := contents[versionKey]; exists && previous != hashJSON(e) {
			return nil, fmt.Errorf("event %q v%d has two different contents", e.ID, e.Version)
		}
		contents[versionKey] = hashJSON(e)
		if prior, exists := latest[e.ID]; exists && prior.PlayerKey != e.PlayerKey {
			return nil, fmt.Errorf("event %q names two players", e.ID)
		}
		if e.RecordedAt.After(asOf) {
			continue
		}
		if prior, exists := latest[e.ID]; !exists || e.Version > prior.Version {
			latest[e.ID] = e
		}
	}
	visible := make([]Event, 0, len(latest))
	for _, e := range latest {
		visible = append(visible, e)
	}
	slices.SortFunc(visible, compareEventOrder)
	return visible, nil
}

func compareEventOrder(a, b Event) int {
	if c := cmp.Compare(a.PlayerKey, b.PlayerKey); c != 0 {
		return c
	}
	if c := a.start().Compare(b.start()); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// counts reports whether an event version can supersede or close others:
// it must be a confirmed or reported event, not held, and still in force.
func counts(e Event) bool {
	return e.Status != StatusRumor && e.Hold == "" &&
		(e.Lifecycle == LifecycleActive || e.Lifecycle == LifecycleResolved)
}

// linkEvents records corrections (Supersedes on a non-return event) and
// returns, which close the availability events they name, or, when they
// name none, every earlier open-ended absence of the player.
func (s *selector) linkEvents() {
	for _, e := range s.visible {
		if !counts(e) || e.Type == EventReturn {
			continue
		}
		for _, target := range e.Supersedes {
			if s.samePlayer(e, target) {
				s.supersededBy[target] = e.ID
			}
		}
	}
	for _, e := range s.visible {
		if !counts(e) || e.Type != EventReturn || s.supersededBy[e.ID] != "" {
			continue
		}
		for _, target := range s.returnTargets(e) {
			if prior, closed := s.closedBy[target]; !closed || e.start().Before(prior.at) {
				s.closedBy[target] = closure{at: e.start(), by: e.ID}
			}
		}
	}
}

func (s *selector) returnTargets(ret Event) []string {
	if len(ret.Supersedes) > 0 {
		return slices.DeleteFunc(slices.Clone(ret.Supersedes), func(id string) bool { return !s.samePlayer(ret, id) })
	}
	var targets []string
	for _, e := range s.visible {
		if e.PlayerKey == ret.PlayerKey && s.openEnded(e) && e.start().Before(ret.start()) {
			targets = append(targets, e.ID)
		}
	}
	return targets
}

// openEnded reports whether an absence has no end of its own: no game
// count, no end time, and not a prior-season injury or absence. Only such an
// absence is closed by a return that does not name it, so an unrelated
// later return cannot stretch an absence that already ended.
func (s *selector) openEnded(e Event) bool {
	switch {
	case !e.affectsAvailability() || e.Lifecycle == LifecycleResolved || !e.EffectiveUntil.IsZero():
		return false
	case e.Duration.Kind == DurationGames:
		return false
	case e.Type != EventSuspension && s.season.priorSeason(e.start(), s.policy.OffseasonDays):
		// Last season's injury news ends with that season unless a return
		// names it; suspensions carry over and stay open.
		return false
	case e.Duration.Kind == DurationSeason:
		return !s.season.priorSeason(e.start(), s.policy.OffseasonDays)
	}
	return true
}

func (s *selector) samePlayer(e Event, targetID string) bool {
	for _, target := range s.visible {
		if target.ID == targetID {
			if target.PlayerKey != e.PlayerKey {
				s.alert(e.PlayerKey, fmt.Sprintf("event %s names event %s of another player; the link is ignored", e.ID, targetID))
				return false
			}
			return true
		}
	}
	return false
}

// screen returns false with a recorded decision when an event cannot
// become a claim; it returns the scenarios where a claim applies.
func (s *selector) screen(e Event) ([]Scenario, bool) {
	player, known := s.players[e.PlayerKey]
	switch {
	case e.Lifecycle == LifecycleRetracted:
		s.decide(e, OutcomeSkipped, "retracted by its source", nil)
	case e.Lifecycle == LifecycleSuperseded:
		s.decide(e, OutcomeSkipped, "superseded by a later event version", nil)
	case s.supersededBy[e.ID] != "":
		s.decide(e, OutcomeSkipped, "superseded by event "+s.supersededBy[e.ID], nil)
	case s.exclusions[e.ID].ID != "":
		o := s.exclusions[e.ID]
		s.decide(e, OutcomeSkipped, fmt.Sprintf("excluded by override %s: %s", o.ID, o.Reason), nil)
	case e.Hold != "":
		s.decide(e, OutcomeAlert, "held: "+e.Hold, nil)
		s.alert(e.PlayerKey, fmt.Sprintf("%s event %s held without effect: %s", e.Type, e.ID, e.Hold))
	case !known:
		s.decide(e, OutcomeSkipped, "player is not in the baseline projection", nil)
		s.alert(e.PlayerKey, fmt.Sprintf("event %s names a player the projection does not have", e.ID))
	case e.Status == StatusRumor:
		return s.screenRumor(e)
	case incorporated(player, e):
		s.decide(e, OutcomeSkipped, fmt.Sprintf("projection %s already incorporates news through %s",
			sourceLabel(player), player.IncorporatesNewsThrough.UTC().Format(time.RFC3339)), nil)
		if closed, exists := s.closedBy[e.ID]; exists && closed.at.After(player.IncorporatesNewsThrough) {
			s.alert(e.PlayerKey, fmt.Sprintf("projection %s incorporates event %s, which return %s later closed; refresh that projection to remove its penalty",
				sourceLabel(player), e.ID, closed.by))
		}
	case e.Type == EventReturn:
		s.decide(e, OutcomeApplied, "return closes earlier availability events", nil)
	default:
		return slices.Clone(Scenarios), true
	}
	return nil, false
}

func (s *selector) screenRumor(e Event) ([]Scenario, bool) {
	if s.policy.Rumors == RumorConservative && e.affectsAvailability() && !incorporated(s.players[e.PlayerKey], e) {
		return []Scenario{ScenarioConservative}, true
	}
	s.decide(e, OutcomeAlert, fmt.Sprintf("unconfirmed %s kept as an alert (rumor policy %s)", e.Type, s.policy.Rumors), nil)
	s.alert(e.PlayerKey, fmt.Sprintf("rumor: %s reported %s", e.Type, e.ReportedAt.UTC().Format(time.RFC3339)))
	return nil, false
}

// incorporated reports whether the projection's source already accounts
// for news reported up to and including the event.
func incorporated(player projection.PlayerProjection, e Event) bool {
	return !player.IncorporatesNewsThrough.IsZero() && !player.IncorporatesNewsThrough.Before(e.ReportedAt)
}

func sourceLabel(player projection.PlayerProjection) string {
	if player.Provider == "" {
		return string(player.Source)
	}
	return player.Provider + " " + player.ProviderVersion
}

func (s *selector) decide(e Event, outcome Outcome, reason string, scenarios []Scenario) {
	s.decisions[e.ID] = Decision{
		EventID: e.ID, Version: e.Version, PlayerKey: e.PlayerKey, IncidentID: e.IncidentID,
		Outcome: outcome, Reason: reason, Scenarios: scenarios,
	}
}

func (s *selector) alert(playerKey, message string) {
	if !slices.Contains(s.alerts[playerKey], message) {
		s.alerts[playerKey] = append(s.alerts[playerKey], message)
	}
}

// sortedDecisions returns the audit trail in event order.
func (s *selector) sortedDecisions() []Decision {
	out := make([]Decision, 0, len(s.decisions))
	for _, e := range s.visible {
		if d, exists := s.decisions[e.ID]; exists {
			out = append(out, d)
		}
	}
	return out
}

package newsadjust

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// member is one event behind an availability claim and the scenarios it
// may influence (a rumor under RumorConservative reaches only one).
type member struct {
	event     Event
	scenarios []Scenario
}

// availabilityClaim is one incident's availability effect. Repeated reports
// of an incident become members of one claim, so they never compound.
type availabilityClaim struct {
	members []member
	// closed is the earliest return closing any member, per scenario.
	closed      map[Scenario]closure
	conflicting bool
}

func (c availabilityClaim) primary() Event { return c.members[0].event }

// roleField is one independently reported part of a role.
type roleField string

const (
	fieldTeam       roleField = "team"
	fieldIceTime    roleField = "ice_time"
	fieldPowerPlay  roleField = "power_play"
	fieldGoalieRole roleField = "goalie_role"
)

// roleClaim is a role event and, per scenario it applies in, the fields it
// decides there and when the next report of each takes over (zero while it
// is the latest).
type roleClaim struct {
	event  Event
	ends   map[Scenario]map[roleField]time.Time
	closed map[Scenario]closure
}

type playerClaims struct {
	availability []availabilityClaim
	roles        []roleClaim
}

// buildClaims screens every visible event and groups the survivors.
func (s *selector) buildClaims() map[string]*playerClaims {
	groups := make(map[string][]member)
	var groupOrder []string
	var roleEvents []member
	for _, e := range s.visible {
		scenarios, ok := s.screen(e)
		if !ok {
			continue
		}
		if !e.affectsAvailability() {
			roleEvents = append(roleEvents, member{event: e, scenarios: scenarios})
			continue
		}
		key := incidentKey(e)
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], member{event: e, scenarios: scenarios})
	}
	claims := make(map[string]*playerClaims)
	for _, key := range groupOrder {
		claim := s.availabilityClaim(groups[key])
		player := playerClaimsFor(claims, claim.primary().PlayerKey)
		player.availability = append(player.availability, claim)
	}
	for _, claim := range s.roleClaims(roleEvents) {
		player := playerClaimsFor(claims, claim.event.PlayerKey)
		player.roles = append(player.roles, claim)
	}
	return claims
}

func playerClaimsFor(claims map[string]*playerClaims, key string) *playerClaims {
	if claims[key] == nil {
		claims[key] = &playerClaims{}
	}
	return claims[key]
}

// incidentKey groups reports of one incident; an event without an incident
// stands alone.
func incidentKey(e Event) string {
	if e.IncidentID != 0 {
		return fmt.Sprintf("%s|incident:%d", e.PlayerKey, e.IncidentID)
	}
	return e.PlayerKey + "|event:" + e.ID
}

// availabilityClaim orders members so the primary report is confirmed,
// most authoritative and newest, records merged reports, and flags
// members that disagree about timing or duration.
func (s *selector) availabilityClaim(members []member) availabilityClaim {
	slices.SortFunc(members, comparePrimary)
	claim := availabilityClaim{members: members, closed: make(map[Scenario]closure)}
	primary := claim.primary()
	for _, m := range members {
		for sc, closed := range s.closedBy[m.event.ID] {
			if prior, exists := claim.closed[sc]; !exists || closed.at.Before(prior.at) {
				claim.closed[sc] = closed
			}
		}
		if !sameTiming(primary, m.event) {
			claim.conflicting = true
		}
	}
	s.decide(primary, OutcomeApplied, claimReason(claim), claimScenarios(members))
	for _, m := range members[1:] {
		s.decide(m.event, OutcomeMerged, fmt.Sprintf("another report of incident %d; event %s carries its effect once", primary.IncidentID, primary.ID), m.scenarios)
	}
	if claim.conflicting {
		s.alert(primary.PlayerKey, fmt.Sprintf("conflicting reports for incident %d (%s): conservative uses the longest absence, optimistic the shortest, base follows event %s",
			primary.IncidentID, memberIDs(members), primary.ID))
	}
	return claim
}

func comparePrimary(a, b member) int {
	if c := cmp.Compare(a.event.Status.firmness(), b.event.Status.firmness()); c != 0 {
		return -c
	}
	if c := cmp.Compare(a.event.authority(), b.event.authority()); c != 0 {
		return c
	}
	if c := b.event.ReportedAt.Compare(a.event.ReportedAt); c != 0 {
		return c
	}
	return cmp.Compare(a.event.ID, b.event.ID)
}

func sameTiming(a, b Event) bool {
	return a.Duration == b.Duration && a.start().Equal(b.start()) && a.EffectiveUntil.Equal(b.EffectiveUntil)
}

func claimReason(claim availabilityClaim) string {
	primary := claim.primary()
	reason := fmt.Sprintf("%s, duration %s", primary.Type, describeDuration(primary.Duration))
	if closures := describeClosures(claim.closed); closures != "" {
		reason += "; " + closures
	}
	if len(claim.members) > 1 {
		reason += fmt.Sprintf("; %d reports merged", len(claim.members))
	}
	return reason
}

func describeDuration(d Duration) string {
	if d.Kind == DurationGames {
		return fmt.Sprintf("%d games", d.Games)
	}
	return string(d.Kind)
}

func claimScenarios(members []member) []Scenario {
	var out []Scenario
	for _, s := range Scenarios {
		if slices.ContainsFunc(members, func(m member) bool { return slices.Contains(m.scenarios, s) }) {
			out = append(out, s)
		}
	}
	return out
}

func memberIDs(members []member) string {
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.event.ID
	}
	return strings.Join(ids, ", ")
}

// roleClaims splits each role field into consecutive segments, per
// scenario: a report decides the field from its effective start until the
// next report of that field applying in the same scenario takes over. A
// reversed role keeps its earlier period, a repeated report never stacks
// on the one before it, and a report excluded from a scenario does not cut
// short the segment before it there.
func (s *selector) roleClaims(events []member) []roleClaim {
	ends := make(map[string]map[Scenario]map[roleField]time.Time)
	for _, sc := range Scenarios {
		var inScenario []Event
		for _, m := range events {
			if slices.Contains(m.scenarios, sc) {
				inScenario = append(inScenario, m.event)
			}
		}
		for id, fields := range segmentEnds(inScenario) {
			setScenario(ends, id, sc, fields)
		}
	}
	return s.decideRoles(events, ends)
}

// segmentEnds returns, per event and field it decides, when the next
// report of the field starts (zero while it is the latest). A report that
// a newer one with the same start replaces decides nothing.
func segmentEnds(events []Event) map[string]map[roleField]time.Time {
	byField := make(map[string][]Event)
	var keys []string
	for _, e := range events {
		for _, field := range reportedFields(e) {
			key := e.PlayerKey + "|" + string(field)
			if _, exists := byField[key]; !exists {
				keys = append(keys, key)
			}
			byField[key] = append(byField[key], e)
		}
	}
	ends := make(map[string]map[roleField]time.Time)
	for _, key := range keys {
		field := roleField(key[strings.LastIndex(key, "|")+1:])
		reports := byField[key]
		slices.SortFunc(reports, compareRoleSegments)
		for i, e := range reports {
			var end time.Time
			if i+1 < len(reports) {
				if !reports[i+1].start().After(e.start()) {
					continue
				}
				end = reports[i+1].start()
			}
			if ends[e.ID] == nil {
				ends[e.ID] = make(map[roleField]time.Time)
			}
			ends[e.ID][field] = end
		}
	}
	return ends
}

func (s *selector) decideRoles(events []member, ends map[string]map[Scenario]map[roleField]time.Time) []roleClaim {
	var claims []roleClaim
	for _, m := range events {
		e := m.event
		var fields []roleField
		var scenarios []Scenario
		for _, sc := range Scenarios {
			if len(ends[e.ID][sc]) > 0 {
				scenarios = append(scenarios, sc)
			}
		}
		for _, field := range reportedFields(e) {
			if slices.ContainsFunc(scenarios, func(sc Scenario) bool { _, decides := ends[e.ID][sc][field]; return decides }) {
				fields = append(fields, field)
			}
		}
		if len(fields) == 0 {
			s.decide(e, OutcomeSkipped, "a newer report with the same start replaces every role it reported", nil)
			continue
		}
		s.decide(e, OutcomeApplied, fmt.Sprintf("%s decides %s", e.Type, joinFields(fields)), scenarios)
		claims = append(claims, roleClaim{event: e, ends: ends[e.ID], closed: s.closedBy[e.ID]})
	}
	return claims
}

func reportedFields(e Event) []roleField {
	if e.Role == nil {
		return nil
	}
	var fields []roleField
	if e.Role.TeamID != nil {
		fields = append(fields, fieldTeam)
	}
	if e.Role.IceTime != DirectionNone {
		fields = append(fields, fieldIceTime)
	}
	if e.Role.PowerPlay != DirectionNone {
		fields = append(fields, fieldPowerPlay)
	}
	if e.Role.GoalieRole != GoalieRoleNone {
		fields = append(fields, fieldGoalieRole)
	}
	return fields
}

// compareRoleSegments orders reports by effective start; at the same
// start the newer, then more authoritative, report sorts last and wins.
func compareRoleSegments(a, b Event) int {
	if c := a.start().Compare(b.start()); c != 0 {
		return c
	}
	if c := a.ReportedAt.Compare(b.ReportedAt); c != 0 {
		return c
	}
	if c := cmp.Compare(b.authority(), a.authority()); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func joinFields(fields []roleField) string {
	names := make([]string, len(fields))
	for i, field := range fields {
		names[i] = string(field)
	}
	return strings.Join(names, ", ")
}

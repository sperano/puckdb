package newsadjust

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/newsevent"
)

// stepKey identifies one reconciled report: the extraction and article
// version behind an evidence row or a transition. A row naming neither is
// its own step, keyed by its time.
type stepKey struct {
	extraction int64
	version    int64
	at         time.Time
}

// step is everything one reconciled report recorded about an event. Its
// time is the latest of its rows, so a version is visible only once all of
// the report's changes are.
type step struct {
	key         stepKey
	at          time.Time
	evidence    []ExtractedEvidence
	transitions []ExtractedTransition
}

// steps groups an event's evidence rows and transitions by report, oldest
// first.
func steps(e *ExtractedEvent) []step {
	index := make(map[stepKey]int)
	var out []step
	add := func(extraction, version int64, at time.Time) int {
		key := stepKey{extraction: extraction, version: version}
		if extraction == 0 && version == 0 {
			key.at = at.UTC()
		}
		i, exists := index[key]
		if !exists {
			i = len(out)
			index[key] = i
			out = append(out, step{key: key})
		}
		if at.After(out[i].at) {
			out[i].at = at.UTC()
		}
		return i
	}
	for _, ev := range e.Evidence {
		i := add(ev.ExtractionID, ev.VersionID, ev.AddedAt)
		out[i].evidence = append(out[i].evidence, ev)
	}
	for _, t := range e.Transitions {
		i := add(t.ExtractionID, t.VersionID, t.At)
		out[i].transitions = append(out[i].transitions, t)
	}
	slices.SortFunc(out, compareSteps)
	for i := range out {
		slices.SortStableFunc(out[i].transitions, func(a, b ExtractedTransition) int { return a.At.Compare(b.At) })
	}
	return out
}

func compareSteps(a, b step) int {
	if c := a.at.Compare(b.at); c != 0 {
		return c
	}
	if c := cmp.Compare(a.key.extraction, b.key.extraction); c != 0 {
		return c
	}
	if c := cmp.Compare(a.key.version, b.key.version); c != 0 {
		return c
	}
	return a.key.at.Compare(b.key.at)
}

// extractedState is an event as recorded up to one step. resolutions are
// the reports that resolved it.
type extractedState struct {
	at          time.Time
	lifecycle   newsevent.Lifecycle
	supports    []ExtractedEvidence
	resolutions []stepKey
}

func (st *extractedState) advance(s step) {
	st.at = s.at
	for _, t := range s.transitions {
		st.lifecycle = t.To
		if t.To == newsevent.LifecycleResolved {
			st.resolved(t.ExtractionID, t.VersionID)
		}
	}
	for _, ev := range s.evidence {
		switch ev.Relation {
		case newsevent.RelationSupports:
			if !slices.ContainsFunc(st.supports, func(o ExtractedEvidence) bool { return o.VersionID == ev.VersionID }) {
				st.supports = append(st.supports, ev)
			}
		case newsevent.RelationResolves:
			st.resolved(ev.ExtractionID, ev.VersionID)
		}
	}
}

func (st *extractedState) resolved(extraction, version int64) {
	key := stepKey{extraction: extraction, version: version}
	if !slices.Contains(st.resolutions, key) {
		st.resolutions = append(st.resolutions, key)
	}
}

// reportedAt is the earliest report of the event on record.
func (st *extractedState) reportedAt() time.Time {
	var earliest time.Time
	for _, ev := range st.supports {
		if earliest.IsZero() || ev.ReportedAt.Before(earliest) {
			earliest = ev.ReportedAt
		}
	}
	return earliest.UTC()
}

// refs are the article versions supporting the event, one per version,
// each with its first quote.
func (st *extractedState) refs() []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(st.supports))
	for _, ev := range st.supports {
		ref := EvidenceRef{
			VersionID: ev.VersionID, Publisher: ev.Publisher, Kind: ev.Kind, URL: ev.URL,
			ReportedAt: ev.ReportedAt.UTC(), RetrievedAt: ev.RetrievedAt.UTC(),
		}
		if len(ev.Quotes) > 0 {
			ref.Quote = ev.Quotes[0].Text
		}
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b EvidenceRef) int { return cmp.Compare(a.VersionID, b.VersionID) })
	return refs
}

// versions returns the event's adjustment versions: one per step that
// changes what the adjustment sees. It warns when a version cannot form a
// valid event or the event has no supporting evidence.
func (c *converter) versions(src ExtractedEvent) ([]Event, string) {
	var st extractedState
	var out []Event
	var previous string
	var invalid []string
	for _, s := range steps(&src) {
		st.advance(s)
		if len(st.supports) == 0 {
			continue
		}
		ev := c.mapVersion(&src, st)
		content := ev
		content.RecordedAt = time.Time{}
		if hash := hashJSON(content.normalized()); hash != previous {
			previous = hash
			ev.Version = len(out) + 1
			if err := ev.Validate(); err != nil {
				invalid = append(invalid, err.Error())
				continue
			}
			out = append(out, ev)
		}
	}
	switch {
	case len(invalid) > 0:
		return out, fmt.Sprintf("news event %d: dropped versions that do not form a valid event: %s", src.ID, joinHolds(invalid))
	case len(out) == 0:
		return nil, fmt.Sprintf("news event %d has no supporting evidence on record", src.ID)
	}
	return out, ""
}

// mapVersion maps the event as recorded up to st.
func (c *converter) mapVersion(src *ExtractedEvent, st extractedState) Event {
	ev := Event{
		ID: extractedID(src.ID), IncidentID: src.IncidentID, PlayerKey: c.playerKey(src.Event.Player),
		Status: mapStatus(src.Event.Status), Lifecycle: mapLifecycle(st.lifecycle),
		ReportedAt: st.reportedAt(), RecordedAt: st.at, EffectiveFrom: utc(src.Event.EffectiveOn),
		Evidence: st.refs(),
	}
	holds := c.holds(src)
	if hold := c.mapEffect(&ev, src, st); hold != "" {
		holds = append(holds, hold)
	}
	ev.Hold = joinHolds(holds)
	return ev
}

// mapLifecycle maps every lifecycle but resolved, which only an
// availability event with a usable resolution keeps (resolveAvailability).
func mapLifecycle(l newsevent.Lifecycle) Lifecycle {
	switch l {
	case newsevent.LifecycleSuperseded:
		return LifecycleSuperseded
	case newsevent.LifecycleRetracted:
		return LifecycleRetracted
	default:
		return LifecycleActive
	}
}

// mapEffect sets the event's type and effect; it returns a hold reason
// when the event has no numeric mapping.
func (c *converter) mapEffect(ev *Event, src *ExtractedEvent, st extractedState) string {
	switch src.Event.Type {
	case newsevent.TypeInjury, newsevent.TypeSuspension:
		ev.Type = EventInjury
		if src.Event.Type == newsevent.TypeSuspension {
			ev.Type = EventSuspension
		}
		hold := mapDuration(ev, src.Event.Duration)
		if st.lifecycle == newsevent.LifecycleResolved {
			c.resolveAvailability(ev, st)
		}
		return hold
	case newsevent.TypeReinstatement:
		ev.Type = EventReturn
		ev.Supersedes = c.resolvedBy(ev.PlayerKey, st)
		if len(ev.Supersedes) == 0 {
			return "the reinstatement resolves no injury or suspension on record"
		}
		return ""
	default:
		return c.mapChange(ev, src)
	}
}

// resolveAvailability ends an injury or suspension that extraction resolved
// at the player's reinstatement in the resolving report: its stated date,
// else when it was reported. A resolution counts only when that
// reinstatement is not held itself, so a reinstatement awaiting review does
// not lift a penalty either.
func (c *converter) resolveAvailability(ev *Event, st extractedState) {
	var end time.Time
	for _, step := range st.resolutions {
		r, found := c.reinstatement[reportKey{step: step, player: ev.PlayerKey}]
		if !found || len(c.holds(r.event)) > 0 {
			continue
		}
		at := utc(r.event.Event.EffectiveOn)
		if at.IsZero() {
			at = r.reportedAt
		}
		if end.IsZero() || at.Before(end) {
			end = at
		}
	}
	if end.IsZero() {
		return
	}
	ev.Lifecycle = LifecycleResolved
	if ev.EffectiveUntil.IsZero() || end.Before(ev.EffectiveUntil) {
		ev.EffectiveUntil = end
	}
}

// resolvedBy lists the player's events a reinstatement resolved in the
// reports that support it so far.
func (c *converter) resolvedBy(player string, st extractedState) []string {
	var targets []string
	for _, ev := range st.supports {
		key, ok := playerReport(ev.ExtractionID, ev.VersionID, player)
		if !ok {
			continue
		}
		for _, target := range c.resolvedAt[key] {
			if id := extractedID(target); !slices.Contains(targets, id) {
				targets = append(targets, id)
			}
		}
	}
	slices.Sort(targets)
	return targets
}

package newsevent

import (
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// conflictWindow is how close two differing reports of equal standing from
// different publishers must be to count as a conflict. Further apart, the
// later one is taken as an update (a day-to-day injury that became
// week-to-week).
const conflictWindow = 48 * time.Hour

// Relation is how a report bears on an event.
type Relation string

const (
	RelationSupports    Relation = "supports"
	RelationContradicts Relation = "contradicts"
	RelationRetracts    Relation = "retracts"
	RelationResolves    Relation = "resolves"
	RelationWithdraws   Relation = "withdraws"
)

// Report is the article version whose events are reconciled.
type Report struct {
	VersionID    int64
	ArticleID    int64
	Version      int
	Publisher    string
	Kind         news.Kind
	ReportedAt   time.Time
	ExtractionID int64
	// Clean is set when validation kept the reply as it was: only then may
	// a newer version's silence retract what an earlier version said.
	Clean bool
}

// Support is an article version supporting a stored event.
type Support struct {
	ArticleID int64
	VersionID int64
	Publisher string
	Kind      news.Kind
}

// StoredEvent is an event on record.
type StoredEvent struct {
	ID              int64
	Event           Event
	Lifecycle       Lifecycle
	FirstReportedAt time.Time
	LastReportedAt  time.Time
	Support         []Support
}

// EventRef names an event on record (ID) or one the plan creates (New, the
// 1-based index into Plan.Create).
type EventRef struct {
	ID  int64
	New int
}

// NewEvent is an event the plan creates.
type NewEvent struct {
	Event     Event
	Lifecycle Lifecycle
	// SupersededBy is the event on record that already replaces it (a
	// report older than the event, reconciled late).
	SupersededBy int64
	Reason       string
	Review       []string
}

// EvidenceLink attaches the report to an event.
type EvidenceLink struct {
	Ref      EventRef
	Relation Relation
	Quotes   []Quote
}

// Transition changes the lifecycle of an event on record.
type Transition struct {
	EventID      int64
	From         Lifecycle
	To           Lifecycle
	SupersededBy EventRef
	Reason       string
}

// ReviewFlag routes an event on record to review.
type ReviewFlag struct {
	EventID int64
	Reason  string
}

// Plan is what reconciling one report changes. Applying it never edits an
// event's facts: new details are new events, and old ones change lifecycle.
type Plan struct {
	Create      []NewEvent
	Evidence    []EvidenceLink
	Transitions []Transition
	Reviews     []ReviewFlag
	// Touch lists events on record whose reported span widens to the report.
	Touch []int64
}

type planner struct {
	existing []StoredEvent
	r        Report
	window   time.Duration
	plan     Plan
	// handled marks events on record the report already addressed.
	handled map[int64]bool
	// claims are the report's own claims and the events they landed on.
	claims []claimed
}

type claimed struct {
	ref   EventRef
	event Event
}

// Reconcile works out how a report's validated events change the events on
// record. existing holds the players' active events, their other events
// reported within window, and every event an earlier version of the article
// supports. window is how far apart two reports of one event may be; a
// reinstatement resolves active injuries and suspensions of any age.
func Reconcile(existing []StoredEvent, r Report, events []Event, window time.Duration) Plan {
	p := &planner{existing: existing, r: r, window: window, handled: make(map[int64]bool)}
	for _, e := range events {
		if e.Status == StatusDenied {
			p.deny(e)
		} else {
			p.claim(e)
		}
	}
	p.flagInnerConflicts()
	for _, e := range events {
		if e.Type == TypeReinstatement && (e.Status == StatusConfirmed || e.Status == StatusReported) {
			p.reinstate(e)
		}
	}
	p.withdraw()
	return p.plan
}

// source describes the report in reasons.
func (p *planner) source() string {
	return fmt.Sprintf("%s version %d (%s)", p.r.Publisher, p.r.VersionID, p.r.ReportedAt.UTC().Format(dateLayout))
}

// candidates are the active events on record the report may confirm,
// update or contradict.
func (p *planner) candidates(e Event, sameField bool) []*StoredEvent {
	var out []*StoredEvent
	for i := range p.existing {
		c := &p.existing[i]
		if c.Lifecycle != LifecycleActive || p.handled[c.ID] || !samePlayer(c.Event.Player, e.Player) || c.Event.Type != e.Type {
			continue
		}
		if sameField && e.Type.keyedByChange() && c.Event.Change.Field != e.Change.Field {
			continue
		}
		if p.supportedByArticle(c) || p.near(c) {
			out = append(out, c)
		}
	}
	return out
}

func (p *planner) near(c *StoredEvent) bool {
	t := p.r.ReportedAt
	return !t.Before(c.FirstReportedAt.Add(-p.window)) && !t.After(c.LastReportedAt.Add(p.window))
}

func (p *planner) supportedByArticle(c *StoredEvent) bool {
	return slices.ContainsFunc(c.Support, func(s Support) bool { return s.ArticleID == p.r.ArticleID })
}

// onlyThisArticle reports whether the article is the event's only support.
func (p *planner) onlyThisArticle(c *StoredEvent) bool {
	return len(c.Support) > 0 && !slices.ContainsFunc(c.Support, func(s Support) bool { return s.ArticleID != p.r.ArticleID })
}

func (p *planner) onlyThisPublisher(c *StoredEvent) bool {
	return len(c.Support) > 0 && !slices.ContainsFunc(c.Support, func(s Support) bool { return s.Publisher != p.r.Publisher })
}

// claim reconciles a reported event with the matching events on record.
func (p *planner) claim(e Event) {
	cands := p.candidates(e, true)
	for _, c := range cands {
		if c.Event.sameClaim(e) {
			p.support(c, e)
			p.claims = append(p.claims, claimed{ref: EventRef{ID: c.ID}, event: e})
			return
		}
	}
	if len(cands) == 0 {
		p.create(e, LifecycleActive, 0, "reported by "+p.source())
		return
	}
	latest := slices.MaxFunc(cands, func(a, b *StoredEvent) int { return a.FirstReportedAt.Compare(b.FirstReportedAt) })
	switch {
	case p.r.ReportedAt.Before(latest.FirstReportedAt) && !p.onlyThisArticle(latest):
		p.create(e, LifecycleSuperseded, latest.ID, fmt.Sprintf("reported before event %d, reconciled later", latest.ID))
	case p.updates(e, latest):
		ref := p.create(e, LifecycleActive, 0, "update reported by "+p.source())
		for _, c := range cands {
			p.transition(c, LifecycleSuperseded, ref, "updated by "+p.source())
		}
	default:
		ref := p.create(e, LifecycleActive, 0, "reported by "+p.source())
		p.plan.Create[ref.New-1].Review = append(p.plan.Create[ref.New-1].Review,
			fmt.Sprintf("conflicts with event %d", latest.ID))
		for _, c := range cands {
			p.handled[c.ID] = true
			p.review(c.ID, "conflicting report by "+p.source())
		}
	}
}

// updates reports whether a report with other details replaces the latest
// event rather than conflicting with it: a newer version of the same
// article, a firmer report, or an equally firm one from the same publisher,
// from an official source, or well after the event. Another extractor
// reading the very version the event rests on differently is a
// disagreement between models, never an update.
func (p *planner) updates(e Event, latest *StoredEvent) bool {
	if p.supportedByThisVersion(latest) {
		return false
	}
	have, got := latest.Event.Status.rank(), e.Status.rank()
	if p.onlyThisArticle(latest) || got > have {
		return true
	}
	return got == have && (p.onlyThisPublisher(latest) || p.r.Kind == news.KindOfficial ||
		p.r.ReportedAt.Sub(latest.LastReportedAt) > conflictWindow)
}

func (p *planner) support(c *StoredEvent, e Event) {
	p.handled[c.ID] = true
	p.link(EventRef{ID: c.ID}, RelationSupports, e.Evidence)
	p.plan.Touch = append(p.plan.Touch, c.ID)
	if e.NeedsReview {
		p.review(c.ID, e.ReviewReason)
	}
}

// create adds an event and returns its reference.
func (p *planner) create(e Event, lifecycle Lifecycle, supersededBy int64, reason string) EventRef {
	n := NewEvent{Event: e, Lifecycle: lifecycle, SupersededBy: supersededBy, Reason: reason}
	if e.NeedsReview {
		n.Review = append(n.Review, e.ReviewReason)
	}
	p.plan.Create = append(p.plan.Create, n)
	ref := EventRef{New: len(p.plan.Create)}
	p.claims = append(p.claims, claimed{ref: ref, event: e})
	p.link(ref, RelationSupports, e.Evidence)
	if lifecycle == LifecycleActive && (e.Type == TypeInjury || e.Type == TypeSuspension) {
		p.endReinstatements(e, ref)
	}
	return ref
}

// endReinstatements supersedes the player's earlier reinstatement: a new
// injury or suspension is the current word on availability.
func (p *planner) endReinstatements(e Event, ref EventRef) {
	for i := range p.existing {
		c := &p.existing[i]
		if c.Lifecycle == LifecycleActive && !p.handled[c.ID] && c.Event.Type == TypeReinstatement &&
			samePlayer(c.Event.Player, e.Player) && !c.LastReportedAt.After(p.r.ReportedAt) {
			p.transition(c, LifecycleSuperseded, ref, fmt.Sprintf("a later %s was reported by %s", e.Type, p.source()))
		}
	}
}

func (p *planner) link(ref EventRef, relation Relation, quotes []Quote) {
	p.plan.Evidence = append(p.plan.Evidence, EvidenceLink{Ref: ref, Relation: relation, Quotes: quotes})
}

func (p *planner) transition(c *StoredEvent, to Lifecycle, by EventRef, reason string) {
	p.handled[c.ID] = true
	p.plan.Transitions = append(p.plan.Transitions, Transition{EventID: c.ID, From: c.Lifecycle, To: to, SupersededBy: by, Reason: reason})
}

func (p *planner) review(id int64, reason string) {
	p.plan.Reviews = append(p.plan.Reviews, ReviewFlag{EventID: id, Reason: reason})
}

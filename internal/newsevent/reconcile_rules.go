package newsevent

import (
	"fmt"
	"slices"

	"github.com/sperano/puckdb/internal/news"
)

// reviewInnerConflict is the review reason of claims one report makes with
// differing details.
const reviewInnerConflict = "the same report states conflicting details"

// deny reconciles a denial or correction. It retracts the events it denies
// when it has the authority to (a newer version of the only supporting
// article, an official source, the same publisher, or a denial of a mere
// rumor); otherwise the events go to review.
func (p *planner) deny(e Event) {
	for _, c := range p.candidates(e, false) {
		if p.authoritativeDenial(c) {
			p.transition(c, LifecycleRetracted, EventRef{}, "denied or corrected by "+p.source())
			p.link(EventRef{ID: c.ID}, RelationRetracts, e.Evidence)
			continue
		}
		p.handled[c.ID] = true
		p.review(c.ID, "contradicted by "+p.source())
		p.link(EventRef{ID: c.ID}, RelationContradicts, e.Evidence)
	}
}

func (p *planner) authoritativeDenial(c *StoredEvent) bool {
	return p.onlyThisArticle(c) || p.onlyThisPublisher(c) || p.r.Kind == news.KindOfficial ||
		c.Event.Status == StatusRumor
}

// reinstate resolves the player's injuries and suspensions reported no later
// than the reinstatement, including those the same report states.
func (p *planner) reinstate(e Event) {
	for i := range p.existing {
		c := &p.existing[i]
		if c.Lifecycle != LifecycleActive || !resolvable(c.Event, e) || c.FirstReportedAt.After(p.r.ReportedAt) {
			continue
		}
		if slices.ContainsFunc(p.plan.Transitions, func(t Transition) bool { return t.EventID == c.ID }) {
			continue
		}
		p.transition(c, LifecycleResolved, EventRef{}, "reinstatement reported by "+p.source())
		p.link(EventRef{ID: c.ID}, RelationResolves, e.Evidence)
	}
	for k := range p.plan.Create {
		n := &p.plan.Create[k]
		if n.Lifecycle == LifecycleActive && resolvable(n.Event, e) {
			n.Lifecycle = LifecycleResolved
			n.Reason += "; the same report states the reinstatement"
		}
	}
}

func resolvable(ev, reinstatement Event) bool {
	return (ev.Type == TypeInjury || ev.Type == TypeSuspension) && samePlayer(ev.Player, reinstatement.Player)
}

// flagInnerConflicts routes to review the claims one report makes about the
// same subject with differing details.
func (p *planner) flagInnerConflicts() {
	for i, a := range p.claims {
		for _, b := range p.claims[i+1:] {
			if !a.event.sameSubject(b.event) || a.event.sameClaim(b.event) {
				continue
			}
			p.flagRef(a.ref, reviewInnerConflict)
			p.flagRef(b.ref, reviewInnerConflict)
		}
	}
}

func (p *planner) flagRef(ref EventRef, reason string) {
	if ref.New > 0 {
		n := &p.plan.Create[ref.New-1]
		if !slices.Contains(n.Review, reason) {
			n.Review = append(n.Review, reason)
		}
		return
	}
	p.review(ref.ID, reason)
}

// withdraw handles the events an earlier version of this article supports
// that this version no longer states. A clean reading of a newer version
// retracts an event the article alone supported and records the withdrawal
// otherwise; a reading with issues, or a new extractor disagreeing about the
// same version, only routes the event to review.
func (p *planner) withdraw() {
	for i := range p.existing {
		c := &p.existing[i]
		if c.Lifecycle != LifecycleActive || p.handled[c.ID] || !p.supportedByArticle(c) {
			continue
		}
		if p.supportedByThisVersion(c) {
			p.review(c.ID, fmt.Sprintf("extraction %d no longer finds it in version %d", p.r.ExtractionID, p.r.VersionID))
			continue
		}
		if !p.r.Clean {
			p.review(c.ID, fmt.Sprintf("version %d of a supporting article could not be read cleanly", p.r.VersionID))
			continue
		}
		p.link(EventRef{ID: c.ID}, RelationWithdraws, nil)
		if p.onlyThisArticle(c) {
			p.transition(c, LifecycleRetracted, EventRef{},
				fmt.Sprintf("version %d of the article no longer reports it (%s)", p.r.Version, p.source()))
		}
	}
}

func (p *planner) supportedByThisVersion(c *StoredEvent) bool {
	return slices.ContainsFunc(c.Support, func(s Support) bool { return s.VersionID == p.r.VersionID })
}

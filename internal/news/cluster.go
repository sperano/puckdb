package news

import (
	"time"
)

// Relation says how a report relates to the evidence already behind an
// incident. Only independent reports are separate sources; the others repeat
// one and never add weight to the incident.
type Relation string

const (
	// RelationIndependent: first report from its publisher that is not a
	// copy of another report.
	RelationIndependent Relation = "independent"
	// RelationSyndicated: same title or text as a report already attached
	// (wire copy, team-site mirror, reposted story).
	RelationSyndicated Relation = "syndicated"
	// RelationSamePublisher: a follow-up from a publisher already attached.
	RelationSamePublisher Relation = "same_publisher"
	// RelationRevision: a newer version of an article already attached.
	RelationRevision Relation = "revision"
)

// Report is one article version reporting a category for one player.
type Report struct {
	VersionID        int64
	ArticleID        int64
	Publisher        string
	Kind             Kind
	ReportedAt       time.Time
	TitleFingerprint string
	TextFingerprint  string
	Category         Category
}

// EvidenceKey is what relating a report needs from attached evidence.
type EvidenceKey struct {
	IncidentID       int64
	VersionID        int64
	ArticleID        int64
	Publisher        string
	TitleFingerprint string
	TextFingerprint  string
}

// IncidentSpan is an incident's category and reported span.
type IncidentSpan struct {
	ID       int64
	Category Category
	First    time.Time
	Last     time.Time
}

// Relate classifies a report against an incident's evidence and returns the
// version it repeats (0 when independent).
func Relate(r Report, evidence []EvidenceKey) (Relation, int64) {
	for _, e := range evidence {
		if e.ArticleID == r.ArticleID {
			return RelationRevision, e.VersionID
		}
	}
	for _, e := range evidence {
		if sameFingerprint(e.TitleFingerprint, r.TitleFingerprint) || sameFingerprint(e.TextFingerprint, r.TextFingerprint) {
			return RelationSyndicated, e.VersionID
		}
	}
	for _, e := range evidence {
		if e.Publisher == r.Publisher {
			return RelationSamePublisher, e.VersionID
		}
	}
	return RelationIndependent, 0
}

func sameFingerprint(a, b string) bool {
	return a != "" && a == b
}

// closedBy lists the categories whose later report ends an incident: after a
// reinstatement, a new injury report is a new incident.
var closedBy = map[Category]Category{
	CategoryInjury:     CategoryReinstatement,
	CategorySuspension: CategoryReinstatement,
}

// ChooseIncident picks the incident a report belongs to among the player's
// incidents near the report: same category, reported within window of the
// incident's span, and not closed by a later report (a reinstatement)
// before this one. A revision joins its earlier version's incident; failing
// that, the nearest incident wins. It returns false when a new incident is
// needed.
func ChooseIncident(r Report, incidents []IncidentSpan, evidence []EvidenceKey, window time.Duration) (int64, bool) {
	var best int64
	bestDistance := time.Duration(-1)
	for _, inc := range incidents {
		if inc.Category != r.Category || !withinWindow(r.ReportedAt, inc, window) || closed(inc, r.ReportedAt, incidents) {
			continue
		}
		for _, e := range evidence {
			if e.IncidentID == inc.ID && e.ArticleID == r.ArticleID {
				return inc.ID, true
			}
		}
		if d := distance(r.ReportedAt, inc); bestDistance < 0 || d < bestDistance {
			best, bestDistance = inc.ID, d
		}
	}
	return best, bestDistance >= 0
}

func withinWindow(t time.Time, inc IncidentSpan, window time.Duration) bool {
	return !t.Before(inc.First.Add(-window)) && !t.After(inc.Last.Add(window))
}

// closed reports whether a closing incident was reported after inc and no
// later than t.
func closed(inc IncidentSpan, t time.Time, incidents []IncidentSpan) bool {
	closing, ok := closedBy[inc.Category]
	if !ok {
		return false
	}
	for _, other := range incidents {
		if other.Category == closing && other.First.After(inc.Last) && !other.First.After(t) {
			return true
		}
	}
	return false
}

func distance(t time.Time, inc IncidentSpan) time.Duration {
	switch {
	case t.Before(inc.First):
		return inc.First.Sub(t)
	case t.After(inc.Last):
		return t.Sub(inc.Last)
	default:
		return 0
	}
}

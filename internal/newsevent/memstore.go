package newsevent

import (
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// Outcome counts what applying plans did.
type Outcome struct {
	// Created counts new events; CreatedSuperseded those already replaced
	// by an event on record when created (a late, older report).
	Created           int `json:"created"`
	CreatedSuperseded int `json:"createdSuperseded"`
	// Supported counts reports attached to an event on record that states
	// the same facts (duplicates, syndicated copies, other extractors).
	Supported  int `json:"supported"`
	Superseded int `json:"superseded"`
	Retracted  int `json:"retracted"`
	Resolved   int `json:"resolved"`
	Reviews    int `json:"reviews"`
}

// Add accumulates another outcome.
func (o *Outcome) Add(other Outcome) {
	o.Created += other.Created
	o.CreatedSuperseded += other.CreatedSuperseded
	o.Supported += other.Supported
	o.Superseded += other.Superseded
	o.Retracted += other.Retracted
	o.Resolved += other.Resolved
	o.Reviews += other.Reviews
}

// countTransition counts a lifecycle change.
func (o *Outcome) countTransition(to Lifecycle) {
	switch to {
	case LifecycleSuperseded:
		o.Superseded++
	case LifecycleRetracted:
		o.Retracted++
	case LifecycleResolved:
		o.Resolved++
	}
}

// MemoryStore keeps events in memory. The evaluation harness reconciles
// reports with it the same way the database store does.
type MemoryStore struct {
	// Events holds every event; an event's ID is its index plus one.
	Events []StoredEvent
	// Reviews holds each event's review reasons.
	Reviews map[int64][]string
}

// Near returns the events a report is reconciled against: the players'
// active events and their other events reported within window of it, and
// every event its article supports.
func (m *MemoryStore) Near(players []news.Identity, r Report, window time.Duration) []StoredEvent {
	var out []StoredEvent
	for _, ev := range m.Events {
		supported := slices.ContainsFunc(ev.Support, func(s Support) bool { return s.ArticleID == r.ArticleID })
		recent := ev.Lifecycle == LifecycleActive || !ev.LastReportedAt.Before(r.ReportedAt.Add(-window))
		named := slices.ContainsFunc(players, func(p news.Identity) bool { return samePlayer(p, ev.Event.Player) })
		if supported || (recent && named) {
			ev.Support = slices.Clone(ev.Support)
			out = append(out, ev)
		}
	}
	return out
}

// Apply records a plan.
func (m *MemoryStore) Apply(plan Plan, r Report) Outcome {
	if m.Reviews == nil {
		m.Reviews = make(map[int64][]string)
	}
	var out Outcome
	ids := make([]int64, len(plan.Create))
	for k, n := range plan.Create {
		id := int64(len(m.Events) + 1)
		ids[k] = id
		m.Events = append(m.Events, StoredEvent{
			ID: id, Event: n.Event, Lifecycle: n.Lifecycle, FirstReportedAt: r.ReportedAt, LastReportedAt: r.ReportedAt,
		})
		countCreated(&out, n)
		for _, reason := range n.Review {
			m.review(id, reason, &out)
		}
	}
	resolve := func(ref EventRef) int64 {
		if ref.New > 0 {
			return ids[ref.New-1]
		}
		return ref.ID
	}
	for _, link := range plan.Evidence {
		if link.Relation == RelationSupports {
			m.support(resolve(link.Ref), r, link.Ref.New == 0, &out)
		}
	}
	for _, t := range plan.Transitions {
		m.Events[t.EventID-1].Lifecycle = t.To
		out.countTransition(t.To)
	}
	for _, f := range plan.Reviews {
		m.review(f.EventID, f.Reason, &out)
	}
	return out
}

func countCreated(out *Outcome, n NewEvent) {
	switch n.Lifecycle {
	case LifecycleSuperseded:
		out.CreatedSuperseded++
	default:
		out.Created++
	}
}

func (m *MemoryStore) support(id int64, r Report, onRecord bool, out *Outcome) {
	ev := &m.Events[id-1]
	s := Support{ArticleID: r.ArticleID, VersionID: r.VersionID, Publisher: r.Publisher, Kind: r.Kind}
	if !slices.Contains(ev.Support, s) {
		ev.Support = append(ev.Support, s)
	}
	if r.ReportedAt.Before(ev.FirstReportedAt) {
		ev.FirstReportedAt = r.ReportedAt
	}
	if r.ReportedAt.After(ev.LastReportedAt) {
		ev.LastReportedAt = r.ReportedAt
	}
	if onRecord {
		out.Supported++
	}
}

func (m *MemoryStore) review(id int64, reason string, out *Outcome) {
	if !slices.Contains(m.Reviews[id], reason) {
		m.Reviews[id] = append(m.Reviews[id], reason)
		out.Reviews++
	}
}

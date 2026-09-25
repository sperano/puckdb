package news

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// FetchState is a source's recorded fetch coverage for one scope.
type FetchState struct {
	SourceID            string
	Scope               string
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	DataAsOf            time.Time
	LastFailureAt       time.Time
	LastError           string
	ConsecutiveFailures int
	LastItems           int
	LastNewVersions     int
}

// CoverageStatus says whether a source's data can be relied on as current.
type CoverageStatus string

const (
	// CoverageFresh: the last fetch succeeded and its data is recent.
	CoverageFresh CoverageStatus = "fresh"
	// CoverageFailing: recent fetches failed but the last good data is
	// still within the stale threshold.
	CoverageFailing CoverageStatus = "failing"
	// CoverageStale: the newest good data is older than the threshold.
	CoverageStale CoverageStatus = "stale"
	// CoverageMissing: the source never fetched successfully.
	CoverageMissing CoverageStatus = "missing"
)

// SourceCoverage is one enabled source's coverage.
type SourceCoverage struct {
	Source Source
	Scope  string
	State  FetchState
	Status CoverageStatus
	// AsOf is when the newest good data was current (zero when missing).
	AsOf time.Time
}

// Current reports whether the source's data is within its stale threshold.
func (c SourceCoverage) Current() bool {
	return c.Status == CoverageFresh || c.Status == CoverageFailing
}

// EvaluateCoverage rates every source against its recorded fetch state.
func EvaluateCoverage(sources []Source, states []FetchState, season int, now time.Time) []SourceCoverage {
	type key struct{ source, scope string }
	byKey := make(map[key]FetchState, len(states))
	for _, s := range states {
		byKey[key{s.SourceID, s.Scope}] = s
	}
	out := make([]SourceCoverage, 0, len(sources))
	for _, src := range sources {
		scope := src.Scope(season)
		state := byKey[key{src.ID, scope}]
		coverage := SourceCoverage{Source: src, Scope: scope, State: state}
		coverage.AsOf = state.DataAsOf
		if coverage.AsOf.IsZero() {
			coverage.AsOf = state.LastSuccessAt
		}
		switch {
		case state.LastSuccessAt.IsZero():
			coverage.Status, coverage.AsOf = CoverageMissing, time.Time{}
		case now.Sub(coverage.AsOf) > src.StaleAfter():
			coverage.Status = CoverageStale
		case state.ConsecutiveFailures > 0:
			coverage.Status = CoverageFailing
		default:
			coverage.Status = CoverageFresh
		}
		out = append(out, coverage)
	}
	return out
}

// Evidence is one report behind an incident.
type Evidence struct {
	Relation        Relation
	Publisher       string
	Kind            Kind
	SourceID        string
	Title           string
	Text            string
	Author          string
	URL             string
	PublishedAt     time.Time
	SourceUpdatedAt time.Time
	RetrievedAt     time.Time
	ReportedAt      time.Time
	Version         int
	LatestVersion   int
}

// Outdated reports whether the article has a newer version than this one.
func (e Evidence) Outdated() bool {
	return e.Version < e.LatestVersion
}

// Incident is an incident candidate with its evidence in report order.
type Incident struct {
	ID              int64
	NHLPlayerID     int64
	YahooPlayerID   int
	PlayerName      string
	Category        Category
	FirstReportedAt time.Time
	LastReportedAt  time.Time
	Evidence        []Evidence
}

// IndependentPublishers lists the publishers of independent reports, in
// report order. Repeats and copies are not counted.
func (i Incident) IndependentPublishers() []string {
	var out []string
	for _, e := range i.Evidence {
		if e.Relation == RelationIndependent && !slices.Contains(out, e.Publisher) {
			out = append(out, e.Publisher)
		}
	}
	return out
}

// Superseded reports whether every report behind the incident has a newer
// version that no longer supports it (a correction). A newer structured
// status (Yahoo) is a change of state, not a correction of the earlier one,
// so an incident with structured evidence is never superseded.
func (i Incident) Superseded() bool {
	for _, e := range i.Evidence {
		if e.Kind == KindStructured || !e.Outdated() {
			return false
		}
	}
	return len(i.Evidence) > 0
}

// PlayerNews is what the stored news says about one player, with the
// coverage it rests on.
type PlayerNews struct {
	Player    Identity
	Incidents []Incident
	Coverage  []SourceCoverage
}

// absenceCaveat is appended whenever no incident is on record.
const absenceCaveat = "The absence of news is not evidence that the player is healthy or available."

// Assessment summarizes the player's news without ever inferring health
// from silence.
func (p PlayerNews) Assessment() string {
	if len(p.Incidents) > 0 {
		return fmt.Sprintf("%d incident candidate(s) on record; read the evidence before acting on it.", len(p.Incidents))
	}
	var gaps []string
	for _, c := range p.Coverage {
		if !c.Current() {
			gaps = append(gaps, fmt.Sprintf("%s %s", c.Source.ID, c.Status))
		}
	}
	if len(gaps) == 0 {
		return "No incident on record from the covered sources. " + absenceCaveat
	}
	return fmt.Sprintf("No incident on record, but coverage is incomplete (%s). %s", strings.Join(gaps, ", "), absenceCaveat)
}

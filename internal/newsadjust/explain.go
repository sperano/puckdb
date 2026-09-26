package newsadjust

import (
	"time"

	"github.com/sperano/puckdb/internal/projection"
)

// Reason is one event behind a player's adjustment, with its evidence and
// how fresh that evidence was at the as-of time.
type Reason struct {
	EventID          string        `json:"event_id"`
	Version          int           `json:"version"`
	IncidentID       int64         `json:"incident_id,omitempty"`
	Type             EventType     `json:"type"`
	Status           ReportStatus  `json:"status"`
	Duration         Duration      `json:"duration"`
	Role             *RoleChange   `json:"role,omitempty"`
	EffectiveFrom    time.Time     `json:"effective_from"`
	EffectiveUntil   time.Time     `json:"effective_until,omitzero"`
	Outcome          Outcome       `json:"outcome"`
	Detail           string        `json:"detail"`
	Scenarios        []Scenario    `json:"scenarios,omitempty"`
	Evidence         []EvidenceRef `json:"evidence"`
	LatestEvidenceAt time.Time     `json:"latest_evidence_at"`
	// AgeHours is how old the newest evidence was at the as-of time.
	AgeHours float64 `json:"age_hours"`
}

// StatChange is a stat's baseline estimate and its value in each scenario.
type StatChange struct {
	Stat     projection.Stat                  `json:"stat"`
	Baseline projection.Estimate              `json:"baseline"`
	Adjusted map[Scenario]projection.Estimate `json:"adjusted"`
}

// PlayerAdjustment explains everything news and overrides did to one
// player: reasons, per-scenario effects, changed stats, the overrides with
// the values they replaced, labeled assumptions and alerts.
type PlayerAdjustment struct {
	PlayerKey           string                `json:"player_key"`
	Kind                projection.PlayerKind `json:"kind"`
	Reasons             []Reason              `json:"reasons,omitempty"`
	Effects             map[Scenario]Effect   `json:"effects"`
	Changes             []StatChange          `json:"changes,omitempty"`
	Overrides           []AppliedOverride     `json:"overrides,omitempty"`
	Assumptions         []string              `json:"assumptions,omitempty"`
	Alerts              []string              `json:"alerts,omitempty"`
	BaselineUncertainty float64               `json:"baseline_uncertainty"`
	Uncertainty         float64               `json:"uncertainty"`
}

func reasonFor(e Event, d Decision, asOf time.Time) Reason {
	latest := e.latestEvidence()
	age := 0.0
	if !latest.IsZero() {
		age = asOf.Sub(latest).Hours()
	}
	return Reason{
		EventID: e.ID, Version: e.Version, IncidentID: e.IncidentID, Type: e.Type, Status: e.Status,
		Duration: e.Duration, Role: e.Role, EffectiveFrom: e.start(), EffectiveUntil: e.EffectiveUntil,
		Outcome: d.Outcome, Detail: d.Reason, Scenarios: d.Scenarios, Evidence: e.Evidence,
		LatestEvidenceAt: latest, AgeHours: age,
	}
}

// statChanges lists the stats whose value differs in any scenario.
func statChanges(baseline projection.PlayerProjection, adjusted map[Scenario]map[projection.Stat]projection.Estimate) []StatChange {
	var changes []StatChange
	for _, stat := range sortedStats(baseline.Values) {
		change := StatChange{Stat: stat, Baseline: baseline.Values[stat], Adjusted: make(map[Scenario]projection.Estimate, len(Scenarios))}
		changed := false
		for _, s := range Scenarios {
			change.Adjusted[s] = adjusted[s][stat]
			changed = changed || adjusted[s][stat] != baseline.Values[stat]
		}
		if changed {
			changes = append(changes, change)
		}
	}
	return changes
}

package newsadjust

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/projection"
)

// storedTimePrecision is PostgreSQL's timestamp precision. As-of times are
// kept to it so a stored adjustment replays with the same identity.
const storedTimePrecision = time.Microsecond

// Request is everything one adjustment reads. Events may include versions
// recorded after AsOf; they are ignored, which is what makes a replay at a
// historical as-of time reproduce what was known then.
type Request struct {
	Baseline  projection.Snapshot
	Events    []Event
	Overrides []Override
	Policy    Policy
	Season    Season
	AsOf      time.Time
	// LeagueKey selects league-scoped overrides; empty applies only
	// overrides that cover every league.
	LeagueKey string
	// CoverageWarnings are news-coverage gaps at AsOf (stale or missing
	// sources); they are carried into the result's alerts.
	CoverageWarnings []string
}

// Result is one adjustment: an adjusted projection snapshot per scenario
// and the audit trail needed to explain and replay it.
type Result struct {
	ID            string                           `json:"id"`
	MethodVersion string                           `json:"method_version"`
	Policy        Policy                           `json:"policy"`
	PolicyHash    string                           `json:"policy_hash"`
	BaselineHash  string                           `json:"baseline_hash"`
	Season        Season                           `json:"season"`
	AsOf          time.Time                        `json:"as_of"`
	LeagueKey     string                           `json:"league_key,omitempty"`
	Snapshots     map[Scenario]projection.Snapshot `json:"-"`
	Players       []PlayerAdjustment               `json:"players"`
	Decisions     []Decision                       `json:"decisions"`
	Events        []Event                          `json:"events"`
	Overrides     []Override                       `json:"overrides"`
	Shadowed      []Override                       `json:"shadowed_overrides,omitempty"`
	Alerts        []string                         `json:"alerts,omitempty"`
}

// Apply adjusts a baseline snapshot for the news and overrides known at
// req.AsOf. The baseline is never modified.
func Apply(req Request) (Result, error) {
	req.Season.Start, req.Season.End = utc(req.Season.Start), utc(req.Season.End)
	req.AsOf = utc(req.AsOf).Truncate(storedTimePrecision)
	if len(req.CoverageWarnings) == 0 {
		req.CoverageWarnings = nil
	}
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	players, err := indexPlayers(req.Baseline.Players)
	if err != nil {
		return Result{}, err
	}
	active, shadowed := activeOverrides(req.Overrides, req.AsOf, req.LeagueKey)
	visible, err := visibleEvents(req.Events, req.AsOf)
	if err != nil {
		return Result{}, err
	}
	sel := newSelector(req, players, visible, active)
	claims := sel.buildClaims()
	result := Result{
		MethodVersion: MethodVersion, Policy: req.Policy, PolicyHash: req.Policy.Hash(),
		BaselineHash: req.Baseline.SourceDataHash, Season: req.Season, AsOf: req.AsOf.UTC(),
		LeagueKey: req.LeagueKey, Decisions: sel.sortedDecisions(), Events: visible,
		Overrides: active, Shadowed: shadowed, Alerts: slices.Clone(req.CoverageWarnings),
	}
	result.ID = adjustmentID(req, result)
	adjusted := make(map[Scenario][]projection.PlayerProjection, len(Scenarios))
	for _, player := range req.Baseline.Players {
		adjustment, byScenario := adjustPlayer(req, player, claims[player.PlayerKey], active, sel)
		for _, s := range Scenarios {
			adjusted[s] = append(adjusted[s], byScenario[s])
		}
		if adjustment != nil {
			result.Players = append(result.Players, *adjustment)
		}
	}
	result.Snapshots = scenarioSnapshots(req, result.ID, adjusted)
	return result, nil
}

func validateRequest(req Request) error {
	switch {
	case req.AsOf.IsZero():
		return fmt.Errorf("adjustment as-of time is required")
	case req.Baseline.SourceDataHash == "" || req.Baseline.Config.ModelVersion == "":
		return fmt.Errorf("baseline snapshot needs its source hash and model version")
	case req.Baseline.AsOf.After(req.AsOf):
		return fmt.Errorf("baseline snapshot as-of %s is after the adjustment as-of %s", req.Baseline.AsOf.UTC(), req.AsOf.UTC())
	}
	if err := req.Policy.Validate(); err != nil {
		return err
	}
	if err := req.Season.validate(); err != nil {
		return err
	}
	ids := make(map[string]bool, len(req.Overrides))
	for _, o := range req.Overrides {
		if err := o.Validate(); err != nil {
			return err
		}
		if ids[o.ID] {
			return fmt.Errorf("duplicate override %s", o.ID)
		}
		ids[o.ID] = true
	}
	return nil
}

func indexPlayers(players []projection.PlayerProjection) (map[string]projection.PlayerProjection, error) {
	byKey := make(map[string]projection.PlayerProjection, len(players))
	for _, player := range players {
		if _, exists := byKey[player.PlayerKey]; exists {
			return nil, fmt.Errorf("duplicate projection %q", player.PlayerKey)
		}
		byKey[player.PlayerKey] = player
	}
	return byKey, nil
}

// adjustPlayer returns the player's projection in each scenario and, when
// anything touched the player, the explanation.
func adjustPlayer(req Request, player projection.PlayerProjection, claims *playerClaims, active []Override, sel *selector) (*PlayerAdjustment, map[Scenario]projection.PlayerProjection) {
	overrides := playerOverrides(active, player.PlayerKey)
	reasons := playerReasons(sel, player.PlayerKey, req.AsOf)
	byScenario := make(map[Scenario]projection.PlayerProjection, len(Scenarios))
	if claims == nil && len(overrides) == 0 && len(reasons) == 0 && len(sel.alerts[player.PlayerKey]) == 0 {
		for _, s := range Scenarios {
			byScenario[s] = player
		}
		return nil, byScenario
	}
	in := effectInputs{player: player, claims: claims, overrides: overrides, season: req.Season, policy: req.Policy}
	adjustment := &PlayerAdjustment{
		PlayerKey: player.PlayerKey, Kind: player.Kind, Reasons: reasons,
		Effects: make(map[Scenario]Effect, len(Scenarios)), BaselineUncertainty: player.Uncertainty,
		Alerts: slices.Clone(sel.alerts[player.PlayerKey]),
	}
	values := make(map[Scenario]map[projection.Stat]projection.Estimate, len(Scenarios))
	for _, s := range Scenarios {
		effect := computeEffect(in, s)
		adjustment.Effects[s] = effect.effect
		adjustment.Overrides = append(adjustment.Overrides, effect.applied...)
		adjustment.Assumptions = appendUnique(adjustment.Assumptions, effect.assumptions...)
		adjustment.Alerts = appendUnique(adjustment.Alerts, effect.alerts...)
		values[s] = adjustedValues(player, effect.effect, req.Season.Games)
	}
	if claims != nil && len(claims.availability) > 0 {
		adjustment.Assumptions = appendUnique(adjustment.Assumptions, scheduleAssumption)
	}
	adjustment.Uncertainty = widenedUncertainty(player.Uncertainty, req.Baseline.Config.MaximumUncertainty, player.Kind, adjustment.Effects)
	adjustment.Changes = statChanges(player, values)
	for _, s := range Scenarios {
		adjusted := player
		adjusted.Values = envelope(values, s)
		adjusted.Uncertainty = adjustment.Uncertainty
		if team := adjustment.Effects[s].TeamID; team != nil {
			adjusted.TeamID = team
		}
		byScenario[s] = adjusted
	}
	return adjustment, byScenario
}

func playerOverrides(active []Override, playerKey string) []Override {
	var out []Override
	for _, o := range active {
		if o.PlayerKey == playerKey && o.Kind != OverrideExcludeEvent {
			out = append(out, o)
		}
	}
	return out
}

func playerReasons(sel *selector, playerKey string, asOf time.Time) []Reason {
	var reasons []Reason
	for _, e := range sel.visible {
		if d, exists := sel.decisions[e.ID]; exists && e.PlayerKey == playerKey {
			reasons = append(reasons, reasonFor(e, d, asOf))
		}
	}
	return reasons
}

func appendUnique(values []string, more ...string) []string {
	for _, value := range more {
		if !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}

func sortedStats(values map[projection.Stat]projection.Estimate) []projection.Stat {
	stats := make([]projection.Stat, 0, len(values))
	for stat := range values {
		stats = append(stats, stat)
	}
	slices.Sort(stats)
	return stats
}

// adjustmentID hashes every input that can change the adjusted snapshots.
func adjustmentID(req Request, result Result) string {
	overrides := slices.Clone(result.Overrides)
	slices.SortFunc(overrides, func(a, b Override) int { return cmp.Compare(a.ID, b.ID) })
	identity := struct {
		Method           string
		PolicyHash       string
		BaselineHash     string
		BaselineConfig   projection.Config
		BaselineAsOf     time.Time
		AsOf             time.Time
		LeagueKey        string
		Season           Season
		Events           []Event
		Overrides        []Override
		CoverageWarnings []string
	}{
		MethodVersion, result.PolicyHash, req.Baseline.SourceDataHash, req.Baseline.Config,
		utc(req.Baseline.AsOf).Truncate(storedTimePrecision), req.AsOf, req.LeagueKey, req.Season, result.Events,
		overrides, req.CoverageWarnings,
	}
	return MethodVersion + ":" + hashJSON(identity)
}

func scenarioSnapshots(req Request, id string, adjusted map[Scenario][]projection.PlayerProjection) map[Scenario]projection.Snapshot {
	snapshots := make(map[Scenario]projection.Snapshot, len(Scenarios))
	for _, s := range Scenarios {
		snapshot := req.Baseline
		snapshot.AsOf = req.AsOf.UTC()
		snapshot.SourceDataHash = hashJSON(struct {
			AdjustmentID string
			Scenario     Scenario
		}{id, s})
		snapshot.Players = adjusted[s]
		snapshots[s] = snapshot
	}
	return snapshots
}

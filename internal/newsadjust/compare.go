package newsadjust

import (
	"fmt"
	"slices"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/projection"
)

// Placement is a player's position in one ranking.
type Placement struct {
	Rank          int     `json:"rank"`
	Score         float64 `json:"score"`
	Tier          int     `json:"tier"`
	Value         float64 `json:"value"`
	AdjustedValue float64 `json:"adjusted_value"`
}

// ComparisonRow is one pool player's baseline and scenario placements and
// the adjustment behind any difference.
type ComparisonRow struct {
	PlayerKey  string                 `json:"player_key"`
	Name       string                 `json:"name"`
	Baseline   Placement              `json:"baseline"`
	Scenarios  map[Scenario]Placement `json:"scenarios"`
	Adjustment *PlayerAdjustment      `json:"adjustment,omitempty"`
}

// Comparison is one league's baseline ranking next to its ranking in each
// scenario. Every ranking is recomputed from its own projection snapshot
// under the same rules, pool and options, so a change in value comes only
// from the adjusted inputs. RulesHash names the stored league rule
// snapshot and AdjustmentID the stored run, so a comparison can be rebuilt
// from stored versions.
type Comparison struct {
	LeagueKey        string              `json:"league_key"`
	RulesHash        string              `json:"rules_hash"`
	AdjustmentID     string              `json:"adjustment_id"`
	BaselineVersion  string              `json:"baseline_version"`
	ScenarioVersions map[Scenario]string `json:"scenario_versions"`
	Assumptions      []string            `json:"assumptions"`
	Alerts           []string            `json:"alerts,omitempty"`
	Rows             []ComparisonRow     `json:"rows"`
}

// Compare ranks the baseline and every scenario of an adjustment for one
// league. Rows follow the baseline ranking order.
func Compare(rules draft.Snapshot, baseline projection.Snapshot, result Result, pool []draft.PoolPlayer, options draft.RankingOptions) (Comparison, error) {
	switch {
	case result.BaselineHash != baseline.SourceDataHash:
		return Comparison{}, fmt.Errorf("adjustment %s was built from another baseline", result.ID)
	case result.LeagueKey != "" && result.LeagueKey != rules.Rules.LeagueKey:
		return Comparison{}, fmt.Errorf("adjustment %s holds overrides for league %s, not %s", result.ID, result.LeagueKey, rules.Rules.LeagueKey)
	}
	base, err := draft.BuildRanking(rules, baseline, pool, options)
	if err != nil {
		return Comparison{}, fmt.Errorf("baseline ranking: %w", err)
	}
	comparison := Comparison{
		LeagueKey: rules.Rules.LeagueKey, RulesHash: base.RuleHash, AdjustmentID: result.ID, BaselineVersion: base.Version,
		ScenarioVersions: make(map[Scenario]string, len(Scenarios)),
		Assumptions:      append(slices.Clone(base.Assumptions), "news assumptions: "+result.Policy.Calibration),
		Alerts:           slices.Clone(result.Alerts),
	}
	placements := make(map[Scenario]map[string]Placement, len(Scenarios))
	for _, s := range Scenarios {
		snapshot, exists := result.Snapshots[s]
		if !exists {
			return Comparison{}, fmt.Errorf("adjustment %s has no %s snapshot", result.ID, s)
		}
		ranking, err := draft.BuildRanking(rules, snapshot, pool, options)
		if err != nil {
			return Comparison{}, fmt.Errorf("%s ranking: %w", s, err)
		}
		comparison.ScenarioVersions[s] = ranking.Version
		placements[s] = placementsByKey(ranking)
	}
	adjustments := make(map[string]*PlayerAdjustment, len(result.Players))
	for i := range result.Players {
		adjustments[result.Players[i].PlayerKey] = &result.Players[i]
	}
	for _, player := range base.Players {
		row := ComparisonRow{
			PlayerKey: player.PlayerKey, Name: player.Name, Baseline: placementOf(player),
			Scenarios: make(map[Scenario]Placement, len(Scenarios)), Adjustment: adjustments[player.PlayerKey],
		}
		for _, s := range Scenarios {
			row.Scenarios[s] = placements[s][player.PlayerKey]
		}
		comparison.Rows = append(comparison.Rows, row)
	}
	return comparison, nil
}

func placementsByKey(ranking draft.RankingSnapshot) map[string]Placement {
	out := make(map[string]Placement, len(ranking.Players))
	for _, player := range ranking.Players {
		out[player.PlayerKey] = placementOf(player)
	}
	return out
}

func placementOf(player draft.RankedPlayer) Placement {
	return Placement{Rank: player.OverallRank, Score: player.OfficialScore, Tier: player.Tier, Value: player.Value, AdjustedValue: player.AdjustedValue}
}

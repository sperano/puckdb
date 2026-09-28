package draft

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

const noOwner = -1

// weeklyGoalieMinimumSetting is Yahoo's head-to-head team minimum of goalie
// games per week; below it the team forfeits the goalie ratio categories.
const weeklyGoalieMinimumSetting = "min_games_played"

// typicalStarterWeeklyStarts is how many games a starting goalie plays in a
// fantasy week: an NHL team plays ~82 games over ~26 weeks (~3.15 a week)
// and a starter takes ~65% of them.
const typicalStarterWeeklyStarts = 2.0

var (
	workloadLimitTokens = map[string]bool{
		"cap": true, "limit": true, "max": true, "maximum": true,
		"min": true, "minimum": true,
	}
	workloadMeasureTokens = map[string]bool{
		"appearance": true, "appearances": true, "game": true, "games": true,
		"start": true, "starts": true,
	}
)

type rankingCaps struct {
	gameCap         float64
	goalieStartsCap float64
}

func (c rankingCaps) validatePolicy(policy WorkloadCapPolicy) error {
	if c.gameCap == 0 && c.goalieStartsCap == 0 {
		return nil
	}
	if policy != WorkloadCapsPerPlayer {
		return fmt.Errorf("workload caps require an explicit %q policy after verifying league semantics", WorkloadCapsPerPlayer)
	}
	return nil
}

type draftPlan struct {
	selected map[string]bool
	owner    []int
	units    []slotUnit
	adjusted bool
}

func capsFromRules(rules Rules) (rankingCaps, error) {
	var caps rankingCaps
	keys := make([]string, 0, len(rules.Settings))
	for key := range rules.Settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		raw := rules.Settings[key]
		if !isWorkloadLimit(key) {
			continue
		}
		if key == weeklyGoalieMinimumSetting && rules.ScoringType == scoringTypeHead {
			if err := checkWeeklyGoalieMinimum(rules, raw); err != nil {
				return caps, err
			}
			continue
		}
		if key != "max_games_played" && key != "max_goalie_starts" {
			return caps, fmt.Errorf("unmodeled workload limit %q requires an explicit ranking adapter", key)
		}
		cap, err := strconv.ParseFloat(raw, 64)
		if err != nil || !finiteRanking(cap) || cap <= 0 {
			return caps, fmt.Errorf("workload cap %q must be a positive number", key)
		}
		if key == "max_games_played" {
			caps.gameCap = cap
		} else {
			caps.goalieStartsCap = cap
		}
	}
	unmodeled := slices.Clone(rules.UnmodeledSettings)
	slices.Sort(unmodeled)
	for _, setting := range unmodeled {
		if isWorkloadLimit(setting) {
			return caps, fmt.Errorf("unmodeled workload limit %q requires an explicit ranking adapter", setting)
		}
	}
	return caps, nil
}

// checkWeeklyGoalieMinimum accepts a head-to-head weekly goalie minimum only
// when the starting goalie slots comfortably supply it, so it cannot change
// player values; a binding minimum is not modeled and fails closed.
func checkWeeklyGoalieMinimum(rules Rules, raw string) error {
	minimum, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finiteRanking(minimum) || minimum <= 0 {
		return fmt.Errorf("workload limit %q must be a positive number", weeklyGoalieMinimumSetting)
	}
	var slots int
	for _, slot := range rules.RosterSlots {
		if slot.Position == PositionGoalie && slot.Kind() == SlotKindStarting && slot.Starting {
			slots += slot.Count
		}
	}
	supply := float64(slots) * typicalStarterWeeklyStarts
	if supply < minimum {
		return fmt.Errorf("weekly goalie minimum of %g games is binding (%d starting goalie slots supply about %g) and is not modeled",
			minimum, slots, supply)
	}
	return nil
}

func isWorkloadLimit(setting string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(setting), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var hasLimit, hasMeasure bool
	for _, token := range tokens {
		hasLimit = hasLimit || workloadLimitTokens[token]
		hasMeasure = hasMeasure || workloadMeasureTokens[token]
	}
	return hasLimit && hasMeasure
}

func demandUnits(rules Rules, options RankingOptions) ([]slotUnit, error) {
	if rules.NumTeams <= 0 {
		return nil, fmt.Errorf("league team count must be positive")
	}
	var units []slotUnit
	for _, slot := range rules.RosterSlots {
		if slot.Count < 0 {
			return nil, fmt.Errorf("roster slot %q has negative count", slot.Position)
		}
		kind := slot.Kind()
		if kind == SlotKindUnsupported {
			return nil, fmt.Errorf("roster slot %q is not modeled for rankings", slot.Position)
		}
		if kind == SlotKindReserve || kind == SlotKindBench && options.BenchPolicy == BenchExcluded {
			continue
		}
		if kind == SlotKindStarting && !slot.Starting {
			return nil, fmt.Errorf("ordinary roster slot %q is not marked starting", slot.Position)
		}
		for range rules.NumTeams * slot.Count {
			units = append(units, slotUnit{slot: slot.Position, index: len(units)})
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("league has no active draft-demand slots")
	}
	slices.SortStableFunc(units, func(a, b slotUnit) int {
		if a.slot == SlotBench && b.slot != SlotBench {
			return 1
		}
		if b.slot == SlotBench && a.slot != SlotBench {
			return -1
		}
		return 0
	})
	return units, nil
}

// draftedCandidates uses the weighted transversal-matroid greedy rule:
// consider higher scoring players first, retaining a player iff the retained
// set can still be assigned to distinct roster units. A flexible player can
// occupy one unit only; the augmenting path moves earlier picks when needed.
func draftedCandidates(candidates []rankingCandidate, units []slotUnit, adjusted bool) draftPlan {
	indices := orderedCandidateIndices(candidates, adjusted)
	owner := make([]int, len(units))
	for i := range owner {
		owner[i] = noOwner
	}
	selected := make(map[string]bool)
	for _, player := range indices {
		seen := make([]bool, len(units))
		if assignRankingUnit(player, candidates, units, owner, seen) {
			selected[candidates[player].pool.PlayerKey] = true
		}
	}
	return draftPlan{selected: selected, owner: owner, units: units, adjusted: adjusted}
}

func orderedCandidateIndices(candidates []rankingCandidate, adjusted bool) []int {
	indices := make([]int, len(candidates))
	for i := range indices {
		indices[i] = i
	}
	slices.SortFunc(indices, func(i, j int) int {
		first := candidateScore(candidates[i], adjusted)
		second := candidateScore(candidates[j], adjusted)
		if first != second {
			if first > second {
				return -1
			}
			return 1
		}
		return strings.Compare(candidates[i].pool.PlayerKey, candidates[j].pool.PlayerKey)
	})
	return indices
}

func assignRankingUnit(player int, candidates []rankingCandidate, units []slotUnit, owner []int, seen []bool) bool {
	for unit, slot := range units {
		if seen[unit] || !SlotAccepts(slot.slot, candidates[player].positions) {
			continue
		}
		seen[unit] = true
		if owner[unit] == noOwner || assignRankingUnit(owner[unit], candidates, units, owner, seen) {
			owner[unit] = player
			return true
		}
	}
	return false
}

func replacementValue(candidateIndex int, candidates []rankingCandidate, plan draftPlan) float64 {
	candidate := candidates[candidateIndex]
	if plan.selected[candidate.pool.PlayerKey] {
		return legalReplacementValue(candidateIndex, candidates, plan)
	}
	return undraftedFrontier(candidate, candidates, plan)
}

func legalReplacementValue(candidateIndex int, candidates []rankingCandidate, plan draftPlan) float64 {
	owner := slices.Clone(plan.owner)
	removed := false
	for unit, player := range owner {
		if player == candidateIndex {
			owner[unit] = noOwner
			removed = true
			break
		}
	}
	if !removed {
		return 0
	}
	for _, replacement := range orderedCandidateIndices(candidates, plan.adjusted) {
		key := candidates[replacement].pool.PlayerKey
		if replacement == candidateIndex || plan.selected[key] {
			continue
		}
		trial := slices.Clone(owner)
		seen := make([]bool, len(plan.units))
		if assignRankingUnit(replacement, candidates, plan.units, trial, seen) {
			return candidateScore(candidates[replacement], plan.adjusted)
		}
	}
	return 0
}

func undraftedFrontier(candidate rankingCandidate, candidates []rankingCandidate, plan draftPlan) float64 {
	best := 0.0
	first := true
	for _, position := range candidate.positions {
		positionBest := 0.0
		found := false
		for _, other := range candidates {
			if plan.selected[other.pool.PlayerKey] || !slices.Contains(other.positions, position) {
				continue
			}
			score := candidateScore(other, plan.adjusted)
			if !found || score > positionBest {
				positionBest = score
				found = true
			}
		}
		if first || positionBest < best {
			best = positionBest
			first = false
		}
	}
	return best
}

func candidateScore(candidate rankingCandidate, adjusted bool) float64 {
	if adjusted {
		return candidate.adjusted
	}
	return candidate.official
}

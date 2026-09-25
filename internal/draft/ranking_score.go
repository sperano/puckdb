package draft

import (
	"fmt"
	"math"
	"slices"

	"github.com/sperano/puckdb/internal/projection"
)

const (
	minimumScale           = 1e-9
	minimumOpportunity     = 1e-9
	minimumRatioMultiplier = 0.0
	maximumRatioMultiplier = 1.0
)

type rankingCandidate struct {
	pool       PoolPlayer
	projection projection.PlayerProjection
	positions  []string
	stats      map[int]statValue
	official   float64
	adjusted   float64
	parts      []Contribution
}

type statValue struct {
	mean        float64
	opportunity float64
}

type categoryScale struct {
	center    float64
	spread    float64
	reference float64
}

func prepareCandidates(scoring Scoring, pool []PoolPlayer, projected projection.Snapshot, gameCap, goalieStartsCap float64) ([]rankingCandidate, error) {
	byKey := make(map[string]projection.PlayerProjection, len(projected.Players))
	for _, player := range projected.Players {
		if _, exists := byKey[player.PlayerKey]; exists {
			return nil, fmt.Errorf("duplicate projection %q", player.PlayerKey)
		}
		byKey[player.PlayerKey] = player
	}
	seen := make(map[string]bool, len(pool))
	candidates := make([]rankingCandidate, 0, len(pool))
	for _, player := range pool {
		if player.PlayerKey == "" || seen[player.PlayerKey] {
			return nil, fmt.Errorf("empty or duplicate pool key %q", player.PlayerKey)
		}
		seen[player.PlayerKey] = true
		value, exists := byKey[player.PlayerKey]
		if !exists {
			return nil, fmt.Errorf("pool player %q has no projection", player.PlayerKey)
		}
		candidate, err := prepareCandidate(scoring, player, value, gameCap, goalieStartsCap)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	slices.SortFunc(candidates, func(a, b rankingCandidate) int {
		if a.pool.PlayerKey < b.pool.PlayerKey {
			return -1
		}
		if a.pool.PlayerKey > b.pool.PlayerKey {
			return 1
		}
		return 0
	})
	return candidates, nil
}

func prepareCandidate(
	scoring Scoring,
	player PoolPlayer,
	projected projection.PlayerProjection,
	gameCap, goalieStartsCap float64,
) (rankingCandidate, error) {
	positions := eligibleBasePositions(player.EligiblePositions)
	if len(positions) == 0 {
		return rankingCandidate{}, fmt.Errorf("pool player %q has no base position eligibility", player.PlayerKey)
	}
	if (projected.Kind == projection.PlayerKindGoalie) != slices.Contains(positions, PositionGoalie) {
		return rankingCandidate{}, fmt.Errorf("pool player %q kind conflicts with Yahoo eligibility", player.PlayerKey)
	}
	candidate := rankingCandidate{
		pool: player, projection: projected, positions: positions,
		stats: make(map[int]statValue),
	}
	for _, stat := range scoring.Stats {
		kind, projectedStat, supported := projection.YahooCategoryStat(stat.StatID)
		if !supported {
			return rankingCandidate{}, fmt.Errorf("yahoo stat %d has no projection mapping", stat.StatID)
		}
		if !positionTypesMatch(stat.PositionTypes, kind) {
			return rankingCandidate{}, fmt.Errorf("yahoo stat %d position type conflicts with projection mapping", stat.StatID)
		}
		if kind != projected.Kind {
			continue
		}
		value, err := scoredValue(projected, projectedStat, gameCap, goalieStartsCap)
		if err != nil {
			return rankingCandidate{}, fmt.Errorf("player %q stat %d: %w", player.PlayerKey, stat.StatID, err)
		}
		candidate.stats[stat.StatID] = value
	}
	return candidate, nil
}

func positionTypesMatch(positionTypes []string, kind projection.PlayerKind) bool {
	if len(positionTypes) == 0 {
		return true
	}
	positionType := "P"
	if kind == projection.PlayerKindGoalie {
		positionType = "G"
	}
	return slices.Contains(positionTypes, positionType)
}

func eligibleBasePositions(eligible []string) []string {
	positions := make([]string, 0, len(basePositions))
	for _, position := range basePositions {
		if slices.Contains(eligible, position) {
			positions = append(positions, position)
		}
	}
	return positions
}

func scoredValue(player projection.PlayerProjection, stat projection.Stat, gameCap, goalieStartsCap float64) (statValue, error) {
	estimate, exists := player.Values[stat]
	if !exists || !finiteRanking(estimate.Mean) || !finiteRanking(estimate.Low) || !finiteRanking(estimate.High) {
		return statValue{}, fmt.Errorf("missing or nonfinite %s projection", stat)
	}
	if !finiteRanking(player.Uncertainty) || player.Uncertainty < 0 {
		return statValue{}, fmt.Errorf("invalid projection uncertainty")
	}
	value := statValue{mean: estimate.Mean}
	if stat == projection.StatSavePercentage || stat == projection.StatGoalsAgainstAvg {
		denominator := projection.StatShotsAgainst
		if stat == projection.StatGoalsAgainstAvg {
			denominator = projection.StatTOISeconds
		}
		exposure, exists := player.Values[denominator]
		if !exists || !finiteRanking(exposure.Mean) || !finiteRanking(exposure.Low) || !finiteRanking(exposure.High) || exposure.Mean <= minimumOpportunity {
			return statValue{}, fmt.Errorf("%s ratio requires positive %s", stat, denominator)
		}
		value.opportunity = exposure.Mean
	}
	if gameCap > 0 || player.Kind == projection.PlayerKindGoalie && goalieStartsCap > 0 {
		factor, err := workloadFactor(player, gameCap, goalieStartsCap)
		if err != nil {
			return statValue{}, err
		}
		if value.opportunity > 0 {
			value.opportunity *= factor
		} else {
			value.mean *= factor
		}
	}
	return value, nil
}

func workloadFactor(player projection.PlayerProjection, gameCap, goalieStartsCap float64) (float64, error) {
	games, exists := player.Values[projection.StatGamesPlayed]
	if !exists || !finiteRanking(games.Mean) || games.Mean <= 0 {
		return 0, fmt.Errorf("game cap requires positive games played projection")
	}
	factor := 1.0
	if gameCap > 0 {
		factor = math.Min(factor, gameCap/games.Mean)
	}
	if player.Kind == projection.PlayerKindGoalie && goalieStartsCap > 0 {
		starts, exists := player.Values[projection.StatGamesStarted]
		if !exists || !finiteRanking(starts.Mean) || starts.Mean <= 0 {
			return 0, fmt.Errorf("goalie start cap requires positive starts projection")
		}
		factor = math.Min(factor, goalieStartsCap/starts.Mean)
	}
	return factor, nil
}

func categoryScales(scoring Scoring, candidates []rankingCandidate) map[int]categoryScale {
	scales := make(map[int]categoryScale, len(scoring.Stats))
	for _, stat := range scoring.Stats {
		var sum, sumSquares, weights, opportunities float64
		for _, candidate := range candidates {
			value, exists := candidate.stats[stat.StatID]
			if !exists {
				continue
			}
			weight := 1.0
			if value.opportunity > 0 {
				weight = value.opportunity
				opportunities += value.opportunity
			}
			sum += value.mean * weight
			sumSquares += value.mean * value.mean * weight
			weights += weight
		}
		if weights == 0 {
			continue
		}
		center := sum / weights
		spread := math.Sqrt(math.Max(sumSquares/weights-center*center, 0))
		if spread < minimumScale {
			spread = math.Max(math.Abs(center), 1)
		}
		reference := 1.0
		if opportunities > 0 {
			reference = opportunities / float64(statCandidateCount(stat.StatID, candidates))
		}
		scales[stat.StatID] = categoryScale{center: center, spread: spread, reference: reference}
	}
	return scales
}

func statCandidateCount(id int, candidates []rankingCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if _, exists := candidate.stats[id]; exists {
			count++
		}
	}
	return count
}

func scoreCandidates(scoring Scoring, options RankingOptions, candidates []rankingCandidate) {
	scales := categoryScales(scoring, candidates)
	for i := range candidates {
		candidate := &candidates[i]
		categoryCount := len(candidate.stats)
		for _, stat := range scoring.Stats {
			value, relevant := candidate.stats[stat.StatID]
			if !relevant {
				continue
			}
			weight := defaultCategoryWeight
			if preferred, exists := options.CategoryWeights[stat.StatID]; exists {
				weight = preferred
			}
			official := officialContribution(scoring, stat, value, scales[stat.StatID], candidate.projection.Uncertainty, categoryCount)
			adjusted := official * weight
			candidate.official += official
			candidate.adjusted += adjusted
			_, projectedStat, _ := projection.YahooCategoryStat(stat.StatID)
			candidate.parts = append(candidate.parts, Contribution{
				StatID: stat.StatID, Stat: projectedStat, Projected: value.mean,
				Official: official, Adjusted: adjusted, Weight: weight,
				Direction: stat.Direction, Opportunity: value.opportunity,
				Explanation: contributionExplanation(scoring, stat, value),
			})
		}
	}
}

func officialContribution(scoring Scoring, stat ScoringStat, value statValue, scale categoryScale, uncertainty float64, count int) float64 {
	if scoring.Format == FormatPoints {
		return value.mean * stat.Weight
	}
	standardized := (value.mean - scale.center) / scale.spread
	if stat.Direction == LowerIsBetter {
		standardized = -standardized
	}
	if value.opportunity > 0 {
		multiplier := math.Sqrt(value.opportunity / scale.reference)
		standardized *= math.Max(minimumRatioMultiplier, math.Min(multiplier, maximumRatioMultiplier))
	}
	if scoring.Objective == ObjectiveHeadToHead {
		reliabilityPenalty := uncertainty / (1 + uncertainty)
		standardized -= reliabilityPenalty * math.Abs(standardized)
	}
	return standardized / math.Sqrt(float64(count))
}

func contributionExplanation(scoring Scoring, stat ScoringStat, value statValue) string {
	if scoring.Format == FormatPoints {
		return fmt.Sprintf("Yahoo weight %.4g multiplied by projected %s", stat.Weight, stat.Abbr)
	}
	objective := "season-long category standing"
	if scoring.Objective == ObjectiveHeadToHead {
		objective = "weekly category reliability adjusted for projection uncertainty"
	}
	if value.opportunity > 0 {
		return fmt.Sprintf("standardized %s for %s, ratio influence weighted by %.1f opportunities", stat.Abbr, objective, value.opportunity)
	}
	return fmt.Sprintf("standardized %s for %s", stat.Abbr, objective)
}

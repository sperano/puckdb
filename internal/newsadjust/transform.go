package newsadjust

import (
	"math"

	"github.com/sperano/puckdb/internal/projection"
)

// minimumWorkload keeps the scenario spread finite when the base scenario
// removes the whole season.
const minimumWorkload = 1e-9

// skaterIceTimeStats scale with games and ice time per game: the baseline
// projects them as per-TOI rates.
var skaterIceTimeStats = []projection.Stat{
	projection.StatTOISeconds, projection.StatPlusMinus, projection.StatPenaltyMinutes,
	projection.StatShotsOnGoal, projection.StatHits, projection.StatBlockedShots,
}

// goalieWorkloadStats scale with starts. Save percentage and GAA are ratios
// of these components, so scaling leaves the per-start rates unchanged.
var goalieWorkloadStats = []projection.Stat{
	projection.StatGamesPlayed, projection.StatGamesStarted, projection.StatTOISeconds,
	projection.StatShotsAgainst, projection.StatSaves, projection.StatGoalsAgainst,
	projection.StatWins, projection.StatShutouts,
}

// adjustedValues applies one scenario's effect to a player's baseline.
func adjustedValues(player projection.PlayerProjection, effect Effect, seasonGames float64) map[projection.Stat]projection.Estimate {
	values := make(map[projection.Stat]projection.Estimate, len(player.Values))
	for stat, estimate := range player.Values {
		values[stat] = estimate
	}
	if player.Kind == projection.PlayerKindGoalie {
		adjustGoalie(values, effect.GamesFactor*effect.GoalieStarts, seasonGames)
		return values
	}
	adjustSkater(values, effect)
	return values
}

func adjustGoalie(values map[projection.Stat]projection.Estimate, workload, seasonGames float64) {
	for _, stat := range goalieWorkloadStats {
		scaleStat(values, stat, workload)
	}
	capStat(values, projection.StatGamesPlayed, seasonGames)
	if games, exists := values[projection.StatGamesPlayed]; exists {
		capStat(values, projection.StatGamesStarted, games.High)
	}
}

// adjustSkater scales even-strength production with ice time and
// power-play production with the power-play factor. Goals and assists
// follow the new point total so the components still add up.
func adjustSkater(values map[projection.Stat]projection.Estimate, effect Effect) {
	games, toi, pp := effect.GamesFactor, effect.IceTime, effect.PowerPlay
	points := values[projection.StatPoints].Mean
	powerPlay := math.Min(values[projection.StatPowerPlayPoints].Mean, points)
	pointsFactor := games * toi
	if points > 0 {
		pointsFactor = games * ((points-powerPlay)*toi + powerPlay*pp) / points
	}
	scaleStat(values, projection.StatGamesPlayed, games)
	for _, stat := range skaterIceTimeStats {
		scaleStat(values, stat, games*toi)
	}
	scaleStat(values, projection.StatPowerPlayPoints, games*pp)
	for _, stat := range []projection.Stat{projection.StatPoints, projection.StatGoals, projection.StatAssists} {
		scaleStat(values, stat, pointsFactor)
	}
}

func scaleStat(values map[projection.Stat]projection.Estimate, stat projection.Stat, factor float64) {
	estimate, exists := values[stat]
	if !exists {
		return
	}
	values[stat] = ordered(projection.Estimate{
		Mean: estimate.Mean * factor, Low: estimate.Low * factor, High: estimate.High * factor,
	})
}

func capStat(values map[projection.Stat]projection.Estimate, stat projection.Stat, maximum float64) {
	estimate, exists := values[stat]
	if !exists {
		return
	}
	values[stat] = ordered(projection.Estimate{
		Mean: math.Min(estimate.Mean, maximum), Low: math.Min(estimate.Low, maximum), High: math.Min(estimate.High, maximum),
	})
}

func ordered(e projection.Estimate) projection.Estimate {
	low, high := math.Min(e.Low, e.High), math.Max(e.Low, e.High)
	return projection.Estimate{Mean: math.Min(math.Max(e.Mean, low), high), Low: low, High: high}
}

// envelope keeps a scenario's mean and widens its interval to cover every
// scenario, so uncertainty about the news shows in each snapshot.
func envelope(byScenario map[Scenario]map[projection.Stat]projection.Estimate, s Scenario) map[projection.Stat]projection.Estimate {
	out := make(map[projection.Stat]projection.Estimate, len(byScenario[s]))
	for stat, estimate := range byScenario[s] {
		for _, other := range Scenarios {
			bound := byScenario[other][stat]
			estimate.Low = math.Min(estimate.Low, bound.Low)
			estimate.High = math.Max(estimate.High, bound.High)
		}
		out[stat] = estimate
	}
	return out
}

// workload summarizes an effect as one factor: games times ice time for a
// skater, starts for a goalie.
func workload(kind projection.PlayerKind, effect Effect) float64 {
	if kind == projection.PlayerKindGoalie {
		return effect.GamesFactor * effect.GoalieStarts
	}
	return effect.GamesFactor * effect.IceTime
}

// widenedUncertainty adds the scenario spread (half the conservative to
// optimistic range, relative to base) to the baseline uncertainty in
// quadrature, capped at the projection's maximum.
func widenedUncertainty(baseline, maximum float64, kind projection.PlayerKind, effects map[Scenario]Effect) float64 {
	low := workload(kind, effects[ScenarioConservative])
	high := workload(kind, effects[ScenarioOptimistic])
	if high == low {
		return baseline
	}
	spread := math.Abs(high-low) / (2 * math.Max(workload(kind, effects[ScenarioBase]), minimumWorkload))
	return math.Min(maximum, math.Max(baseline, math.Hypot(baseline, spread)))
}

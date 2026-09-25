package draft

import (
	"fmt"
)

const tiedPredictionCredit = 0.5

// OrderingBacktest compares a frozen ranking with basic point totals against
// later realized values. It does not produce those outcomes or retrain scores.
type OrderingBacktest struct {
	Players            int
	Pairs              int
	RankingConcordance float64
	PointConcordance   float64
	RankingImprovement float64
}

// EvaluateOrdering measures pairwise ordering accuracy on supplied held-out
// outcomes. Tied predictions get half credit; tied outcomes do not count.
// All players need both a realized outcome and a basic point-total baseline.
func EvaluateOrdering(ranking RankingSnapshot, realized, basicPoints map[string]float64) (OrderingBacktest, error) {
	if len(ranking.Players) < 2 {
		return OrderingBacktest{}, fmt.Errorf("backtest needs at least two ranked players")
	}
	for _, player := range ranking.Players {
		outcome, hasOutcome := realized[player.PlayerKey]
		points, hasPoints := basicPoints[player.PlayerKey]
		if !hasOutcome || !hasPoints || !finiteRanking(outcome) || !finiteRanking(points) {
			return OrderingBacktest{}, fmt.Errorf("backtest player %q needs finite outcome and point total", player.PlayerKey)
		}
	}
	result := OrderingBacktest{Players: len(ranking.Players)}
	var rankingCorrect, pointsCorrect float64
	for i, first := range ranking.Players {
		for _, second := range ranking.Players[i+1:] {
			actual := compareValues(realized[first.PlayerKey], realized[second.PlayerKey])
			if actual == 0 {
				continue
			}
			result.Pairs++
			rankingCorrect += orderingCredit(compareValues(first.AdjustedValue, second.AdjustedValue), actual)
			pointsCorrect += orderingCredit(compareValues(basicPoints[first.PlayerKey], basicPoints[second.PlayerKey]), actual)
		}
	}
	if result.Pairs == 0 {
		return OrderingBacktest{}, fmt.Errorf("backtest outcomes contain no ordered player pairs")
	}
	result.RankingConcordance = rankingCorrect / float64(result.Pairs)
	result.PointConcordance = pointsCorrect / float64(result.Pairs)
	result.RankingImprovement = result.RankingConcordance - result.PointConcordance
	return result, nil
}

func compareValues(first, second float64) int {
	if first > second {
		return 1
	}
	if first < second {
		return -1
	}
	return 0
}

func orderingCredit(predicted, actual int) float64 {
	if predicted == 0 {
		return tiedPredictionCredit
	}
	if predicted == actual {
		return 1
	}
	return 0
}

package draftrecommend

import (
	"errors"
	"math"
	"sort"
)

const (
	p50Quantile = 0.50
	p95Quantile = 0.95
)

// EvaluateReplay measures whether each historical selection matched either
// persisted recommendation and summarizes the recorded evaluation latency.
func EvaluateReplay(cases []ReplayCase) (ReplayMetrics, error) {
	if len(cases) == 0 {
		return ReplayMetrics{}, errors.New("draft recommendation replay requires at least one case")
	}
	metrics := ReplayMetrics{Cases: len(cases)}
	latencies := make([]int64, 0, len(cases))
	for _, replay := range cases {
		if replay.Result.LatencyMillis < 0 || replay.SelectedPlayerKey == "" {
			return ReplayMetrics{}, errors.New("draft recommendation replay contains invalid selection or latency")
		}
		latencies = append(latencies, replay.Result.LatencyMillis)
		if matchesRecommendation(replay.Result, replay.SelectedPlayerKey) {
			metrics.Correct++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	metrics.Correctness = float64(metrics.Correct) / float64(metrics.Cases)
	metrics.P50LatencyMS = quantile(latencies, p50Quantile)
	metrics.P95LatencyMS = quantile(latencies, p95Quantile)
	return metrics, nil
}

func matchesRecommendation(result Result, selected string) bool {
	return result.BestValue != nil && result.BestValue.PlayerKey == selected ||
		result.BestRosterFit != nil && result.BestRosterFit.PlayerKey == selected
}

func quantile(values []int64, probability float64) int64 {
	index := int(math.Ceil(probability*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}

package draft

import "testing"

func TestEvaluateOrdering_ComparisonToBasicPoints(t *testing.T) {
	ranking := RankingSnapshot{Players: []RankedPlayer{
		{PlayerKey: "wing", AdjustedValue: 8},
		{PlayerKey: "center", AdjustedValue: 2},
		{PlayerKey: "depth", AdjustedValue: 0},
	}}
	realized := map[string]float64{"wing": 9, "center": 7, "depth": 1}
	points := map[string]float64{"wing": 80, "center": 100, "depth": 50}
	result, err := EvaluateOrdering(ranking, realized, points)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pairs != 3 || result.RankingConcordance != 1 || result.PointConcordance != 2.0/3.0 || result.RankingImprovement <= 0 {
		t.Fatalf("unexpected ordering result: %+v", result)
	}
	points["wing"] = 110
	result, err = EvaluateOrdering(ranking, realized, points)
	if err != nil || result.RankingImprovement != 0 {
		t.Fatalf("equal predictive order should show no improvement: %+v, %v", result, err)
	}
	delete(realized, "wing")
	if _, err := EvaluateOrdering(ranking, realized, points); err == nil {
		t.Fatal("missing held-out outcome must fail")
	}
}

package draftrecommend

import "testing"

func TestEvaluateReplay_ComputesCorrectnessAndLatencyQuantiles(t *testing.T) {
	first := Result{LatencyMillis: 10, BestValue: &Candidate{PlayerKey: "picked-a"}}
	second := Result{LatencyMillis: 20, BestRosterFit: &Candidate{PlayerKey: "picked-b"}}
	third := Result{LatencyMillis: 50, BestValue: &Candidate{PlayerKey: "other"}, BestRosterFit: &Candidate{PlayerKey: "picked-c"}}
	metrics, err := EvaluateReplay([]ReplayCase{
		{Result: first, SelectedPlayerKey: "picked-a"},
		{Result: second, SelectedPlayerKey: "picked-b", ShownRecommendationKey: "picked-b"},
		{Result: third, SelectedPlayerKey: "picked-c", ShownRecommendationKey: "other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Cases != 3 || metrics.Correct != 2 || metrics.Correctness != 2.0/3 {
		t.Fatalf("replay correctness = %+v", metrics)
	}
	if metrics.P50LatencyMS != 20 || metrics.P95LatencyMS != 50 {
		t.Fatalf("replay latency = %+v", metrics)
	}
}

func TestEvaluateReplay_RejectsUnrecordedShownRecommendation(t *testing.T) {
	_, err := EvaluateReplay([]ReplayCase{{
		Result:            Result{BestValue: &Candidate{PlayerKey: "value"}},
		SelectedPlayerKey: "value", ShownRecommendationKey: "not-recorded",
	}})
	if err == nil {
		t.Fatal("expected invalid shown recommendation error")
	}
}

func TestEvaluateReplay_RejectsEmptyCases(t *testing.T) {
	if _, err := EvaluateReplay(nil); err == nil {
		t.Fatal("expected empty replay error")
	}
}

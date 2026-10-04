package draftrecommend

import "testing"

func TestEvaluateReplay_ComputesCorrectnessAndLatencyQuantiles(t *testing.T) {
	first := Result{LatencyMillis: 10, BestValue: &Candidate{PlayerKey: "picked-a"}}
	second := Result{LatencyMillis: 20, BestRosterFit: &Candidate{PlayerKey: "picked-b"}}
	third := Result{LatencyMillis: 50, BestValue: &Candidate{PlayerKey: "other"}}
	metrics, err := EvaluateReplay([]ReplayCase{
		{Result: first, SelectedPlayerKey: "picked-a"},
		{Result: second, SelectedPlayerKey: "picked-b"},
		{Result: third, SelectedPlayerKey: "picked-c"},
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

func TestEvaluateReplay_RejectsEmptyCases(t *testing.T) {
	if _, err := EvaluateReplay(nil); err == nil {
		t.Fatal("expected empty replay error")
	}
}

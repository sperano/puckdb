package draftrecommend

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draftrank"
)

func TestEvaluate_PreservesRankingIssueCodesAndCodesItsOwn(t *testing.T) {
	in := recommendationInput(t)
	stalePool := draftrank.Issue{Code: draftrank.IssueStalePool, Message: "old pool"}
	newsDown := draftrank.Issue{Code: draftrank.IssueNewsAdjustmentsDown, Message: "news service unavailable"}
	in.RankingIssues = []draftrank.Issue{stalePool}
	in.Ranking.Meta.Unavailable = []draftrank.Issue{newsDown}
	in.SessionStale = true

	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []draftrank.Issue{stalePool, newsDown, issueBoardStale, issueBoardIncomplete} {
		if !slices.Contains(result.Issues, want) {
			t.Fatalf("issue %+v missing from %+v", want, result.Issues)
		}
	}
	if result.BoardStatus != BoardStatusStale {
		t.Fatalf("board status = %q, want %q", result.BoardStatus, BoardStatusStale)
	}
}

func TestEvaluate_ScenarioFallbackUsesRankingIssueCode(t *testing.T) {
	in := recommendationInput(t)
	in.Scenario = draftrank.ScenarioOptimistic
	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.Issues, issueScenarioFallback) || result.Issues[0].Code != draftrank.IssueScenarioFallback {
		t.Fatalf("scenario fallback issue = %+v", result.Issues)
	}
}

// Stored runs are the service boundary: the persisted JSON must decode back
// to the same codes and messages, and the run service must not reshape them.
func TestRecommendPersistsCodedIssuesThroughStoredRun(t *testing.T) {
	in := recommendationInput(t)
	in.RankingIssues = []draftrank.Issue{{Code: draftrank.IssueNewsSourceStale, Message: "feed is old"}}
	in.Order = nil
	store := &recordingRunStore{id: uuid.New()}
	run, err := NewService(store).Recommend(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(run.Result.Issues, store.result.Issues) {
		t.Fatalf("returned issues %+v differ from stored %+v", run.Result.Issues, store.result.Issues)
	}
	inputRaw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	resultRaw, err := json.Marshal(store.result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStoredRun(run.ID, inputRaw, resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded.Result.Issues, run.Result.Issues) {
		t.Fatalf("decoded issues %+v, want %+v", decoded.Result.Issues, run.Result.Issues)
	}
	for _, want := range []draftrank.IssueCode{draftrank.IssueNewsSourceStale, IssuePickOrderMissing, IssueTurnEstimateOmitted} {
		if !slices.ContainsFunc(decoded.Result.Issues, func(issue draftrank.Issue) bool { return issue.Code == want }) {
			t.Fatalf("code %s missing from %+v", want, decoded.Result.Issues)
		}
	}
}

func TestIssuesDecodeLegacyStrings(t *testing.T) {
	raw := []byte(`{"issues":[
		"draft board is stale",
		"NEWS_SOURCE_STALE: feed is old",
		"ranking player 500.p.7 lacks requested scenario placement",
		"requested news scenario unavailable; baseline scenario used",
		"something no release ever wrote"]}`)
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	want := Issues{
		issueBoardStale,
		{Code: draftrank.IssueNewsSourceStale, Message: "feed is old"},
		missingPlacementIssue("500.p.7"),
		issueScenarioFallback,
		{Code: IssueUnclassified, Message: "something no release ever wrote"},
	}
	if !slices.Equal(result.Issues, want) {
		t.Fatalf("legacy issues = %+v, want %+v", result.Issues, want)
	}
}

func TestIssuesDecodeRejectsOtherShapes(t *testing.T) {
	var result Result
	if err := json.Unmarshal([]byte(`{"issues":[1,2]}`), &result); err == nil {
		t.Fatal("expected an error for numeric issues")
	}
}

func TestEvaluate_ReportsRankingIssueOnceWhenPassedTwice(t *testing.T) {
	in := recommendationInput(t)
	newsDown := draftrank.Issue{Code: draftrank.IssueNewsAdjustmentsDown, Message: "news service unavailable"}
	in.Ranking.Meta.Unavailable = []draftrank.Issue{newsDown}
	in.RankingIssues = []draftrank.Issue{newsDown}
	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, issue := range result.Issues {
		if issue == newsDown {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("issue %+v reported %d times in %+v", newsDown, count, result.Issues)
	}
}

package draftrecommend

import (
	"slices"
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
)

func TestEvaluate_OutdatedRankingKeepsLabeledAdvice(t *testing.T) {
	codes := []draftrank.IssueCode{
		draftrank.IssueStaleSnapshot, draftrank.IssueStalePool, draftrank.IssueOverridesChanged,
		draftrank.IssueRulesChanged, draftrank.IssueNewerSnapshot, draftrank.IssueRefreshFailed,
		draftrank.IssueRefreshCanceled, draftrank.IssueRefreshInterrupted,
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			in := recommendationInput(t)
			issue := draftrank.Issue{Code: code, Message: "ranking condition"}
			in.RankingIssues = []draftrank.Issue{issue}

			result, err := Evaluate(in)

			if err != nil {
				t.Fatal(err)
			}
			if result.BoardStatus != BoardStatusStale || len(result.Candidates) == 0 {
				t.Fatalf("status = %q with %d candidates, want stale advice", result.BoardStatus, len(result.Candidates))
			}
			if !slices.Contains(result.Issues, issue) {
				t.Fatalf("issues %+v lack %+v", result.Issues, issue)
			}
		})
	}
}

func TestEvaluate_WithheldRankingGivesNoAdviceBeforeCheckingObsoleteSlots(t *testing.T) {
	for _, code := range []draftrank.IssueCode{draftrank.IssueRosterSlotsChanged, draftrank.IssueScoringChanged} {
		t.Run(string(code), func(t *testing.T) {
			in := recommendationInput(t)
			issue := draftrank.Issue{Code: code, Message: "ranked under other rules"}
			in.RankingIssues = []draftrank.Issue{{Code: draftrank.IssueStalePool, Message: "old pool"}, issue}
			// Three centers fit the league's current slots but not the
			// snapshot's one center and one bench slot.
			in.Roster = append(in.Roster,
				draft.RosterPlayer{YahooPlayerID: 201, Name: "second", EligiblePositions: []string{draft.PositionCenter}},
				draft.RosterPlayer{YahooPlayerID: 202, Name: "third", EligiblePositions: []string{draft.PositionCenter}})

			result, err := Evaluate(in)

			if err != nil {
				t.Fatalf("a withheld ranking must be labeled, not checked against its obsolete slots: %v", err)
			}
			if result.BoardStatus != BoardStatusWithheld || len(result.Candidates) != 0 ||
				result.BestValue != nil || result.BestRosterFit != nil {
				t.Fatalf("withheld result = %+v", result)
			}
			if !slices.Contains(result.Issues, issue) {
				t.Fatalf("issues %+v lack %+v", result.Issues, issue)
			}
		})
	}
}

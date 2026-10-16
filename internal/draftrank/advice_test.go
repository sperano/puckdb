package draftrank_test

import (
	"testing"

	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/stretchr/testify/assert"
)

func TestAdviceOf_IsTheStrongestIssueEffect(t *testing.T) {
	tests := []struct {
		name   string
		codes  []draftrank.IssueCode
		advice draftrank.Advice
	}{
		{"no issues", nil, draftrank.AdviceUnaffected},
		{"snapshot-time news coverage", []draftrank.IssueCode{draftrank.IssueNewsSourceStale, draftrank.IssueNewsAdjustmentsDown}, draftrank.AdviceUnaffected},
		{"unknown code", []draftrank.IssueCode{"BOARD_STALE"}, draftrank.AdviceUnaffected},
		{"stale pool", []draftrank.IssueCode{draftrank.IssueStalePool}, draftrank.AdviceOutdated},
		{"interrupted refresh", []draftrank.IssueCode{draftrank.IssueRefreshInterrupted}, draftrank.AdviceOutdated},
		{"withheld wins", []draftrank.IssueCode{draftrank.IssueStaleSnapshot, draftrank.IssueScoringChanged, draftrank.IssueOverridesChanged}, draftrank.AdviceWithheld},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := make([]draftrank.Issue, len(test.codes))
			for i, code := range test.codes {
				issues[i] = draftrank.Issue{Code: code}
			}
			assert.Equal(t, test.advice, draftrank.AdviceOf(issues))
		})
	}
}

package draftrank

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// freshnessIssues reports provisional rules, news sources that were not
// current when the snapshot was built, and a snapshot or player pool older
// than staleAfter at now (never, when staleAfter is not positive).
func freshnessIssues(snapshot *SnapshotInfo, now time.Time, staleAfter time.Duration) []Issue {
	var issues []Issue
	if snapshot.League.Provisional {
		issues = append(issues, Issue{Code: IssueProvisionalRules, Message: "rules come from a temporary stand-in league and are provisional"})
	}
	for _, source := range snapshot.News {
		if issue, reported := newsIssue(source); reported {
			issues = append(issues, issue)
		}
	}
	if staleAfter <= 0 {
		return issues
	}
	if age := now.Sub(snapshot.AsOf); age > staleAfter {
		issues = append(issues, Issue{Code: IssueStaleSnapshot, Message: fmt.Sprintf("snapshot was computed %s ago", age.Truncate(time.Minute))})
	}
	if !snapshot.PoolFetchedAt.IsZero() && now.Sub(snapshot.PoolFetchedAt) > staleAfter {
		issues = append(issues, Issue{Code: IssueStalePool, Message: "player pool was fetched " + snapshot.PoolFetchedAt.Format(time.RFC3339)})
	}
	return issues
}

func newsIssue(source SourceFreshness) (Issue, bool) {
	label := fmt.Sprintf("news source %s (%s)", source.SourceID, source.Scope)
	switch news.CoverageStatus(source.Status) {
	case news.CoverageStale:
		return Issue{Code: IssueNewsSourceStale, Message: fmt.Sprintf("%s was stale: newest data from %s", label, source.DataAsOf.Format(time.RFC3339))}, true
	case news.CoverageFailing:
		return Issue{Code: IssueNewsSourceFailing, Message: fmt.Sprintf("%s was failing (%d consecutive failures): %s", label, source.ConsecutiveFailures, source.LastError)}, true
	case news.CoverageMissing:
		return Issue{Code: IssueNewsSourceMissing, Message: label + " had never been fetched; its news could not adjust anything"}, true
	}
	return Issue{}, false
}

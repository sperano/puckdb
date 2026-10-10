package draftboard

import (
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftwatch"
)

// Board issue codes. Board warnings share draftrank's Issue shape so ranking
// and recommendation issues keep their codes; staleness and completeness use
// the recommendation codes so one condition has one code.
const (
	IssueSyncPending                draftrank.IssueCode = "SYNC_PENDING"
	IssueSyncFailed                 draftrank.IssueCode = "SYNC_FAILED"
	IssueSkippedPicks               draftrank.IssueCode = "SKIPPED_PICKS"
	IssueRosterWarning              draftrank.IssueCode = "ROSTER_WARNING"
	IssueRecommendationsUnavailable draftrank.IssueCode = "RECOMMENDATIONS_UNAVAILABLE"
	IssueRecommendationsSuppressed  draftrank.IssueCode = "RECOMMENDATIONS_SUPPRESSED"
)

var (
	issueSyncPending = draftrank.Issue{Code: IssueSyncPending,
		Message: "Yahoo draft board has not completed its first synchronization"}
	issueSessionStale = draftrank.Issue{Code: draftrecommend.IssueBoardStale,
		Message: "Yahoo draft board is stale; last successful sync is outside the freshness window"}
	issueSessionIncomplete = draftrank.Issue{Code: draftrecommend.IssueBoardIncomplete,
		Message: "draft board is incomplete or has unresolved manual conflicts"}
	issueRecommendationsUnavailable = draftrank.Issue{Code: IssueRecommendationsUnavailable,
		Message: "draft recommendation service is unavailable"}
	issueRecommendationsSuppressed = draftrank.Issue{Code: IssueRecommendationsSuppressed,
		Message: "recommendations are suppressed because the current roster is not feasible"}
)

func statusOf(session draftwatch.Session, now time.Time, staleAfter time.Duration) Status {
	stale := session.LastSuccessAt != nil && staleAfter > 0 && now.Sub(*session.LastSuccessAt) > staleAfter
	status := Status{
		Version: session.State.Version, SyncVersion: session.SyncVersion, DraftStatus: session.DraftStatus,
		RecommendationsSafe: session.RecommendationsSafe, Complete: session.Complete,
		Stale: stale, LastPollAt: session.LastPollAt, LastSuccessAt: session.LastSuccessAt,
		LastAuthoritativeAt: session.LastAuthoritativeAt, LastError: session.LastError,
	}
	if session.LastSuccessAt == nil {
		status.Warnings = append(status.Warnings, issueSyncPending)
	} else if stale {
		status.Warnings = append(status.Warnings, issueSessionStale)
	}
	if !session.RecommendationsSafe {
		status.Warnings = append(status.Warnings, issueSessionIncomplete)
	}
	if session.LastError != "" {
		status.Warnings = append(status.Warnings, draftrank.Issue{Code: IssueSyncFailed,
			Message: "last Yahoo sync failed: " + session.LastError})
	}
	if session.SkippedPickCount > 0 {
		status.Warnings = append(status.Warnings, draftrank.Issue{Code: IssueSkippedPicks,
			Message: fmt.Sprintf("Yahoo board has %d unresolved or malformed picks", session.SkippedPickCount)})
	}
	return status
}

// appendIssues appends the issues not already present. The same condition
// reaches a board from its session, its ranking and its recommendation.
func appendIssues(issues []draftrank.Issue, more ...draftrank.Issue) []draftrank.Issue {
	for _, issue := range more {
		if !slices.Contains(issues, issue) {
			issues = append(issues, issue)
		}
	}
	return issues
}

// versionConflict marks a stale-version error as ErrVersionConflict and keeps
// it in the chain, so callers can still read its expected and current versions.
func versionConflict(stale error) error {
	return fmt.Errorf("%w: %w", ErrVersionConflict, stale)
}

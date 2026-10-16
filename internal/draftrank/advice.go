package draftrank

// Advice is what a ranking issue means for draft advice built on the
// snapshot. It is the one policy the rankings API, the live board and the
// recommendation service share.
type Advice int

const (
	// AdviceUnaffected issues are reported but leave advice as it is.
	AdviceUnaffected Advice = iota
	// AdviceOutdated issues mean the snapshot no longer reflects everything
	// known (it aged, overrides or non-ranking rules changed, a newer
	// snapshot exists or a later refresh failed). Advice stays available,
	// labeled stale: the snapshot is still internally consistent.
	AdviceOutdated
	// AdviceWithheld issues mean the snapshot was ranked under rules the
	// league no longer has, so its values or slot allocation are wrong for
	// the league. No advice may be built on it until a refresh.
	AdviceWithheld
)

var issueAdvice = map[IssueCode]Advice{
	IssueStaleSnapshot:      AdviceOutdated,
	IssueStalePool:          AdviceOutdated,
	IssueOverridesChanged:   AdviceOutdated,
	IssueRulesChanged:       AdviceOutdated,
	IssueNewerSnapshot:      AdviceOutdated,
	IssueRefreshFailed:      AdviceOutdated,
	IssueRefreshCanceled:    AdviceOutdated,
	IssueRefreshInterrupted: AdviceOutdated,
	IssueRosterSlotsChanged: AdviceWithheld,
	IssueScoringChanged:     AdviceWithheld,
}

// AdviceFor is the effect of one issue code; codes the policy does not name
// (snapshot-time news coverage, a refresh still running, codes owned by
// other packages) leave advice unaffected.
func AdviceFor(code IssueCode) Advice {
	return issueAdvice[code]
}

// AdviceOf is the strongest effect among issues.
func AdviceOf(issues []Issue) Advice {
	advice := AdviceUnaffected
	for _, issue := range issues {
		advice = max(advice, AdviceFor(issue.Code))
	}
	return advice
}

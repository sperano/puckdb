package draftrecommend

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/draftrank"
)

// Recommendation issue codes. They share draftrank's Issue shape so ranking
// issues keep their codes through a result; the codes below are owned by
// this package and never reach the GraphQL DraftIssueCode enum.
const (
	IssueBoardStale               draftrank.IssueCode = "BOARD_STALE"
	IssueBoardIncomplete          draftrank.IssueCode = "BOARD_INCOMPLETE"
	IssueScenarioMissing          draftrank.IssueCode = "SCENARIO_MISSING"
	IssuePoolSizeMismatch         draftrank.IssueCode = "POOL_SIZE_MISMATCH"
	IssuePickOrderMissing         draftrank.IssueCode = "PICK_ORDER_MISSING"
	IssuePickOutsideOrder         draftrank.IssueCode = "PICK_OUTSIDE_ORDER"
	IssueNoRemainingPicks         draftrank.IssueCode = "NO_REMAINING_PICKS"
	IssueTurnEstimateOmitted      draftrank.IssueCode = "TURN_ESTIMATE_OMITTED"
	IssueTurnOutsideOrder         draftrank.IssueCode = "TURN_OUTSIDE_ORDER"
	IssueMissingPlacement         draftrank.IssueCode = "MISSING_PLACEMENT"
	IssueMissingRosterEligibility draftrank.IssueCode = "MISSING_ROSTER_ELIGIBILITY"
	IssueNoFeasiblePlayer         draftrank.IssueCode = "NO_FEASIBLE_PLAYER"
	// IssueUnclassified is the code of a stored legacy issue string that
	// matches no known message.
	IssueUnclassified draftrank.IssueCode = "UNCLASSIFIED"
)

// The fixed issues Evaluate reports. Their messages are the exact strings
// draft-recommendation-v1 results stored before issues carried codes.
var (
	issueBoardStale      = draftrank.Issue{Code: IssueBoardStale, Message: "draft board is stale"}
	issueBoardIncomplete = draftrank.Issue{Code: IssueBoardIncomplete,
		Message: "draft board is incomplete or has unresolved manual conflicts"}
	issueScenarioFallback = draftrank.Issue{Code: draftrank.IssueScenarioFallback,
		Message: "requested news scenario unavailable; baseline scenario used"}
	issueScenarioMissing = draftrank.Issue{Code: IssueScenarioMissing,
		Message: "requested scenario is unavailable; no recommendations generated"}
	issuePoolSizeMismatch = draftrank.Issue{Code: IssuePoolSizeMismatch,
		Message: "ranking pool size does not match its stored players"}
	issuePickOrderMissing = draftrank.Issue{Code: IssuePickOrderMissing,
		Message: "verified chronological pick order is missing"}
	issuePickOutsideOrder = draftrank.Issue{Code: IssuePickOutsideOrder,
		Message: "draft board contains a pick outside the verified order"}
	issueNoRemainingPicks = draftrank.Issue{Code: IssueNoRemainingPicks,
		Message: "draft has no remaining picks in the verified order"}
	issueTurnEstimateOmitted = draftrank.Issue{Code: IssueTurnEstimateOmitted,
		Message: "verified chronological pick order is unavailable; turn estimate omitted"}
	issueTurnOutsideOrder = draftrank.Issue{Code: IssueTurnOutsideOrder,
		Message: "our next turn is absent from the supplied draft order"}
	issueMissingRosterEligibility = draftrank.Issue{Code: IssueMissingRosterEligibility,
		Message: "ranking snapshot lacks full Yahoo roster eligibility; reserve-slot feasibility is unavailable"}
	issueNoFeasiblePlayer = draftrank.Issue{Code: IssueNoFeasiblePlayer,
		Message: "no available player is feasible for the current roster"}

	legacyIssues = []draftrank.Issue{
		issueBoardStale, issueBoardIncomplete, issueScenarioFallback, issueScenarioMissing,
		issuePoolSizeMismatch, issuePickOrderMissing, issuePickOutsideOrder, issueNoRemainingPicks,
		issueTurnEstimateOmitted, issueTurnOutsideOrder, issueMissingRosterEligibility, issueNoFeasiblePlayer,
	}
)

const (
	legacyCodeSeparator          = ": "
	legacyMissingPlacementPrefix = "ranking player "
	legacyMissingPlacementSuffix = " lacks requested scenario placement"
)

func missingPlacementIssue(playerKey string) draftrank.Issue {
	return draftrank.Issue{Code: IssueMissingPlacement,
		Message: legacyMissingPlacementPrefix + playerKey + legacyMissingPlacementSuffix}
}

// Issues are a result's coded conditions. Results stored before issues had
// codes hold plain strings; decoding maps them back to their codes so old
// runs stay readable for audit and replay.
type Issues []draftrank.Issue

// UnmarshalJSON accepts the coded form and the legacy string form.
func (issues *Issues) UnmarshalJSON(raw []byte) error {
	var coded []draftrank.Issue
	codedErr := json.Unmarshal(raw, &coded)
	if codedErr == nil {
		*issues = coded
		return nil
	}
	var legacy []string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("decode recommendation issues: %w", codedErr)
	}
	decoded := make(Issues, len(legacy))
	for i, message := range legacy {
		decoded[i] = legacyIssue(message)
	}
	*issues = decoded
	return nil
}

// legacyIssue recovers the code of one legacy issue string: a fixed message,
// a missing-placement message, or a ranking issue flattened to "CODE: message".
func legacyIssue(message string) draftrank.Issue {
	for _, issue := range legacyIssues {
		if issue.Message == message {
			return issue
		}
	}
	if strings.HasPrefix(message, legacyMissingPlacementPrefix) && strings.HasSuffix(message, legacyMissingPlacementSuffix) {
		return draftrank.Issue{Code: IssueMissingPlacement, Message: message}
	}
	if code, text, found := strings.Cut(message, legacyCodeSeparator); found && isIssueCode(code) {
		return draftrank.Issue{Code: draftrank.IssueCode(code), Message: text}
	}
	return draftrank.Issue{Code: IssueUnclassified, Message: message}
}

func isIssueCode(code string) bool {
	if code == "" {
		return false
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

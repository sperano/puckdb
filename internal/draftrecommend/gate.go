package draftrecommend

import (
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

// withoutCandidates labels a result that must not carry candidates: a
// stale or unsafe board, or a ranking whose advice is withheld. A withheld
// ranking stops here, before the roster is checked against its obsolete
// slots.
func withoutCandidates(result *Result, in Input) bool {
	if in.SessionStale {
		result.BoardStatus = BoardStatusStale
		result.Issues = append(result.Issues, issueBoardStale)
	}
	if !in.SessionSafe || !draftsession.SafeToRecommend(in.Session) || in.SessionStale {
		if !in.SessionStale {
			result.BoardStatus = BoardStatusIncomplete
		}
		result.Issues = append(result.Issues, issueBoardIncomplete)
		return true
	}
	if draftrank.AdviceOf(in.RankingIssues) == draftrank.AdviceWithheld {
		result.BoardStatus = BoardStatusWithheld
		return true
	}
	return false
}

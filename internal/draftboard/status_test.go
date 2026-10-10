package draftboard

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type discardRunStore struct{}

func (discardRunStore) Save(context.Context, draftrecommend.Input, draftrecommend.Result) (uuid.UUID, error) {
	return uuid.New(), nil
}

// A ranking issue reaches the board both directly and through the real
// recommendation service; it must arrive once and with its code, next to the
// coded session and recommendation conditions.
func TestBoardWarningsKeepIssueCodesThroughRecommendationService(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	ranking := boardRanking(identity.LeagueKey)
	ranking.Meta.League.RulesHash = "rules-v1"
	newsDown := draftrank.Issue{Code: draftrank.IssueNewsAdjustmentsDown, Message: "news service unavailable"}
	ranking.Meta.Unavailable = []draftrank.Issue{newsDown}
	lastSuccess := now
	data := &fakeDataSource{identity: identity, ranking: ranking, session: draftwatch.Session{
		Identity: identity, State: draftsession.State{Version: 2}, LastSuccessAt: &lastSuccess, LastError: "HTTP 503",
	}, leagueData: LeagueData{TeamID: 3,
		Rules: draft.Snapshot{Rules: draft.Rules{RosterSlots: []draft.RosterSlot{{Position: draft.PositionCenter, Count: 1, Starting: true}}}},
	}}
	service := NewService(data, draftrecommend.NewService(discardRunStore{}), nil,
		Options{Now: func() time.Time { return now }, StaleAfter: time.Minute})

	board, err := service.Board(context.Background(), Request{League: identity.LeagueKey})

	require.NoError(t, err)
	require.NotNil(t, board.Recommendation)
	assert.Contains(t, board.Recommendation.Result.Issues, newsDown)
	want := []draftrank.Issue{
		issueSessionIncomplete,
		{Code: IssueSyncFailed, Message: "last Yahoo sync failed: HTTP 503"},
	}
	for _, warning := range board.Roster.Warnings {
		want = append(want, draftrank.Issue{Code: IssueRosterWarning, Message: warning})
	}
	want = append(want, newsDown, draftrank.Issue{Code: draftrecommend.IssuePickOrderMissing,
		Message: "verified chronological pick order is missing"})
	assert.Equal(t, want, board.Session.Warnings)
}

func TestStatusCodesEverySessionCondition(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-time.Hour)
	stale := statusOf(draftwatch.Session{LastSuccessAt: &lastSuccess, RecommendationsSafe: true, SkippedPickCount: 2},
		now, time.Minute)
	assert.Equal(t, []draftrank.Issue{issueSessionStale,
		{Code: IssueSkippedPicks, Message: "Yahoo board has 2 unresolved or malformed picks"}}, stale.Warnings)

	pending := statusOf(draftwatch.Session{RecommendationsSafe: true}, now, time.Minute)
	assert.Equal(t, []draftrank.Issue{issueSyncPending}, pending.Warnings)
}

func TestBoardWithoutRecommendationServiceCodesTheGap(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	data := &fakeDataSource{identity: identity, ranking: boardRanking(identity.LeagueKey),
		session:    draftwatch.Session{Identity: identity, LastSuccessAt: &now, RecommendationsSafe: true},
		leagueData: LeagueData{TeamID: 3}}

	board, err := NewService(data, nil, nil, Options{Now: func() time.Time { return now }}).
		Board(context.Background(), Request{League: identity.LeagueKey})

	require.NoError(t, err)
	assert.Contains(t, board.Session.Warnings, issueRecommendationsUnavailable)
	assert.NotContains(t, board.Session.Warnings, issueRecommendationsSuppressed)
}

func TestAppendIssuesSkipsExactDuplicatesOnly(t *testing.T) {
	stale := draftrank.Issue{Code: draftrecommend.IssueBoardStale, Message: "draft board is stale"}
	issues := appendIssues([]draftrank.Issue{issueSessionStale}, stale, issueSessionStale, stale)
	assert.Equal(t, []draftrank.Issue{issueSessionStale, stale}, issues,
		"the same code with a different message is a different warning")
}

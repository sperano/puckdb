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
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// rankingStaleAfter is the rankings API default (--draft-stale-after).
	rankingStaleAfter = 24 * time.Hour
	// sessionStaleAfter is a one-hour Yahoo-session freshness window.
	sessionStaleAfter = time.Hour
	sharedReadTeamID  = 1
)

var sharedReadIdentity = draftwatch.Identity{LeagueKey: draftfixtures.LeagueKey, Season: draftfixtures.Season,
	LeagueID: draftfixtures.LeagueID, GameKey: 465}

// sharedReadData is a board data source whose ranking comes from a real
// draftrank.Service over the in-memory fixture store, through the same
// readRanking the PostgreSQL data source uses.
type sharedReadData struct {
	*fakeDataSource
	rankings *draftrank.Service
}

func (d *sharedReadData) Ranking(ctx context.Context, id draftwatch.Identity) (draftrank.Read, error) {
	return readRanking(ctx, d.rankings, id)
}

// evaluatingEngine runs the real deterministic evaluation and counts how
// often the board asks for one.
type evaluatingEngine struct {
	inputs []draftrecommend.Input
}

func (e *evaluatingEngine) Recommend(_ context.Context, input draftrecommend.Input) (draftrecommend.StoredRun, error) {
	e.inputs = append(e.inputs, input)
	result, err := draftrecommend.Evaluate(input)
	return draftrecommend.StoredRun{ID: uuid.New(), Input: input, Result: result}, err
}

type sharedReadBoard struct {
	store   *draftfixtures.Store
	data    *sharedReadData
	engine  *evaluatingEngine
	service *Service
	now     time.Time
}

// newSharedReadBoard serves the fixture snapshot to a board whose Yahoo
// session synced a minute ago, so only the ranking can make it unsafe.
func newSharedReadBoard(t *testing.T) *sharedReadBoard {
	t.Helper()
	snapshot, err := draftfixtures.Snapshot(uuid.NewSHA1(uuid.NameSpaceOID, []byte("board")), draftfixtures.AsOf)
	require.NoError(t, err)
	board := &sharedReadBoard{store: draftfixtures.NewStore(), engine: &evaluatingEngine{}, now: draftfixtures.AsOf.Add(time.Hour)}
	board.store.Add(snapshot)
	clock := func() time.Time { return board.now }
	board.data = &sharedReadData{
		rankings: draftrank.NewService(board.store, board.store, draftrank.ServiceOptions{StaleAfter: rankingStaleAfter, Now: clock}),
		fakeDataSource: &fakeDataSource{identity: sharedReadIdentity,
			leagueData: LeagueData{Rules: draftfixtures.Rules(), TeamID: sharedReadTeamID, TeamName: "Fixture team"}},
	}
	board.service = NewService(board.data, board.engine, nil, Options{StaleAfter: sessionStaleAfter, Now: clock})
	return board
}

func (b *sharedReadBoard) load(t *testing.T) Board {
	t.Helper()
	lastSuccess := b.now.Add(-time.Minute)
	b.data.session = draftwatch.Session{Identity: sharedReadIdentity, RecommendationsSafe: true, Complete: true,
		LastSuccessAt: &lastSuccess, State: draftsession.State{UpstreamComplete: true, Version: 1,
			Upstream: map[draftsession.PickKey]draftsession.Pick{}, Manual: map[draftsession.PickKey]draftsession.ManualChange{}}}
	board, err := b.service.Board(context.Background(), Request{League: draftfixtures.LeagueKey, Season: draftfixtures.Season})
	require.NoError(t, err)
	return board
}

// changeRosterSlots re-imports the league's rules with one more center, as
// both the ranking store and the league data would then read them.
func (b *sharedReadBoard) changeRosterSlots() {
	key := draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)
	rules := b.store.Rules[key]
	rules.Rules.RosterSlots = append([]draft.RosterSlot(nil), rules.Rules.RosterSlots...)
	rules.Rules.RosterSlots[0].Count++
	b.store.Rules[key] = rules
	b.data.leagueData.Rules = rules
}

func (b *sharedReadBoard) failRefresh() {
	b.store.Refreshes[draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)] = &draftrank.Refresh{
		State: draftrank.RefreshFailed, Code: draftrank.IssueMissingProjections, Error: "2 players lack projections",
		StartedAt: b.now.Add(-time.Minute)}
}

func codesOf(issues []draftrank.Issue) []draftrank.IssueCode {
	codes := make([]draftrank.IssueCode, len(issues))
	for i, issue := range issues {
		codes[i] = issue.Code
	}
	return codes
}

func TestBoardSharesRankingReadStatus(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*sharedReadBoard)
		codes       []draftrank.IssueCode
		boardStatus string
		candidates  bool
	}{
		{name: "current ranking", change: func(*sharedReadBoard) {},
			boardStatus: draftrecommend.BoardStatusReady, candidates: true},
		{name: "stale snapshot and pool", change: func(b *sharedReadBoard) { b.now = draftfixtures.AsOf.Add(26 * time.Hour) },
			codes:       []draftrank.IssueCode{draftrank.IssueStaleSnapshot, draftrank.IssueStalePool},
			boardStatus: draftrecommend.BoardStatusStale, candidates: true},
		{name: "changed overrides", change: func(b *sharedReadBoard) { b.store.Changes = 1 },
			codes:       []draftrank.IssueCode{draftrank.IssueOverridesChanged},
			boardStatus: draftrecommend.BoardStatusStale, candidates: true},
		{name: "failed refresh", change: (*sharedReadBoard).failRefresh,
			codes:       []draftrank.IssueCode{draftrank.IssueRefreshFailed},
			boardStatus: draftrecommend.BoardStatusStale, candidates: true},
		{name: "changed roster rules", change: (*sharedReadBoard).changeRosterSlots,
			codes:       []draftrank.IssueCode{draftrank.IssueRulesChanged, draftrank.IssueRosterSlotsChanged},
			boardStatus: draftrecommend.BoardStatusWithheld, candidates: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b := newSharedReadBoard(t)
			test.change(b)

			board := b.load(t)

			want := append([]draftrank.IssueCode{draftrank.IssueNewsSourceStale}, test.codes...)
			assert.ElementsMatch(t, want, codesOf(board.RankingIssues), "the board carries the ranking read's issues")
			read, err := b.data.rankings.Read(context.Background(),
				draftrank.LeagueRef{Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID}, uuid.Nil)
			require.NoError(t, err)
			assert.Equal(t, read.Issues, board.RankingIssues, "the board and the rankings API report the same issues")
			assert.False(t, board.Session.Stale, "the Yahoo session itself is fresh")
			for _, issue := range board.RankingIssues {
				assert.Contains(t, board.Session.Warnings, issue, "every ranking issue is shown as a board warning")
			}
			require.Len(t, b.engine.inputs, 1)
			assert.Equal(t, board.RankingIssues, b.engine.inputs[0].RankingIssues, "recommendations see the same status")
			require.NotNil(t, board.Recommendation)
			assert.Equal(t, test.boardStatus, board.Recommendation.Result.BoardStatus)
			assert.Equal(t, test.candidates, len(board.Recommendation.Result.Candidates) > 0)
		})
	}
}

func TestBoardChecksRosterUnderCurrentSlotsWhileRankingRulesAreWithheld(t *testing.T) {
	b := newSharedReadBoard(t)
	b.changeRosterSlots()

	board := b.load(t)

	assert.Equal(t, 2, board.Roster.OpenSlots[draft.PositionCenter], "the roster shows the league's current slots")
	assert.Equal(t, 1, board.Snapshot.League.RosterSlots[0].Count, "the snapshot still holds the slots it was ranked under")
	assert.Empty(t, board.Recommendation.Result.Candidates, "no advice allocates players to the obsolete slots")
	assert.Nil(t, board.Recommendation.Result.BestRosterFit)
}

func TestBoardRecommendationCacheFollowsRankingSafety(t *testing.T) {
	b := newSharedReadBoard(t)
	steps := []struct {
		name   string
		change func(*sharedReadBoard)
		calls  int
	}{
		{"first read", func(*sharedReadBoard) {}, 1},
		{"same status", func(*sharedReadBoard) {}, 1},
		{"snapshot turns stale", func(b *sharedReadBoard) { b.now = draftfixtures.AsOf.Add(26 * time.Hour) }, 2},
		{"stale an hour longer", func(b *sharedReadBoard) { b.now = b.now.Add(time.Hour) }, 2},
		{"overrides change", func(b *sharedReadBoard) { b.store.Changes = 1 }, 3},
		{"a refresh fails", (*sharedReadBoard).failRefresh, 4},
		{"roster rules change", (*sharedReadBoard).changeRosterSlots, 5},
		{"same withheld status", func(*sharedReadBoard) {}, 5},
	}
	for _, step := range steps {
		step.change(b)
		board := b.load(t)
		assert.Equal(t, step.calls, len(b.engine.inputs), step.name)
		assert.Equal(t, uint64(1), board.Session.Version, "the Yahoo session version never changes: %s", step.name)
	}
}

func TestBoardShowsCurrentRankingMessagesWithCachedRecommendation(t *testing.T) {
	b := newSharedReadBoard(t)
	b.now = draftfixtures.AsOf.Add(26 * time.Hour)
	b.load(t)
	b.now = b.now.Add(time.Hour)

	board := b.load(t)

	require.Len(t, b.engine.inputs, 1, "an older message of the same condition reuses the recommendation")
	stale := issuesWithCode(board.Session.Warnings, draftrank.IssueStaleSnapshot)
	require.Len(t, stale, 1, "one warning per condition")
	assert.Equal(t, "snapshot was computed 27h0m0s ago", stale[0].Message)
}

func TestBoardRefusesRulesImportedBetweenReads(t *testing.T) {
	b := newSharedReadBoard(t)
	b.data.leagueData.Rules.Rules.NumTeams++

	_, err := b.service.Board(context.Background(), Request{League: draftfixtures.LeagueKey, Season: draftfixtures.Season})

	assert.ErrorContains(t, err, "league rules of 465.l.1001 changed while the board was loading")
	assert.Empty(t, b.engine.inputs)
}

func TestReadRankingRequiresASnapshot(t *testing.T) {
	store := draftfixtures.NewStore()
	rankings := draftrank.NewService(store, store, draftrank.ServiceOptions{})

	_, err := readRanking(context.Background(), rankings, sharedReadIdentity)

	assert.ErrorContains(t, err, "draft ranking snapshot is not available for 465.l.1001 (ranking status NOT_COMPUTED)")
}

func issuesWithCode(issues []draftrank.Issue, code draftrank.IssueCode) []draftrank.Issue {
	var matching []draftrank.Issue
	for _, issue := range issues {
		if issue.Code == code {
			matching = append(matching, issue)
		}
	}
	return matching
}

func TestRecommendationCacheKeyUsesRankingIssueCodesNotMessages(t *testing.T) {
	input := draftrecommend.Input{SessionLeagueKey: "500.l.5621", Session: draftsession.State{Version: 9},
		Ranking: boardRanking("500.l.5621"), OurTeamID: 3, SessionSafe: true}
	key := func(issues ...draftrank.Issue) string {
		t.Helper()
		input.RankingIssues = issues
		value, err := recommendationCacheKey(input)
		require.NoError(t, err)
		return value
	}
	stale := draftrank.Issue{Code: draftrank.IssueStaleSnapshot, Message: "snapshot was computed 26h0m0s ago"}
	staler := draftrank.Issue{Code: draftrank.IssueStaleSnapshot, Message: "snapshot was computed 27h0m0s ago"}
	withheld := draftrank.Issue{Code: draftrank.IssueRosterSlotsChanged, Message: "slots changed"}

	assert.NotEqual(t, key(), key(stale), "a ranking turning stale is a new recommendation boundary")
	assert.Equal(t, key(stale), key(staler), "an aging message alone keeps the cached recommendation")
	assert.Equal(t, key(stale, withheld), key(withheld, stale, staler), "codes are compared as a set")
	assert.NotEqual(t, key(stale), key(stale, withheld))
}

package draftboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoardComposesFreshnessHistoryRosterAvailableNewsAndNoGuessedTurn(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	identity := draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
	state := draftsession.State{
		Upstream: map[draftsession.PickKey]draftsession.Pick{
			{Round: 1, Pick: 1}: {Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 7, PlayerID: 101},
			{Round: 1, Pick: 2}: {Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 3, PlayerID: 102},
		},
		Manual: map[draftsession.PickKey]draftsession.ManualChange{}, UpstreamComplete: true, Version: 9,
	}
	lastSuccess := now.Add(-2 * time.Minute)
	data := &fakeDataSource{identity: identity, session: draftwatch.Session{
		Identity: identity, State: state, RecommendationsSafe: true, LastSuccessAt: &lastSuccess,
		SyncVersion: 14,
	}, ranking: boardRanking(identity.LeagueKey), leagueData: LeagueData{
		TeamID: 3, TeamName: "My team", DraftPosition: 2, DraftFormat: "snake",
		Rules: draft.Snapshot{Rules: draft.Rules{RosterSlots: []draft.RosterSlot{{Position: draft.PositionCenter, Count: 4, Starting: true}}}},
	}}
	engine := &fakeRecommendationEngine{result: draftrecommend.Result{
		Scenario: draftrank.ScenarioBase, Issues: []string{"verified chronological pick order is unavailable"},
		Candidates: []draftrecommend.Candidate{{PlayerKey: "500.p.103", ValueRank: 1, RosterFitRank: 1}},
	}}
	service := NewService(data, engine, nil, Options{Now: func() time.Time { return now }, StaleAfter: time.Minute})

	board, err := service.Board(context.Background(), Request{League: identity.LeagueKey})

	require.NoError(t, err)
	assert.True(t, board.Session.Stale)
	assert.Equal(t, uint64(14), board.Session.SyncVersion)
	assert.Len(t, board.Picks, 2)
	assert.Equal(t, draftsession.SourceYahoo, board.Picks[0].Source)
	assert.Equal(t, 1, len(board.Roster.Players), "only our own drafted pick belongs in the personal roster")
	assert.Equal(t, 102, board.Roster.Players[0].YahooPlayerID)
	assert.Len(t, board.Available, 1)
	assert.Equal(t, "500.p.103", board.Available[0].PlayerKey)
	assert.Equal(t, 2, board.Available[0].BaselineRank)
	assert.Equal(t, 1, board.Available[0].SelectedRank)
	assert.Equal(t, "report changed availability", board.Available[0].News[0].Detail)
	assert.Nil(t, board.CurrentPick, "turns must stay unknown without a verified chronological order")
	assert.Nil(t, board.PicksUntilTurn)
	assert.Equal(t, uint64(9), engine.input.Session.Version)
	assert.True(t, engine.input.SessionStale)
	_, err = service.Board(context.Background(), Request{League: identity.LeagueKey})
	require.NoError(t, err)
	assert.Equal(t, 1, engine.calls, "polling the same frozen boundary reuses its persisted recommendation")
}

func TestRecommendationCacheKeyIncludesSessionSafetyAndFreshness(t *testing.T) {
	input := draftrecommend.Input{
		SessionLeagueKey: "500.l.5621",
		Session:          draftsession.State{Version: 9},
		Ranking:          boardRanking("500.l.5621"),
		OurTeamID:        3,
		SessionSafe:      true,
	}
	freshKey, err := recommendationCacheKey(input)
	require.NoError(t, err)

	input.SessionStale = true
	staleKey, err := recommendationCacheKey(input)
	require.NoError(t, err)
	assert.NotEqual(t, freshKey, staleKey)

	input.SessionStale = false
	input.SessionSafe = false
	unsafeKey, err := recommendationCacheKey(input)
	require.NoError(t, err)
	assert.NotEqual(t, freshKey, unsafeKey)
}

func TestSetShortlistRejectsStaleVersion(t *testing.T) {
	data := &fakeDataSource{identity: draftwatch.Identity{LeagueKey: "500.l.5621"},
		session: draftwatch.Session{State: draftsession.State{Version: 4}}}
	service := NewService(data, nil, nil, Options{})
	err := service.SetShortlist(context.Background(), "500.l.5621", 2026, "500.p.103", 3, true)
	assert.ErrorIs(t, err, ErrVersionConflict)
	assert.False(t, data.shortlistChanged)
}

func TestManualMutationUsesExpectedVersionAndMapsConflict(t *testing.T) {
	data := &fakeDataSource{identity: draftwatch.Identity{LeagueKey: "500.l.5621"},
		applyErr: &draftwatch.StaleStateVersionError{Expected: 4, Actual: 5}}
	service := NewService(data, nil, nil, Options{})
	_, err := service.ApplyManual(context.Background(), "500.l.5621", 2026,
		draftsession.ManualOperation{Kind: draftsession.ManualUndo}, 4, time.Now())
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestRosterDoesNotIncludeOpponentPicks(t *testing.T) {
	identity := "500.l.5621"
	state := draftsession.State{Upstream: map[draftsession.PickKey]draftsession.Pick{
		{Round: 1, Pick: 1}: {Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 8, PlayerID: 101},
		{Round: 1, Pick: 2}: {Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 3, PlayerID: 102},
	}}
	roster := rosterOf(state, nil, boardRanking(identity), []draft.RosterSlot{{Position: draft.PositionCenter, Count: 3, Starting: true}}, 3)
	require.Len(t, roster.Players, 1)
	assert.Equal(t, 102, roster.Players[0].YahooPlayerID)
}

func TestConflictedUndoRemainsVisibleUntilEitherResolution(t *testing.T) {
	state := conflictedUndoState(t)
	rows := picksOf(state, boardRanking("500.l.5621"))
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Conflict)
	assert.True(t, rows[0].Undone)
	assert.Equal(t, 102, rows[0].PlayerID)

	kept, _, err := draftsession.ResolveConflict(state, draftsession.PickKey{Round: 1, Pick: 1}, draftsession.KeepManual)
	require.NoError(t, err)
	assert.Empty(t, picksOf(kept, boardRanking("500.l.5621")))

	accepted, _, err := draftsession.ResolveConflict(state, draftsession.PickKey{Round: 1, Pick: 1}, draftsession.AcceptUpstream)
	require.NoError(t, err)
	acceptedRows := picksOf(accepted, boardRanking("500.l.5621"))
	require.Len(t, acceptedRows, 1)
	assert.False(t, acceptedRows[0].Conflict)
	assert.False(t, acceptedRows[0].Undone)
	assert.Equal(t, draftsession.SourceYahoo, acceptedRows[0].Source)
}

func conflictedUndoState(t *testing.T) draftsession.State {
	t.Helper()
	key := draftsession.PickKey{Round: 1, Pick: 1}
	state, _, err := draftsession.Reconcile(draftsession.State{}, draftsession.Snapshot{
		Picks:    []draftsession.ObservedPick{{Key: key, TeamID: 3, PlayerID: 101}},
		RawCount: 1, ExpectedCount: 1, HasExpectedCount: true, Authoritative: true,
	})
	require.NoError(t, err)
	state, _, err = draftsession.ApplyManual(state, draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: key})
	require.NoError(t, err)
	state, _, err = draftsession.Reconcile(state, draftsession.Snapshot{
		Picks:    []draftsession.ObservedPick{{Key: key, TeamID: 3, PlayerID: 102}},
		RawCount: 1, ExpectedCount: 1, HasExpectedCount: true, Authoritative: true,
	})
	require.NoError(t, err)
	return state
}

func boardRanking(leagueKey string) *draftrank.Snapshot {
	asOf := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	news := &newsadjust.PlayerAdjustment{Reasons: []newsadjust.Reason{{Detail: "report changed availability",
		EffectiveFrom: asOf, LatestEvidenceAt: asOf.Add(time.Hour), Scenarios: []newsadjust.Scenario{newsadjust.ScenarioBase}}}}
	return &draftrank.Snapshot{SnapshotInfo: draftrank.SnapshotInfo{
		ID: uuid.New(), Identity: "rank-identity", AsOf: asOf,
		Meta: draftrank.Meta{League: draftrank.League{LeagueKey: leagueKey}, PoolSize: 3,
			Projection: draftrank.ProjectionInfo{SnapshotID: uuid.New(), ModelVersion: "projection-v1"},
			Versions:   map[draftrank.Scenario]string{draftrank.ScenarioBaseline: "baseline-v1", draftrank.ScenarioBase: "base-v1"},
			Scenarios:  []draftrank.Scenario{draftrank.ScenarioBaseline, draftrank.ScenarioBase}},
	}, Players: []draftrank.Player{
		{PlayerKey: "500.p.101", YahooPlayerID: 101, Name: "Opponent pick", EligiblePositions: []string{"C"}, RosterEligiblePositions: []string{"C"}, Placements: placements(3, 3)},
		{PlayerKey: "500.p.102", YahooPlayerID: 102, Name: "Our pick", EligiblePositions: []string{"C"}, RosterEligiblePositions: []string{"C"}, Placements: placements(4, 4)},
		{PlayerKey: "500.p.103", YahooPlayerID: 103, Name: "Available", EligiblePositions: []string{"C"}, RosterEligiblePositions: []string{"C"}, Placements: placements(2, 1), Adjustment: news},
	}}
}

func placements(baselineRank, selectedRank int) map[draftrank.Scenario]draftrank.Placement {
	return map[draftrank.Scenario]draftrank.Placement{
		draftrank.ScenarioBaseline: {OverallRank: baselineRank, AdjustedValue: float64(baselineRank)},
		draftrank.ScenarioBase:     {OverallRank: selectedRank, AdjustedValue: float64(selectedRank)},
	}
}

type fakeDataSource struct {
	identity         draftwatch.Identity
	session          draftwatch.Session
	ranking          *draftrank.Snapshot
	leagueData       LeagueData
	applyErr         error
	shortlistChanged bool
}

func (f *fakeDataSource) ResolveIdentity(context.Context, string, int) (draftwatch.Identity, error) {
	return f.identity, nil
}
func (f *fakeDataSource) Session(context.Context, draftwatch.Identity) (draftwatch.Session, error) {
	return f.session, nil
}
func (f *fakeDataSource) Ranking(context.Context, draftwatch.Identity) (*draftrank.Snapshot, error) {
	return f.ranking, nil
}
func (f *fakeDataSource) LeagueData(context.Context, draftwatch.Identity) (LeagueData, error) {
	return f.leagueData, nil
}
func (f *fakeDataSource) ListShortlist(context.Context, string) ([]string, error) { return nil, nil }
func (f *fakeDataSource) SetShortlist(context.Context, string, string, uint64, bool) error {
	f.shortlistChanged = true
	return nil
}
func (f *fakeDataSource) ApplyManual(context.Context, draftwatch.Identity, draftsession.ManualOperation,
	uint64, time.Time) (draftwatch.Session, draftsession.Report, error) {
	return draftwatch.Session{}, draftsession.Report{}, f.applyErr
}
func (f *fakeDataSource) ResolveConflict(context.Context, draftwatch.Identity, draftsession.PickKey,
	draftsession.ConflictChoice, uint64, time.Time) (draftwatch.Session, draftsession.Report, error) {
	return draftwatch.Session{}, draftsession.Report{}, f.applyErr
}
func (f *fakeDataSource) ListEvents(context.Context, string, uint64, int) ([]draftwatch.Event, error) {
	return nil, nil
}

type fakeRecommendationEngine struct {
	input  draftrecommend.Input
	result draftrecommend.Result
	calls  int
}

func (f *fakeRecommendationEngine) Recommend(_ context.Context, input draftrecommend.Input) (draftrecommend.StoredRun, error) {
	f.input = input
	f.calls++
	return draftrecommend.StoredRun{ID: uuid.New(), Input: input, Result: f.result}, nil
}

func TestSetShortlistVersionCheckAllowsCurrentVersion(t *testing.T) {
	data := &fakeDataSource{identity: draftwatch.Identity{LeagueKey: "500.l.5621"},
		session: draftwatch.Session{State: draftsession.State{Version: 4}}}
	service := NewService(data, nil, nil, Options{})
	require.NoError(t, service.SetShortlist(context.Background(), "500.l.5621", 2026, "500.p.103", 4, true))
	assert.True(t, data.shortlistChanged)
}

func TestBoardRejectsUnavailableOwnedTeam(t *testing.T) {
	data := &fakeDataSource{identity: draftwatch.Identity{LeagueKey: "500.l.5621"},
		ranking: boardRanking("500.l.5621")}
	_, err := NewService(data, nil, nil, Options{}).Board(context.Background(), Request{League: "500.l.5621"})
	assert.True(t, errors.Is(err, ErrNoOwnedTeam))
}

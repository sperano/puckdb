package graph

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftboard"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMauriceDraftBoardGraphQLQueryAndVersionedManualPick(t *testing.T) {
	server, data := newDraftBoardTestServer(t)
	initial := fetchGraphDraftBoard(t, server)
	assert.Equal(t, "FRESH", initial.Sync.Connection)
	assert.False(t, initial.Turn.OrderKnown)
	assert.Equal(t, 0, initial.Turn.PicksUntilNextTurn)
	assert.Contains(t, initial.Turn.TimingLabel, "estimate")
	require.Positive(t, initial.TotalAvailable)
	playerKey := centerPlayerKey(data.ranking.Players)
	require.NotEmpty(t, playerKey)
	mutation := `mutation($input: MauriceDraftManualPickInput!) { applyMauriceDraftPick(input: $input) {
		stateVersion syncVersion
	} }`
	manualInput := map[string]any{
		"league": draftfixtures.LeagueKey, "season": draftfixtures.Season, "action": "ADD",
		"round": 1, "pick": 1, "teamKey": draftfixtures.LeagueKey + ".t.3", "playerKey": playerKey,
		"expectedStateVersion": 0, "clientMutationId": "pick-1",
	}
	added := postGraphQL(t, server, nil, mutation, map[string]any{"input": manualInput})
	require.Empty(t, added.Errors)
	assert.Equal(t, uint64(1), data.session.State.Version)

	duplicate := postGraphQL(t, server, nil, mutation, map[string]any{"input": manualInput})
	require.NotEmpty(t, duplicate.Errors)
	assert.Contains(t, duplicate.Errors[0].Message, draftboard.ErrVersionConflict.Error())

	updated := fetchGraphDraftBoard(t, server)
	assert.Equal(t, int64(1), updated.Sync.StateVersion)
	assert.Less(t, updated.TotalAvailable, initial.TotalAvailable)
	require.Len(t, updated.Roster.Assignments, 1)
	assert.Equal(t, playerKey, updated.Roster.Assignments[0].PlayerKey)
}

const graphDraftBoardQuery = `query($input: MauriceDraftBoardInput!) { mauriceDraftBoard(input: $input) {
	sync { connection stateVersion syncVersion recommendationsSafe }
	turn { orderKnown timingLabel pickTimeSeconds picksUntilNextTurn }
	roster { feasible assignments { playerKey slot } }
	totalAvailable available { playerKey baselineRank scenarioRank rosterFitRank recommendationReasons }
	recommendations { bestValue { playerKey name recommendationReasons } bestRosterFit { playerKey name recommendationReasons } }
} }`

type graphDraftBoardView struct {
	TotalAvailable int `json:"totalAvailable"`
	Sync           struct {
		Connection   string `json:"connection"`
		StateVersion int64  `json:"stateVersion"`
	} `json:"sync"`
	Turn struct {
		OrderKnown         bool   `json:"orderKnown"`
		PicksUntilNextTurn int    `json:"picksUntilNextTurn"`
		TimingLabel        string `json:"timingLabel"`
	} `json:"turn"`
	Available []struct {
		PlayerKey string `json:"playerKey"`
	} `json:"available"`
	Recommendations struct {
		BestValue *struct {
			PlayerKey             string   `json:"playerKey"`
			RecommendationReasons []string `json:"recommendationReasons"`
		} `json:"bestValue"`
	} `json:"recommendations"`
	Roster struct {
		Assignments []struct {
			PlayerKey string `json:"playerKey"`
		} `json:"assignments"`
	} `json:"roster"`
}

func TestMauriceRecommendationsRemainVisibleOutsideFilteredPage(t *testing.T) {
	server, _ := newDraftBoardTestServer(t)
	variables := map[string]any{"input": map[string]any{
		"league": draftfixtures.LeagueKey, "season": draftfixtures.Season,
		"search": "no-player-matches-this-filter", "limit": 1,
	}}
	response := postGraphQL(t, server, nil, graphDraftBoardQuery, variables)
	require.Empty(t, response.Errors)
	var data struct {
		MauriceDraftBoard graphDraftBoardView `json:"mauriceDraftBoard"`
	}
	require.NoError(t, jsonUnmarshal(response.Data, &data))
	assert.Empty(t, data.MauriceDraftBoard.Available)
	require.NotNil(t, data.MauriceDraftBoard.Recommendations.BestValue)
	assert.NotEmpty(t, data.MauriceDraftBoard.Recommendations.BestValue.RecommendationReasons)
}

func fetchGraphDraftBoard(t *testing.T, server *httptest.Server) graphDraftBoardView {
	t.Helper()
	variables := map[string]any{"input": map[string]any{
		"league": draftfixtures.LeagueKey, "season": draftfixtures.Season, "positions": []string{"C"}, "limit": 10,
	}}
	response := postGraphQL(t, server, nil, graphDraftBoardQuery, variables)
	require.Empty(t, response.Errors)
	var data struct {
		MauriceDraftBoard graphDraftBoardView `json:"mauriceDraftBoard"`
	}
	require.NoError(t, jsonUnmarshal(response.Data, &data))
	return data.MauriceDraftBoard
}

func centerPlayerKey(players []draftrank.Player) string {
	for _, player := range players {
		if slices.Contains(player.EligiblePositions, draft.PositionCenter) {
			return player.PlayerKey
		}
	}
	return ""
}

func newDraftBoardTestServer(t *testing.T) (*httptest.Server, *graphBoardData) {
	t.Helper()
	snapshot, err := draftfixtures.Snapshot(uuid.New(), draftfixtures.AsOf)
	require.NoError(t, err)
	now := draftfixtures.AsOf.Add(time.Hour)
	data := &graphBoardData{
		identity: draftwatch.Identity{LeagueKey: draftfixtures.LeagueKey, Season: draftfixtures.Season,
			LeagueID: draftfixtures.LeagueID, GameKey: 465},
		ranking: snapshot, session: draftwatch.Session{RecommendationsSafe: true, LastSuccessAt: &now,
			State: draftsession.State{Upstream: map[draftsession.PickKey]draftsession.Pick{},
				Manual: map[draftsession.PickKey]draftsession.ManualChange{}, UpstreamComplete: true}},
	}
	data.session.Identity = data.identity
	data.league = draftboard.LeagueData{TeamID: 3, TeamName: "My team", DraftFormat: "snake",
		PickClock: new(90), Rules: draft.Snapshot{Rules: draft.Rules{RosterSlots: snapshot.League.RosterSlots}},
		EstimatedOrder: []draftrecommend.PickSlot{
			{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 3},
			{Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 4},
			{Key: draftsession.PickKey{Round: 2, Pick: 3}, TeamID: 4},
			{Key: draftsession.PickKey{Round: 2, Pick: 4}, TeamID: 3},
		}}
	service := draftboard.NewService(data, graphBoardEngine{}, nil,
		draftboard.Options{Now: func() time.Time { return now }, StaleAfter: time.Hour})
	schema := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{DraftBoard: service}}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(schema)
	t.Cleanup(server.Close)
	return server, data
}

type graphBoardData struct {
	identity  draftwatch.Identity
	session   draftwatch.Session
	ranking   *draftrank.Snapshot
	league    draftboard.LeagueData
	shortlist []string
}

func (d *graphBoardData) ResolveIdentity(context.Context, string, int) (draftwatch.Identity, error) {
	return d.identity, nil
}
func (d *graphBoardData) Session(context.Context, draftwatch.Identity) (draftwatch.Session, error) {
	return d.session, nil
}
func (d *graphBoardData) Ranking(context.Context, draftwatch.Identity) (*draftrank.Snapshot, error) {
	return d.ranking, nil
}
func (d *graphBoardData) LeagueData(context.Context, draftwatch.Identity) (draftboard.LeagueData, error) {
	return d.league, nil
}
func (d *graphBoardData) ListShortlist(context.Context, string) ([]string, error) {
	return d.shortlist, nil
}
func (d *graphBoardData) SetShortlist(context.Context, string, string, uint64, bool) error {
	return nil
}
func (d *graphBoardData) ApplyManual(_ context.Context, _ draftwatch.Identity, operation draftsession.ManualOperation,
	expected uint64, _ time.Time) (draftwatch.Session, draftsession.Report, error) {
	if d.session.State.Version != expected {
		return draftwatch.Session{}, draftsession.Report{}, &draftwatch.StaleStateVersionError{Expected: expected, Actual: d.session.State.Version}
	}
	next, report, err := draftsession.ApplyManual(d.session.State, operation)
	if err != nil {
		return draftwatch.Session{}, draftsession.Report{}, err
	}
	d.session.State, d.session.SyncVersion = next, d.session.SyncVersion+1
	return d.session, report, nil
}
func (d *graphBoardData) ResolveConflict(context.Context, draftwatch.Identity, draftsession.PickKey,
	draftsession.ConflictChoice, uint64, time.Time) (draftwatch.Session, draftsession.Report, error) {
	return d.session, draftsession.Report{}, nil
}
func (d *graphBoardData) ListEvents(context.Context, string, uint64, int) ([]draftwatch.Event, error) {
	return nil, nil
}

type graphBoardEngine struct{}

func (graphBoardEngine) Recommend(_ context.Context, input draftrecommend.Input) (draftrecommend.StoredRun, error) {
	result, err := draftrecommend.Evaluate(input)
	return draftrecommend.StoredRun{ID: uuid.New(), Input: input, Result: result}, err
}

func jsonUnmarshal(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

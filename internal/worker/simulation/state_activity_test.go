package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// State + telemetry activities — tests for:
//
//   SetPoolStatusActivity        — workflow status writes
//   LoadDraftCandidatesActivity  — prior-season ranking
//   priorSeasonID                — pure helper
//   skaterDraftPosition / isGoalieByLookup — position filters
// ============================================================================

// poolStateQueriesFake is the pool lifecycle activities' query group:
// the season and status writes over the shared pool and agent fakes.
type poolStateQueriesFake struct {
	*poolFake
	*agentFake

	getSeasonArgs   []int32
	getSeasonReturn sqlcdb.Season
	getSeasonErr    error

	updatePoolStatusCalls []sqlcdb.UpdateSimPoolStatusParams
	updatePoolStatusErr   error
}

var _ poolStateQueries = (*poolStateQueriesFake)(nil)

func newPoolStateQueriesFake() *poolStateQueriesFake {
	return &poolStateQueriesFake{poolFake: &poolFake{}, agentFake: &agentFake{}}
}

func (s *poolStateQueriesFake) GetSeason(_ context.Context, id int32) (sqlcdb.Season, error) {
	s.getSeasonArgs = append(s.getSeasonArgs, id)
	if s.getSeasonErr != nil {
		return sqlcdb.Season{}, s.getSeasonErr
	}
	return s.getSeasonReturn, nil
}

func (s *poolStateQueriesFake) SetSimAgentDraftPosition(_ context.Context, _ sqlcdb.SetSimAgentDraftPositionParams) error {
	return nil
}

func (s *poolStateQueriesFake) UpdateSimPoolStatus(_ context.Context, arg sqlcdb.UpdateSimPoolStatusParams) error {
	s.updatePoolStatusCalls = append(s.updatePoolStatusCalls, arg)
	return s.updatePoolStatusErr
}

// PoolStateTestSuite drives the pool lifecycle activities; agents is the
// worker agent cache SetPoolStatus evicts from.
type PoolStateTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *poolStateQueriesFake
	agents  *Activities
	acts    poolStateActivity
}

func (s *PoolStateTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = newPoolStateQueriesFake()
	s.agents = &Activities{}
	s.acts = poolStateActivity{q: s.queries, agents: s.agents}
	s.env.RegisterActivity(s.acts.SetPoolStatus)
}

func TestPoolStateTestSuite(t *testing.T) {
	suite.Run(t, new(PoolStateTestSuite))
}

// draftCandidateQueriesFake is LoadDraftCandidates' query group: the
// prior season's club stats over the shared player fake.
type draftCandidateQueriesFake struct {
	*playerFake

	clubSkaterRows []sqlcdb.GetClubSkaterStatsBySeasonRow
	clubSkaterErr  error
	clubGoalieRows []sqlcdb.GetClubGoalieStatsBySeasonRow
	clubGoalieErr  error
}

var _ draftCandidateQueries = (*draftCandidateQueriesFake)(nil)

func (s *draftCandidateQueriesFake) GetClubSkaterStatsBySeason(_ context.Context, _ sqlcdb.GetClubSkaterStatsBySeasonParams) ([]sqlcdb.GetClubSkaterStatsBySeasonRow, error) {
	if s.clubSkaterErr != nil {
		return nil, s.clubSkaterErr
	}
	return s.clubSkaterRows, nil
}

func (s *draftCandidateQueriesFake) GetClubGoalieStatsBySeason(_ context.Context, _ sqlcdb.GetClubGoalieStatsBySeasonParams) ([]sqlcdb.GetClubGoalieStatsBySeasonRow, error) {
	if s.clubGoalieErr != nil {
		return nil, s.clubGoalieErr
	}
	return s.clubGoalieRows, nil
}

type DraftCandidatesTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *draftCandidateQueriesFake
	acts    draftCandidatesActivity
}

func (s *DraftCandidatesTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &draftCandidateQueriesFake{playerFake: &playerFake{}}
	s.acts = draftCandidatesActivity{q: s.queries}
	s.env.RegisterActivity(s.acts.LoadDraftCandidates)
}

func TestDraftCandidatesTestSuite(t *testing.T) {
	suite.Run(t, new(DraftCandidatesTestSuite))
}

// ----------------------------------------------------------------------------
// priorSeasonID — pure helper
// ----------------------------------------------------------------------------

func TestPriorSeasonID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       int32
		expected int32
		wantErr  bool
	}{
		{20242025, 20232024, false},
		{20002001, 19992000, false},
		{19181919, 19171918, false},
		{19171918, 19161917, true}, // below NHL minimum even after subtraction (bound check)
		{0, 0, true},
		{-1, 0, true},
	}
	for _, tc := range cases {
		got, err := priorSeasonID(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "season=%d", tc.in)
			continue
		}
		assert.NoError(t, err, "season=%d", tc.in)
		assert.Equal(t, tc.expected, got, "season=%d", tc.in)
	}
}

// ----------------------------------------------------------------------------
// SetPoolStatusActivity
// ----------------------------------------------------------------------------

func (s *PoolStateTestSuite) TestSetPoolStatus_HappyPath() {
	t := s.T()
	// Activity returns only error → don't call future.Get, just
	// check the immediate error from ExecuteActivity.
	_, err := s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{
		PoolID: 7, Status: PoolStatusComplete,
	})
	require.NoError(t, err)
	require.Len(t, s.queries.updatePoolStatusCalls, 1)
	assert.Equal(t, int32(7), s.queries.updatePoolStatusCalls[0].ID)
	assert.Equal(t, "complete", s.queries.updatePoolStatusCalls[0].Status)
}

func (s *PoolStateTestSuite) TestSetPoolStatus_EachStatusValueRoundtrips() {
	t := s.T()
	statuses := []PoolStatus{
		PoolStatusDraft, PoolStatusRunning, PoolStatusPaused,
		PoolStatusComplete, PoolStatusCancelled,
	}
	for _, st := range statuses {
		_, err := s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{PoolID: 1, Status: st})
		require.NoError(t, err)
	}
	require.Len(t, s.queries.updatePoolStatusCalls, len(statuses))
	for i, st := range statuses {
		assert.Equal(t, string(st), s.queries.updatePoolStatusCalls[i].Status)
	}
}

// A status that ends the pool drops its cached agents on this worker;
// a status the pool keeps running in leaves them for reuse.
func (s *PoolStateTestSuite) TestSetPoolStatus_EvictsAgentsOnlyWhenPoolEnds() {
	t := s.T()
	s.agents.AgentFactory = func(int32, AgentConfig, map[llm.Provider]llm.ProviderConfig, int) (*Agent, error) {
		return &Agent{}, nil
	}
	statuses := []PoolStatus{
		PoolStatusDraft, PoolStatusRunning, PoolStatusPaused,
		PoolStatusComplete, PoolStatusCancelled,
	}
	for _, st := range statuses {
		_, err := s.agents.getOrCreateAgent(1, 7, AgentConfig{Provider: "anthropic"}, 10)
		require.NoError(t, err)
		_, err = s.agents.getOrCreateAgent(2, 8, AgentConfig{Provider: "anthropic"}, 10)
		require.NoError(t, err)

		_, err = s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{PoolID: 1, Status: st})
		require.NoError(t, err)

		want := 2
		if st.ended() {
			want = 1
		}
		assert.Equal(t, want, s.agents.agents.len(), "status=%s", st)
	}
}

// A failed status write keeps the agents: the pool has not ended.
func (s *PoolStateTestSuite) TestSetPoolStatus_FailedWriteKeepsAgents() {
	t := s.T()
	s.agents.AgentFactory = func(int32, AgentConfig, map[llm.Provider]llm.ProviderConfig, int) (*Agent, error) {
		return &Agent{}, nil
	}
	_, err := s.agents.getOrCreateAgent(1, 7, AgentConfig{Provider: "anthropic"}, 10)
	require.NoError(t, err)
	s.queries.updatePoolStatusErr = errors.New("constraint violation")

	_, err = s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{PoolID: 1, Status: PoolStatusComplete})
	require.Error(t, err)
	assert.Equal(t, 1, s.agents.agents.len())
}

func (s *PoolStateTestSuite) TestSetPoolStatus_PropagatesError() {
	t := s.T()
	s.queries.updatePoolStatusErr = errors.New("constraint violation")
	_, err := s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{PoolID: 1, Status: PoolStatusComplete})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update pool status")
}

// ----------------------------------------------------------------------------
// LoadDraftCandidatesActivity — happy path + edge cases
// ----------------------------------------------------------------------------

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_AggregatesTradedSkater() {
	t := s.T()
	// Player 100 traded mid-season: two team rows. SUM should be (1+2)+(3+4)=10.
	s.queries.clubSkaterRows = []sqlcdb.GetClubSkaterStatsBySeasonRow{
		{PlayerID: 100, FirstName: "F", LastName: "L", TeamID: 1, Goals: 1, Assists: 3},
		{PlayerID: 100, FirstName: "F", LastName: "L", TeamID: 2, Goals: 2, Assists: 4},
		{PlayerID: 200, FirstName: "F", LastName: "L", TeamID: 1, Goals: 5, Assists: 0},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {ID: 200, Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
	}

	future, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.NoError(t, err)
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))

	require.Len(t, got.Skaters, 2, "two distinct players ranked")
	// McDavid: 3+7 = 10 G+A across both teams; Matthews: 5+0 = 5.
	// Ranked descending by Score (G+A), so McDavid is first.
	assert.Equal(t, int64(100), got.Skaters[0].PlayerID)
	assert.Equal(t, 3, got.Skaters[0].PriorG, "G summed across team rows")
	assert.Equal(t, 7, got.Skaters[0].PriorA, "A summed across team rows")
	assert.Equal(t, "C", got.Skaters[0].Position)
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_FiltersZeroScoreSkaters() {
	t := s.T()
	s.queries.clubSkaterRows = []sqlcdb.GetClubSkaterStatsBySeasonRow{
		{PlayerID: 100, FirstName: "F", LastName: "L", Goals: 5, Assists: 5},
		{PlayerID: 999, FirstName: "F", LastName: "L", Goals: 0, Assists: 0},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		999: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
	}
	_, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.NoError(t, err)
	// Get the result via a second execute to inspect — but since we
	// don't have a result-capture handle here, we'd need future.Get.
	// Restructure to verify call shape via the count of skater rows
	// passed into agg. In practice the easier check: the only way
	// to assert is to capture the future. Let me re-do the call.

	future, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.NoError(t, err)
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))
	require.Len(t, got.Skaters, 1, "zero-score players filtered out")
	assert.Equal(t, int64(100), got.Skaters[0].PlayerID)
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_ExcludesGoaliesFromSkaters() {
	t := s.T()
	// A goalie shouldn't appear in skater rankings even if their
	// goals/assists are nonzero (rare but possible — backup goalie
	// gets a fluke assist).
	s.queries.clubSkaterRows = []sqlcdb.GetClubSkaterStatsBySeasonRow{
		{PlayerID: 100, FirstName: "F", LastName: "L", Goals: 5, Assists: 5},
		{PlayerID: 200, FirstName: "F", LastName: "L", Goals: 0, Assists: 1},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
	}
	future, _ := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))
	require.Len(t, got.Skaters, 1)
	assert.Equal(t, int64(100), got.Skaters[0].PlayerID)
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_GoaliesFilteredAndRanked() {
	t := s.T()
	s.queries.clubGoalieRows = []sqlcdb.GetClubGoalieStatsBySeasonRow{
		{PlayerID: 1, FirstName: "F", LastName: "L", Wins: 30, GoalsAgainst: 95},
		{PlayerID: 2, FirstName: "F", LastName: "L", Wins: 35, GoalsAgainst: 90},
		{PlayerID: 3, FirstName: "F", LastName: "L", Wins: 0, GoalsAgainst: 5}, // filtered (zero W)
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		1: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
		2: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
		3: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
	}
	future, _ := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))
	require.Len(t, got.Goalies, 2, "zero-W goalie filtered")
	// RankGoalies sorts by W desc — Hellebuyck (35) ahead of Shesterkin (30).
	assert.Equal(t, int64(2), got.Goalies[0].PlayerID)
	assert.Equal(t, 35, got.Goalies[0].PriorW)
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_NullPositionDropped() {
	t := s.T()
	// A player with a NULL position can't be classified — drop both
	// from skater AND goalie lists.
	s.queries.clubSkaterRows = []sqlcdb.GetClubSkaterStatsBySeasonRow{
		{PlayerID: 100, FirstName: "F", LastName: "L", Goals: 5, Assists: 5},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {Position: sqlcdb.NullPlayerPosition{Valid: false}},
	}
	future, _ := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))
	assert.Empty(t, got.Skaters)
}

// TestLoadDraftCandidates_BatchesPlayerLookups guards the batching:
// position lookups for the (up to ~600) draft candidates must collapse
// to one GetPlayersByIDs call per candidate list (skaters, goalies),
// not one GetPlayer call per candidate. Also asserts zero-score/zero-W
// players are excluded from the batch entirely (filtered before the
// lookup, not after).
func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_BatchesPlayerLookups() {
	t := s.T()
	s.queries.clubSkaterRows = []sqlcdb.GetClubSkaterStatsBySeasonRow{
		{PlayerID: 100, FirstName: "F", LastName: "L", Goals: 5, Assists: 5},
		{PlayerID: 200, FirstName: "F", LastName: "L", Goals: 1, Assists: 0},
		{PlayerID: 999, FirstName: "F", LastName: "L", Goals: 0, Assists: 0}, // zero-score
	}
	s.queries.clubGoalieRows = []sqlcdb.GetClubGoalieStatsBySeasonRow{
		{PlayerID: 1, FirstName: "F", LastName: "L", Wins: 10, GoalsAgainst: 50},
		{PlayerID: 2, FirstName: "F", LastName: "L", Wins: 0, GoalsAgainst: 5}, // zero-W
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}},
		1:   {Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
	}

	future, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.NoError(t, err)
	var got LoadDraftCandidatesResult
	require.NoError(t, future.Get(&got))

	require.Len(t, s.queries.getPlayersByIDsCalls, 2,
		"one batch call for skater candidates, one for goalie candidates — not one per candidate")
	assert.ElementsMatch(t, []int64{100, 200}, s.queries.getPlayersByIDsCalls[0],
		"zero-score skater excluded from the batch (filtered before lookup)")
	assert.ElementsMatch(t, []int64{1}, s.queries.getPlayersByIDsCalls[1],
		"zero-W goalie excluded from the batch (filtered before lookup)")

	require.Len(t, got.Skaters, 2)
	require.Len(t, got.Goalies, 1)
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_ErrorOnPriorSeason() {
	t := s.T()
	// Season below the NHL minimum after the subtraction triggers
	// the priorSeasonID error path.
	_, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 19171918})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid prior NHL season")
}

func (s *DraftCandidatesTestSuite) TestLoadDraftCandidates_PropagatesSkaterDBError() {
	t := s.T()
	s.queries.clubSkaterErr = errors.New("query timeout")
	_, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query timeout")
}

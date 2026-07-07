package simulation

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// State + telemetry activities — tests for the production-gating
// follow-ups that turn the simulation runnable end-to-end:
//
//   SetPoolStatusActivity        — workflow status writes
//   LoadDraftCandidatesActivity  — prior-season ranking
//   priorSeasonID                — pure helper
//   skaterDraftPosition / isGoalieByLookup — position filters
// ============================================================================

type StateActivityTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	acts    *Activities
}

func (s *StateActivityTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
	s.acts = &Activities{Queries: s.queries}
	s.env.RegisterActivity(s.acts.SetPoolStatus)
	s.env.RegisterActivity(s.acts.LoadDraftCandidates)
}

func TestStateActivityTestSuite(t *testing.T) {
	suite.Run(t, new(StateActivityTestSuite))
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

func (s *StateActivityTestSuite) TestSetPoolStatus_HappyPath() {
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

func (s *StateActivityTestSuite) TestSetPoolStatus_EachStatusValueRoundtrips() {
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

func (s *StateActivityTestSuite) TestSetPoolStatus_PropagatesError() {
	t := s.T()
	s.queries.updatePoolStatusErr = errors.New("constraint violation")
	_, err := s.env.ExecuteActivity(s.acts.SetPoolStatus, SetPoolStatusInput{PoolID: 1, Status: PoolStatusComplete})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update pool status")
}

// ----------------------------------------------------------------------------
// LoadDraftCandidatesActivity — happy path + edge cases
// ----------------------------------------------------------------------------

func (s *StateActivityTestSuite) TestLoadDraftCandidates_AggregatesTradedSkater() {
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

func (s *StateActivityTestSuite) TestLoadDraftCandidates_FiltersZeroScoreSkaters() {
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

func (s *StateActivityTestSuite) TestLoadDraftCandidates_ExcludesGoaliesFromSkaters() {
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

func (s *StateActivityTestSuite) TestLoadDraftCandidates_GoaliesFilteredAndRanked() {
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

func (s *StateActivityTestSuite) TestLoadDraftCandidates_NullPositionDropped() {
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

func (s *StateActivityTestSuite) TestLoadDraftCandidates_ErrorOnPriorSeason() {
	t := s.T()
	// Season below the NHL minimum after the subtraction triggers
	// the priorSeasonID error path.
	_, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 19171918})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid prior NHL season")
}

func (s *StateActivityTestSuite) TestLoadDraftCandidates_PropagatesSkaterDBError() {
	t := s.T()
	s.queries.clubSkaterErr = errors.New("query timeout")
	_, err := s.env.ExecuteActivity(s.acts.LoadDraftCandidates, LoadDraftCandidatesInput{Season: 20242025})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query timeout")
}

// ----------------------------------------------------------------------------
// BuildManageRosterContextActivity
// ----------------------------------------------------------------------------

func (s *StateActivityTestSuite) registerContextActivity() {
	s.env.RegisterActivity(s.acts.BuildManageRosterContext)
}

func (s *StateActivityTestSuite) TestBuildManageRosterContext_PopulatesNotesAndRoster() {
	t := s.T()
	s.registerContextActivity()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{
		1: {ID: 1, Notes: "Watch the schedule"},
	}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {
			{PoolID: 1, AgentID: 1, PlayerID: 100, Slot: "C"},
			{PoolID: 1, AgentID: 1, PlayerID: 200, Slot: "BN"},
		},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, FirstName: "Connor", LastName: "McDavid", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {ID: 200, FirstName: "Leon", LastName: "Draisaitl", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
	}

	in := BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate:    pgDate(t, "2024-11-15"),
		PoolConfig: PoolConfig{RosterPositions: map[RosterSlot]int{SlotC: 2, SlotBN: 5}},
		FreeAgents: []int64{300, 400},
	}
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	assert.Equal(t, "Watch the schedule", got.DailyPrompt.YourNotes)
	require.Len(t, got.DailyPrompt.YourRoster, 2)
	assert.Equal(t, "Connor McDavid", got.DailyPrompt.YourRoster[0].Player)
	assert.Equal(t, RosterSlot("C"), got.DailyPrompt.YourRoster[0].Slot)
	assert.Equal(t, RosterSlot("C"), got.Roster.Placements[100])
	assert.Equal(t, RosterSlot("BN"), got.Roster.Placements[200])
	assert.Equal(t, sqlcdb.PlayerPositionC, got.Positions[100])
	assert.Equal(t, []int64{300, 400}, got.FreeAgents)
}

// PendingClaims for the agent flow into the context so the daily turn's
// ValidateClaimPlayer can reject a duplicate pending claim cleanly.
func (s *StateActivityTestSuite) TestBuildManageRosterContext_PopulatesPendingClaims() {
	t := s.T()
	s.registerContextActivity()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1}}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}}
	s.queries.listPendingByAgentRows = map[int32][]sqlcdb.SimWaiverClaim{
		1: {claim(50, 1, 8478550, 0), claim(51, 1, 8480039, 0)},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate: pgDate(t, "2024-11-15"),
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	assert.ElementsMatch(t, []int64{8478550, 8480039}, got.PendingClaims)
	require.Len(t, s.queries.listPendingByAgentArgs, 1)
	assert.Equal(t, int32(1), s.queries.listPendingByAgentArgs[0].AgentID)
}

func (s *StateActivityTestSuite) TestBuildManageRosterContext_EmptyStandingsBeforeFirstGame() {
	t := s.T()
	s.registerContextActivity()
	// No latest standings date yet — pre-first-game state.
	s.queries.getSimStandingsLatestDateReturn = pgtype.Date{Valid: false}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}}
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1}}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))
	assert.Empty(t, got.DailyPrompt.Standings, "no standings yet → empty slice, not error")
}

func (s *StateActivityTestSuite) TestBuildManageRosterContext_StandingsTagIsYou() {
	t := s.T()
	s.registerContextActivity()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1, TeamName: "Alpha"}, 2: {ID: 2, TeamName: "Bravo"}}
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Alpha"}, {ID: 2, TeamName: "Bravo"},
	}
	s.queries.getSimStandingsLatestDateReturn = pgtype.Date{Valid: true}
	s.queries.listSimStandingsByDateRows = []sqlcdb.SimStanding{
		{AgentID: 1, Category: "G", RotoPoints: numericFromIntForTest(t, 5)},
		{AgentID: 2, Category: "G", RotoPoints: numericFromIntForTest(t, 3)},
	}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}, 2: {}}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.Standings, 2)
	byAgent := map[string]bool{}
	for _, r := range got.DailyPrompt.Standings {
		byAgent[r.Agent] = r.IsYou
	}
	assert.True(t, byAgent["Alpha"], "Alpha is the requested agent → is_you=true")
	assert.False(t, byAgent["Bravo"], "Bravo is not requested → is_you=false")
}

// numericFromIntForTest is a tiny helper for test fixtures —
// converts an int to pgtype.Numeric without dragging strconv into
// the test imports section.
func numericFromIntForTest(t require.TestingT, v int) pgtype.Numeric {
	n, err := numericFromFloat(float64(v))
	require.NoError(t, err)
	return n
}

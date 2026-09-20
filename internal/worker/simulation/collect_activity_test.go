package simulation

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// CollectDayStatsTestSuite — covers no-games skip, single-skater
// happy path, single-goalie + decision semantics, multi-agent batch,
// recompute ordering, idempotent rerun, and DB error propagation.
// ============================================================================

type CollectDayStatsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	tx      *stubTransactor
	acts    *Activities
}

func (s *CollectDayStatsTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{
		gameSkaterRows:       map[int64][]sqlcdb.GetGameSkaterStatsByGameRow{},
		gameGoalieRows:       map[int64][]sqlcdb.GetGameGoalieStatsByGameRow{},
		listActiveRosterRows: map[int32][]sqlcdb.SimRoster{},
	}
	s.tx = &stubTransactor{queries: s.queries}
	s.acts = &Activities{Queries: s.queries, Tx: s.tx}
	s.env.RegisterActivity(s.acts.CollectDayStats)
}

func TestCollectDayStatsTestSuite(t *testing.T) {
	suite.Run(t, new(CollectDayStatsTestSuite))
}

// validInput returns a payload pinning a known date and pool. Tests
// override fields they care about and seed the stub maps.
func (s *CollectDayStatsTestSuite) validInput() CollectDayStatsInput {
	return CollectDayStatsInput{
		PoolID:   1,
		Season:   20242025,
		SimDate:  pgDate(s.T(), "2024-11-15"),
		AgentIDs: []int32{1, 2},
	}
}

// ----------------------------------------------------------------------------
// No-games skip
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestNoGames_Skips() {
	t := s.T()
	// listDayGamesReturn defaults to nil → empty slice → skip.

	future, err := s.env.ExecuteActivity(s.acts.CollectDayStats, s.validInput())
	require.NoError(t, err)
	var got CollectDayStatsResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonNoGames, got.SkipReason)

	assert.Empty(t, s.queries.upsertDailyPlayerCalls, "no upserts when skipped")
	assert.Empty(t, s.queries.aggregateDailyStatsCalls)
	assert.Empty(t, s.queries.recomputeCountingCalls)
	assert.Empty(t, s.queries.recomputeGAACalls)
	assert.Zero(t, s.tx.inTxCalled, "no commit when skipped")
}

// pin: the day's games are looked up within the input season, so
// another season's games on the same date can't be scored.
func (s *CollectDayStatsTestSuite) TestListDayGames_ScopedToSeason() {
	t := s.T()
	in := s.validInput()

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	assert.Equal(t, []sqlcdb.ListSimDayGamesParams{
		{Season: in.Season, GameDate: in.SimDate},
	}, s.queries.listDayGamesArgs)
}

// ----------------------------------------------------------------------------
// Single-skater happy path
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestSingleSkater_EmitsAllSixCategories() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	s.queries.gameSkaterRows[2024020001] = []sqlcdb.GetGameSkaterStatsByGameRow{
		{
			GameID:          2024020001,
			PlayerID:        8478402, // McDavid
			Goals:           2,
			Assists:         1,
			PlusMinus:       3,
			PenaltyMinutes:  2,
			PowerPlayPoints: 1,
			ShotsOnGoal:     6,
		},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8478402, Slot: string(SlotC)},
	}

	future, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	var got CollectDayStatsResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped)
	assert.Equal(t, 6, got.PlayerRowsWritten, "six skater categories")

	require.Len(t, s.queries.upsertDailyPlayerCalls, 6)
	byCat := map[string]float64{}
	for _, c := range s.queries.upsertDailyPlayerCalls {
		v, err := NumericToFloat(c.Value)
		require.NoError(t, err)
		byCat[c.Category] = v
	}
	assert.Equal(t, 2.0, byCat[string(CategoryG)])
	assert.Equal(t, 1.0, byCat[string(CategoryA)])
	assert.Equal(t, 3.0, byCat[string(CategoryPM)])
	assert.Equal(t, 2.0, byCat[string(CategoryPIM)])
	assert.Equal(t, 1.0, byCat[string(CategoryPPP)])
	assert.Equal(t, 6.0, byCat[string(CategorySOG)])

	// Aggregate fired once per agent.
	require.Len(t, s.queries.aggregateDailyStatsCalls, 1)
	assert.Equal(t, int32(1), s.queries.aggregateDailyStatsCalls[0].AgentID)

	// Recomputes fired once each, AFTER the per-agent writes (one tx).
	assert.Equal(t, []int32{1}, s.queries.recomputeCountingCalls)
	assert.Equal(t, []int32{1}, s.queries.recomputeGAACalls)
}

// ----------------------------------------------------------------------------
// Goalie + decision semantics
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestGoalie_DecisionSemantics_W() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	// Goalie won, allowed 2 GA over 3600 sec TOI.
	s.queries.gameGoalieRows[2024020001] = []sqlcdb.GetGameGoalieStatsByGameRow{
		{
			GameID:       2024020001,
			PlayerID:     8480039,
			Decision:     sqlcdb.NullGoalieDecision{GoalieDecision: sqlcdb.GoalieDecisionW, Valid: true},
			GoalsAgainst: 2,
			TOISeconds:   3600,
		},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8480039, Slot: string(SlotG)},
	}

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)

	require.Len(t, s.queries.upsertDailyPlayerCalls, 3)
	byCat := map[string]sqlcdb.UpsertSimAgentDailyPlayerStatParams{}
	for _, c := range s.queries.upsertDailyPlayerCalls {
		byCat[c.Category] = c
	}
	wValue, _ := NumericToFloat(byCat[string(CategoryW)].Value)
	assert.Equal(t, 1.0, wValue, "decision=W contributes 1 to W")
	gaValue, _ := NumericToFloat(byCat[string(CategoryGA)].Value)
	assert.Equal(t, 2.0, gaValue, "GA carries the goals_against count")

	gaa := byCat[string(CategoryGAA)]
	gaaValue, _ := NumericToFloat(gaa.Value)
	assert.Equal(t, 0.0, gaaValue, "GAA per-player value is 0 — components carry the truth")
	assert.True(t, gaa.GoalieGA.Valid)
	assert.Equal(t, int32(2), gaa.GoalieGA.Int32)
	assert.True(t, gaa.GoalieTOISeconds.Valid)
	assert.Equal(t, int32(3600), gaa.GoalieTOISeconds.Int32)
}

// L / OTL / T / NULL decisions all contribute 0 to W (PLAN.md
// "Goalie decision semantics"). GA still accumulates from the
// pulled starter's goals_against — the row's NULL decision means
// "didn't get the W", not "didn't allow goals".
func (s *CollectDayStatsTestSuite) TestGoalie_NullDecisionGivesZeroW_ButGAStillAccumulates() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	// Pulled starter: NULL decision, allowed 3 GA over 1200 seconds.
	s.queries.gameGoalieRows[2024020001] = []sqlcdb.GetGameGoalieStatsByGameRow{
		{
			GameID:       2024020001,
			PlayerID:     8480039,
			Decision:     sqlcdb.NullGoalieDecision{Valid: false},
			GoalsAgainst: 3,
			TOISeconds:   1200,
		},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8480039, Slot: string(SlotG)},
	}

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)

	byCat := map[string]sqlcdb.UpsertSimAgentDailyPlayerStatParams{}
	for _, c := range s.queries.upsertDailyPlayerCalls {
		byCat[c.Category] = c
	}
	wValue, _ := NumericToFloat(byCat[string(CategoryW)].Value)
	assert.Equal(t, 0.0, wValue, "NULL decision → 0 W")
	gaValue, _ := NumericToFloat(byCat[string(CategoryGA)].Value)
	assert.Equal(t, 3.0, gaValue, "GA still accumulates from NULL-decision rows")
}

// "L" / "OTL" / "T" all contribute 0 to W — pin all three explicitly.
func (s *CollectDayStatsTestSuite) TestGoalie_NonWinDecisionsContributeZeroW() {
	t := s.T()
	cases := []sqlcdb.GoalieDecision{sqlcdb.GoalieDecisionL, sqlcdb.GoalieDecisionOTL, sqlcdb.GoalieDecisionT}
	for _, dec := range cases {
		s.SetupTest() // reset stubs for each sub-case
		in := s.validInput()
		in.AgentIDs = []int32{1}
		s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
			{ID: 2024020001, GameDate: in.SimDate},
		}
		s.queries.gameGoalieRows[2024020001] = []sqlcdb.GetGameGoalieStatsByGameRow{
			{
				PlayerID: 8480039,
				Decision: sqlcdb.NullGoalieDecision{GoalieDecision: dec, Valid: true},
			},
		}
		s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
			{PoolID: 1, AgentID: 1, PlayerID: 8480039, Slot: string(SlotG)},
		}

		_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
		require.NoError(t, err)

		byCat := map[string]sqlcdb.UpsertSimAgentDailyPlayerStatParams{}
		for _, c := range s.queries.upsertDailyPlayerCalls {
			byCat[c.Category] = c
		}
		wValue, _ := NumericToFloat(byCat[string(CategoryW)].Value)
		assert.Equal(t, 0.0, wValue, "decision=%s → 0 W", dec)
	}
}

// ----------------------------------------------------------------------------
// Players not on roster shouldn't generate rows.
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestPlayerNotOnRoster_NoRowEmitted() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	// Stat row exists for player 8478402 but no agent rosters them.
	s.queries.gameSkaterRows[2024020001] = []sqlcdb.GetGameSkaterStatsByGameRow{
		{GameID: 2024020001, PlayerID: 8478402, Goals: 1},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{} // empty

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	assert.Empty(t, s.queries.upsertDailyPlayerCalls)
	// Aggregate skipped when no rows were written for this agent.
	assert.Empty(t, s.queries.aggregateDailyStatsCalls)
	// But pool-wide recomputes still fire (they SUM existing data; an
	// empty new day is a valid no-op).
	require.Len(t, s.queries.recomputeCountingCalls, 1)
	require.Len(t, s.queries.recomputeGAACalls, 1)
}

// ----------------------------------------------------------------------------
// Multi-agent batch
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestMultiAgent_EachGetsOwnRowsAndAggregate() {
	t := s.T()
	in := s.validInput() // AgentIDs = [1, 2]
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	s.queries.gameSkaterRows[2024020001] = []sqlcdb.GetGameSkaterStatsByGameRow{
		{PlayerID: 8478402, Goals: 1},
		{PlayerID: 8480039, Goals: 2},
	}
	// Agent 1 rosters McDavid, agent 2 rosters MacKinnon.
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8478402, Slot: string(SlotC)},
	}
	s.queries.listActiveRosterRows[2] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 2, PlayerID: 8480039, Slot: string(SlotC)},
	}

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)

	// 12 rows: 6 skater categories × 2 agents.
	assert.Len(t, s.queries.upsertDailyPlayerCalls, 12)

	// Each agent got one Aggregate call.
	require.Len(t, s.queries.aggregateDailyStatsCalls, 2)
	agentIDs := []int32{
		s.queries.aggregateDailyStatsCalls[0].AgentID,
		s.queries.aggregateDailyStatsCalls[1].AgentID,
	}
	assert.ElementsMatch(t, []int32{1, 2}, agentIDs)

	// Recomputes fire EXACTLY once at the end, not per-agent.
	require.Len(t, s.queries.recomputeCountingCalls, 1)
	require.Len(t, s.queries.recomputeGAACalls, 1)
}

// ----------------------------------------------------------------------------
// Idempotency: rerunning with same inputs makes the same upsert calls.
// (The DB-level ON CONFLICT DO UPDATE is what makes this safe in
// practice; at the activity level we just pin "deterministic call set".)
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestIdempotency_SameInputProducesSameCalls() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 2024020001, GameDate: in.SimDate},
	}
	s.queries.gameSkaterRows[2024020001] = []sqlcdb.GetGameSkaterStatsByGameRow{
		{PlayerID: 8478402, Goals: 1, Assists: 2, ShotsOnGoal: 4},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8478402, Slot: string(SlotC)},
	}

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	firstRunCalls := append([]sqlcdb.UpsertSimAgentDailyPlayerStatParams(nil), s.queries.upsertDailyPlayerCalls...)

	// Reset only the call recorders, keep the scripted return data.
	s.queries.upsertDailyPlayerCalls = nil
	s.queries.aggregateDailyStatsCalls = nil
	s.queries.recomputeCountingCalls = nil
	s.queries.recomputeGAACalls = nil

	_, err = s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	secondRunCalls := s.queries.upsertDailyPlayerCalls

	assert.Equal(t, len(firstRunCalls), len(secondRunCalls),
		"deterministic call count across reruns")
	for i := range firstRunCalls {
		assert.Equal(t, firstRunCalls[i].PlayerID, secondRunCalls[i].PlayerID)
		assert.Equal(t, firstRunCalls[i].Category, secondRunCalls[i].Category)
	}
}

// ----------------------------------------------------------------------------
// Error propagation
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestListDayGamesError_Aborts() {
	t := s.T()
	s.queries.listDayGamesErr = errors.New("connection lost")

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list day games")
}

func (s *CollectDayStatsTestSuite) TestUpsertError_Aborts() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{{ID: 1, GameDate: in.SimDate}}
	s.queries.gameSkaterRows[1] = []sqlcdb.GetGameSkaterStatsByGameRow{
		{PlayerID: 8478402, Goals: 1},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 1, PlayerID: 8478402, Slot: string(SlotC)},
	}
	s.queries.upsertDailyPlayerErr = errors.New("constraint violation")

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "constraint violation")
}

func (s *CollectDayStatsTestSuite) TestRecomputeError_Aborts() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{{ID: 1, GameDate: in.SimDate}}
	s.queries.recomputeCountingErr = errors.New("statement timeout")

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recompute totals")
}

// ----------------------------------------------------------------------------
// Activity does NOT mutate input (no AgentIDs slice rewrite, no
// SimDate adjustment). Pin so a future refactor that "reuses" the
// input slice can't sneak through.
// ----------------------------------------------------------------------------

func (s *CollectDayStatsTestSuite) TestInputNotMutated() {
	t := s.T()
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 1, GameDate: pgDate(t, "2024-11-15")},
	}
	in := s.validInput()
	originalIDs := append([]int32(nil), in.AgentIDs...)
	originalDate := in.SimDate

	_, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	assert.Equal(t, originalIDs, in.AgentIDs)
	assert.Equal(t, originalDate, in.SimDate)
}

// pin: GamesScored result reflects the day-games count.
func (s *CollectDayStatsTestSuite) TestResult_GamesScoredCount() {
	t := s.T()
	in := s.validInput()
	in.AgentIDs = []int32{1}
	s.queries.listDayGamesReturn = []sqlcdb.ListSimDayGamesRow{
		{ID: 1}, {ID: 2}, {ID: 3},
	}
	s.queries.listActiveRosterRows[1] = []sqlcdb.SimRoster{}

	future, err := s.env.ExecuteActivity(s.acts.CollectDayStats, in)
	require.NoError(t, err)
	var got CollectDayStatsResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 3, got.GamesScored)
	assert.Equal(t, 1, got.AgentsProcessed)
}

// silence unused-import linter when pgtype is referenced only via
// the stub fields.
var _ = pgtype.Date{}

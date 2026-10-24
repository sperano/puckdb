package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// standingsQueriesFake is UpdateStandings' query group.
type standingsQueriesFake struct {
	// ListSimAgentTotalsByPool
	listTotalsArg    int32
	listTotalsRows   []sqlcdb.SimAgentTotal
	listTotalsErr    error
	listTotalsCalled int

	// UpsertSimStanding
	upsertStandingsCalls []sqlcdb.UpsertSimStandingParams
	upsertStandingErr    error
}

var _ standingsQueries = (*standingsQueriesFake)(nil)

func (s *standingsQueriesFake) ListSimAgentTotalsByPool(_ context.Context, poolID int32) ([]sqlcdb.SimAgentTotal, error) {
	s.listTotalsCalled++
	s.listTotalsArg = poolID
	if s.listTotalsErr != nil {
		return nil, s.listTotalsErr
	}
	return s.listTotalsRows, nil
}

func (s *standingsQueriesFake) UpsertSimStanding(_ context.Context, arg sqlcdb.UpsertSimStandingParams) error {
	s.upsertStandingsCalls = append(s.upsertStandingsCalls, arg)
	return s.upsertStandingErr
}

// UpdateStandingsTestSuite uses Temporal's TestActivityEnvironment so
// activity.GetLogger(ctx) works (it panics outside an activity ctx).
type UpdateStandingsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *standingsQueriesFake
	acts    standingsActivity
}

func (s *UpdateStandingsTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &standingsQueriesFake{}
	s.acts = standingsActivity{q: s.queries}
	s.env.RegisterActivity(s.acts.UpdateStandings)
}

func TestUpdateStandingsTestSuite(t *testing.T) {
	suite.Run(t, new(UpdateStandingsTestSuite))
}

// ============================================================================
// UpdateStandings
// ============================================================================

// numericFromInt is a tiny test helper for building pgtype.Numeric
// row values without dragging strconv into every test case.
func numericFromInt(t require.TestingT, v int) pgtype.Numeric {
	n, err := pgconv.NumericFromFloat(float64(v))
	require.NoError(t, err)
	return n
}

// totalsRow is a test-only helper for SimAgentTotal construction.
// goalieGA / goalieTOI are 0 unless the row is for the GAA category.
func totalsRow(t require.TestingT, agentID int32, cat string, value int) sqlcdb.SimAgentTotal {
	return sqlcdb.SimAgentTotal{
		PoolID:   1,
		AgentID:  agentID,
		Category: cat,
		Value:    numericFromInt(t, value),
	}
}

func (s *UpdateStandingsTestSuite) TestUpdateStandings_RanksAndUpsertsAllCategories() {
	// 3 agents, single counting category G. Values 50, 30, 10 →
	// ranks 1, 2, 3 → roto points 3, 2, 1 in a 3-team pool.
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 50),
		totalsRow(t, 2, string(CategoryG), 30),
		totalsRow(t, 3, string(CategoryG), 10),
	}

	in := UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")}
	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)

	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 3, got.StandingsWritten)
	require.Equal(t, 1, s.queries.listTotalsCalled)
	assert.Equal(t, int32(1), s.queries.listTotalsArg)
	require.Len(t, s.queries.upsertStandingsCalls, 3)

	// Verify each upsert carried the right (agent, category, roto_points).
	// Build a lookup keyed by agent_id since map iteration in
	// UpdateStandings can write rows in any order.
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		assert.Equal(t, int32(1), c.PoolID)
		assert.Equal(t, "G", c.Category)
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(1))
	require.Contains(t, byAgent, int32(2))
	require.Contains(t, byAgent, int32(3))

	// Spot-check the top scorer's roto_points (should be 3.0 in a 3-team pool).
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	assert.InDelta(t, 3.0, rp1.Float64, 1e-6, "rank 1 in a 3-team pool earns 3 roto points")

	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	assert.InDelta(t, 1.0, rp3.Float64, 1e-6, "rank 3 (last) earns 1 roto point")
}

// Empty totals — day 1 of the sim, before any games — must be a
// no-op success rather than an error. Pin: no upserts called.
func (s *UpdateStandingsTestSuite) TestUpdateStandings_EmptyTotalsIsNoOp() {
	s.queries.listTotalsRows = nil

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1})
	require.NoError(s.T(), err)
	var got UpdateStandingsResult
	require.NoError(s.T(), future.Get(&got))
	assert.Equal(s.T(), 0, got.StandingsWritten)
	assert.Empty(s.T(), s.queries.upsertStandingsCalls)
}

func (s *UpdateStandingsTestSuite) TestUpdateStandings_PropagatesListError() {
	s.queries.listTotalsErr = errors.New("query timeout")

	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1})
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "list agent totals")
	assert.Contains(s.T(), err.Error(), "query timeout")
	assert.Empty(s.T(), s.queries.upsertStandingsCalls, "no upserts must fire when list fails")
}

func (s *UpdateStandingsTestSuite) TestUpdateStandings_PropagatesUpsertError() {
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 10),
	}
	s.queries.upsertStandingErr = errors.New("constraint violation")

	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert standing")
	assert.Contains(t, err.Error(), "constraint violation")
}

// GAA category needs the goalie components (GA, TOI) carried through
// the conversion from sqlc rows to scoring engine input. Pin that
// path so a future "drop the int4 fields" change breaks loudly.
func (s *UpdateStandingsTestSuite) TestUpdateStandings_GAARoutesThroughGoalieComponents() {
	t := s.T()
	// Two agents tied on raw GAA value but with different goalie
	// components. Agent 1: 60 GA / 18000 TOI = 12 GAA. Agent 2: zero TOI.
	// Per PLAN.md, zero-TOI agent gets the worst rank in GAA.
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		{
			PoolID: 1, AgentID: 1, Category: string(CategoryGAA),
			Value:            numericFromInt(t, 0), // value column unused for GAA
			GoalieGA:         pgtype.Int4{Int32: 60, Valid: true},
			GoalieTOISeconds: pgtype.Int4{Int32: 18000, Valid: true},
		},
		{
			PoolID: 1, AgentID: 2, Category: string(CategoryGAA),
			Value:            numericFromInt(t, 0),
			GoalieGA:         pgtype.Int4{Int32: 0, Valid: true},
			GoalieTOISeconds: pgtype.Int4{Int32: 0, Valid: true}, // zero TOI
		},
	}

	in := UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")}
	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	require.Len(t, s.queries.upsertStandingsCalls, 2)

	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	rp2, _ := byAgent[2].RotoPoints.Float64Value()
	assert.Greater(t, rp1.Float64, rp2.Float64, "non-zero-TOI agent must outrank zero-TOI agent in GAA")
}

// ============================================================================
// Low-1: AgentIDs zero-pads agents absent from sim_agent_totals so the
// ranking pool size always equals the full roster, not just the agents
// that have stats so far.
// ============================================================================

// Agent 3 has no totals row yet (e.g., all their players had days off).
// With AgentIDs=[1,2,3], agent 3 must be padded with a zero-valued
// row so that the pool size stays 3 and agents 1/2 each earn roto
// points from a three-agent ranking, not a two-agent one.
//
// Higher-is-better (Goals): values 50, 30 → ranks 1, 2 (of 3) → roto
// points 3.0, 2.0 for agents 1 and 2; zero-pad agent 3 earns 1.0.
func (s *UpdateStandingsTestSuite) TestUpdateStandings_AgentIDsZeroPadsAbsentAgents_HigherIsBetter() {
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 50),
		totalsRow(t, 2, string(CategoryG), 30),
		// agent 3 has no stats row
	}
	in := UpdateStandingsInput{
		PoolID:   1,
		SimDate:  pgDate(t, "2025-11-15"),
		AgentIDs: []int32{1, 2, 3},
	}

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))

	// All three agents get a standings row.
	require.Len(t, s.queries.upsertStandingsCalls, 3)
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(3), "absent agent must still receive a standings upsert")

	// Points awarded from a 3-team pool, not a 2-team pool.
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	rp2, _ := byAgent[2].RotoPoints.Float64Value()
	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	assert.InDelta(t, 3.0, rp1.Float64, 1e-6, "rank 1 of 3 earns 3 roto points")
	assert.InDelta(t, 2.0, rp2.Float64, 1e-6, "rank 2 of 3 earns 2 roto points")
	assert.InDelta(t, 1.0, rp3.Float64, 1e-6, "zero-padded rank 3 of 3 earns 1 roto point")
}

// Lower-is-better (GA): a zero-stat agent has zero goals-against,
// which is the best possible value, so they rank first (or tied first)
// — not last. Verify that the padded zero row earns the top roto points.
func (s *UpdateStandingsTestSuite) TestUpdateStandings_AgentIDsZeroPadsAbsentAgents_LowerIsBetter() {
	t := s.T()
	// CategoryGA is lower-is-better (fewer goals against = better).
	// Agents 1 and 2 have GA values; agent 3 has no totals row yet.
	// After padding, agent 3's zero GA ranks best and earns the most points.
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryGA), 30),
		totalsRow(t, 2, string(CategoryGA), 20),
		// agent 3 absent → zero GA = best
	}
	in := UpdateStandingsInput{
		PoolID:   1,
		SimDate:  pgDate(t, "2025-11-15"),
		AgentIDs: []int32{1, 2, 3},
	}

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))

	require.Len(t, s.queries.upsertStandingsCalls, 3)
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(3), "absent agent must receive a standings upsert")

	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	assert.Greater(t, rp3.Float64, rp1.Float64,
		"zero-GA agent (lower-is-better) must outrank the agent with the highest GA value")
}

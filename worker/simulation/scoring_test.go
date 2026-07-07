package simulation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pointsConservationDelta is the floating-point tolerance for the
// sum(points) == N(N+1)/2 invariant. The Yahoo averaging formula always
// produces exact half-integers from integer positions, so this delta
// only guards against future-test sloppiness, not real loss-of-precision.
const pointsConservationDelta = 1e-9

// assertConservation checks the Yahoo invariant: regardless of how many
// ties or where, sum(points) across all agents must equal N*(N+1)/2.
func assertConservation(t *testing.T, n int, rankings []CategoryRanking) {
	t.Helper()
	var sum float64
	for _, r := range rankings {
		sum += r.RotoPoints
	}
	expected := float64(n*(n+1)) / 2.0
	assert.InDelta(t, expected, sum, pointsConservationDelta,
		"sum of roto points must equal N(N+1)/2")
}

// pointsByID returns a map from agentID → RotoPoints for ergonomic
// out-of-order lookup in assertions.
func pointsByID(rankings []CategoryRanking) map[int64]CategoryRanking {
	m := make(map[int64]CategoryRanking, len(rankings))
	for _, r := range rankings {
		m[r.AgentID] = r
	}
	return m
}

// PLAN.md worked example #1: G values 50, 45, 30, 30, 20.
// Tied pair at sorted positions 3,4 → each gets (3+2)/2 = 2.5.
// Final points: 5, 4, 2.5, 2.5, 1.
func TestRankCategory_TwoWayTieAtThird_HigherIsBetter(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, Value: 50},
		{AgentID: 2, Value: 45},
		{AgentID: 3, Value: 30},
		{AgentID: 4, Value: 30},
		{AgentID: 5, Value: 20},
	}
	got := RankCategory(stats, false)
	require.Len(t, got, 5)

	by := pointsByID(got)
	assert.Equal(t, 5.0, by[1].RotoPoints)
	assert.Equal(t, 4.0, by[2].RotoPoints)
	assert.Equal(t, 2.5, by[3].RotoPoints)
	assert.Equal(t, 2.5, by[4].RotoPoints)
	assert.Equal(t, 1.0, by[5].RotoPoints)

	// Tied agents share the lowest position in their tie group.
	assert.Equal(t, 1, by[1].Rank)
	assert.Equal(t, 2, by[2].Rank)
	assert.Equal(t, 3, by[3].Rank)
	assert.Equal(t, 3, by[4].Rank)
	assert.Equal(t, 5, by[5].Rank)

	assertConservation(t, 5, got)
}

// PLAN.md worked example #2: PPP values 25, 18, 18, 18, 10.
// Tied trio at sorted positions 2,3,4 → each gets (4+3+2)/3 = 3.
// Final points: 5, 3, 3, 3, 1.
func TestRankCategory_ThreeWayTieAtSecond_HigherIsBetter(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, Value: 25},
		{AgentID: 2, Value: 18},
		{AgentID: 3, Value: 18},
		{AgentID: 4, Value: 18},
		{AgentID: 5, Value: 10},
	}
	got := RankCategory(stats, false)
	require.Len(t, got, 5)

	by := pointsByID(got)
	assert.Equal(t, 5.0, by[1].RotoPoints)
	assert.Equal(t, 3.0, by[2].RotoPoints)
	assert.Equal(t, 3.0, by[3].RotoPoints)
	assert.Equal(t, 3.0, by[4].RotoPoints)
	assert.Equal(t, 1.0, by[5].RotoPoints)

	assert.Equal(t, 1, by[1].Rank)
	assert.Equal(t, 2, by[2].Rank)
	assert.Equal(t, 2, by[3].Rank)
	assert.Equal(t, 2, by[4].Rank)
	assert.Equal(t, 5, by[5].Rank)

	assertConservation(t, 5, got)
}

// PLAN.md worked example #3: all five tied — day-1 zero-state.
// All share position 1, K=5 → each gets (5+4+3+2+1)/5 = 3.
// This is the steady state until the first game day produces non-zero values.
func TestRankCategory_AllFiveTied_DayOneSteadyState(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, Value: 0},
		{AgentID: 2, Value: 0},
		{AgentID: 3, Value: 0},
		{AgentID: 4, Value: 0},
		{AgentID: 5, Value: 0},
	}
	got := RankCategory(stats, false)
	require.Len(t, got, 5)

	for _, r := range got {
		assert.Equal(t, 3.0, r.RotoPoints, "agent %d", r.AgentID)
		assert.Equal(t, 1, r.Rank, "agent %d shares lowest position in tie group", r.AgentID)
	}
	assertConservation(t, 5, got)
}

// PLAN.md worked example #4: GA values 20, 20, 30, 35, 40 (lower is better).
// Tied pair at sorted positions 1,2 → each gets (5+4)/2 = 4.5.
// Final points: 4.5, 4.5, 3, 2, 1.
func TestRankCategory_TwoWayTieAtFirst_LowerIsBetter(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, Value: 20},
		{AgentID: 2, Value: 20},
		{AgentID: 3, Value: 30},
		{AgentID: 4, Value: 35},
		{AgentID: 5, Value: 40},
	}
	got := RankCategory(stats, true)
	require.Len(t, got, 5)

	by := pointsByID(got)
	assert.Equal(t, 4.5, by[1].RotoPoints)
	assert.Equal(t, 4.5, by[2].RotoPoints)
	assert.Equal(t, 3.0, by[3].RotoPoints)
	assert.Equal(t, 2.0, by[4].RotoPoints)
	assert.Equal(t, 1.0, by[5].RotoPoints)

	assert.Equal(t, 1, by[1].Rank)
	assert.Equal(t, 1, by[2].Rank)
	assert.Equal(t, 3, by[3].Rank)
	assert.Equal(t, 4, by[4].Rank)
	assert.Equal(t, 5, by[5].Rank)

	assertConservation(t, 5, got)
}

// PLAN.md worked example #5: zero-TOI agent receives worst rank in GAA
// regardless of value. Even a hypothetical agent with the best raw value
// must lose to every agent that actually had goalie minutes.
func TestRankGAA_ZeroTOI_GetsWorstRank(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, GoalieGA: 90, GoalieTOISeconds: 120000},  // 2.7 GAA
		{AgentID: 2, GoalieGA: 80, GoalieTOISeconds: 120000},  // 2.4 GAA  ← best
		{AgentID: 3, GoalieGA: 100, GoalieTOISeconds: 120000}, // 3.0 GAA
		{AgentID: 4, GoalieGA: 110, GoalieTOISeconds: 120000}, // 3.3 GAA
		{AgentID: 5, GoalieGA: 0, GoalieTOISeconds: 0},        // worst-rank
	}
	got := RankGAA(stats)
	require.Len(t, got, 5)

	by := pointsByID(got)
	assert.Equal(t, 1, by[2].Rank, "best GAA wins")
	assert.Equal(t, 2, by[1].Rank)
	assert.Equal(t, 3, by[3].Rank)
	assert.Equal(t, 4, by[4].Rank)
	assert.Equal(t, 5, by[5].Rank, "zero-TOI sorts last")

	assert.Equal(t, 5.0, by[2].RotoPoints)
	assert.Equal(t, 4.0, by[1].RotoPoints)
	assert.Equal(t, 3.0, by[3].RotoPoints)
	assert.Equal(t, 2.0, by[4].RotoPoints)
	assert.Equal(t, 1.0, by[5].RotoPoints)

	// Reported GAA values (allowing FP slack on 80/120000*3600 etc.).
	assert.InDelta(t, 2.7, by[1].Value, 1e-9)
	assert.InDelta(t, 2.4, by[2].Value, 1e-9)
	assert.InDelta(t, 3.0, by[3].Value, 1e-9)
	assert.InDelta(t, 3.3, by[4].Value, 1e-9)
	// Zero-TOI agent's reported Value is 0, never the +Inf sentinel.
	assert.Equal(t, 0.0, by[5].Value)
	assert.False(t, math.IsInf(by[5].Value, 1))

	assertConservation(t, 5, got)
}

// Multiple zero-TOI agents tie at the bottom — they all share the lowest
// position and split the lowest points using the standard rule.
func TestRankGAA_MultipleZeroTOI_TieAtBottom(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, GoalieGA: 80, GoalieTOISeconds: 120000}, // 2.4
		{AgentID: 2, GoalieGA: 90, GoalieTOISeconds: 120000}, // 2.7
		{AgentID: 3, GoalieGA: 0, GoalieTOISeconds: 0},
		{AgentID: 4, GoalieGA: 0, GoalieTOISeconds: 0},
	}
	got := RankGAA(stats)
	require.Len(t, got, 4)

	by := pointsByID(got)
	assert.Equal(t, 1, by[1].Rank)
	assert.Equal(t, 4.0, by[1].RotoPoints)
	assert.Equal(t, 2, by[2].Rank)
	assert.Equal(t, 3.0, by[2].RotoPoints)
	// Zero-TOI tie at position 3, K=2: each gets (2+1)/2 = 1.5.
	assert.Equal(t, 3, by[3].Rank)
	assert.Equal(t, 1.5, by[3].RotoPoints)
	assert.Equal(t, 0.0, by[3].Value)
	assert.Equal(t, 3, by[4].Rank)
	assert.Equal(t, 1.5, by[4].RotoPoints)
	assert.Equal(t, 0.0, by[4].Value)

	assertConservation(t, 4, got)
}

// All-zero-TOI degenerate case: GAA is undefined for every agent.
// They all tie at position 1 with the average of all positions.
func TestRankGAA_AllZeroTOI_AllTie(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, GoalieGA: 0, GoalieTOISeconds: 0},
		{AgentID: 2, GoalieGA: 0, GoalieTOISeconds: 0},
		{AgentID: 3, GoalieGA: 0, GoalieTOISeconds: 0},
	}
	got := RankGAA(stats)
	require.Len(t, got, 3)
	for _, r := range got {
		assert.Equal(t, 1, r.Rank)
		assert.Equal(t, 2.0, r.RotoPoints) // (3+2+1)/3 = 2
		assert.Equal(t, 0.0, r.Value)
	}
	assertConservation(t, 3, got)
}

func TestRankCategory_EmptyInput(t *testing.T) {
	assert.Empty(t, RankCategory(nil, false))
	assert.Empty(t, RankGAA(nil))
}

// Single-agent pool: that agent is automatically 1st and earns 1 point.
func TestRankCategory_SingleAgent(t *testing.T) {
	got := RankCategory([]AgentCategoryStat{{AgentID: 7, Value: 42}}, false)
	require.Len(t, got, 1)
	assert.Equal(t, 1.0, got[0].RotoPoints)
	assert.Equal(t, 1, got[0].Rank)
	assertConservation(t, 1, got)
}

// +/- can be negative — verify higher-is-better still works correctly
// when values straddle zero.
func TestRankCategory_NegativeValues_PlusMinus(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 1, Value: -10}, // worst
		{AgentID: 2, Value: 5},   // best
		{AgentID: 3, Value: -3},  // middle
	}
	got := RankCategory(stats, false)
	require.Len(t, got, 3)

	by := pointsByID(got)
	assert.Equal(t, 3.0, by[2].RotoPoints)
	assert.Equal(t, 2.0, by[3].RotoPoints)
	assert.Equal(t, 1.0, by[1].RotoPoints)
	assertConservation(t, 3, got)
}

// The output is in input order (not sorted) so callers can zip with their
// own parallel arrays (agent display names, etc.) without re-keying.
func TestRankCategory_PreservesInputOrder(t *testing.T) {
	stats := []AgentCategoryStat{
		{AgentID: 100, Value: 5},
		{AgentID: 200, Value: 9},
		{AgentID: 300, Value: 1},
	}
	got := RankCategory(stats, false)
	require.Len(t, got, 3)
	assert.Equal(t, int64(100), got[0].AgentID)
	assert.Equal(t, int64(200), got[1].AgentID)
	assert.Equal(t, int64(300), got[2].AgentID)
}

func TestCategory_IsLowerBetter(t *testing.T) {
	for _, c := range []Category{CategoryG, CategoryA, CategoryPM, CategoryPIM, CategoryPPP, CategorySOG, CategoryW} {
		assert.False(t, c.IsLowerBetter(), "%s should be higher-is-better", c)
	}
	assert.True(t, CategoryGA.IsLowerBetter())
	assert.True(t, CategoryGAA.IsLowerBetter())
}

// RankAllCategories must dispatch GAA through RankGAA (so zero-TOI worst-rank
// fires) and counting categories through RankCategory with the right direction.
func TestRankAllCategories_DispatchesGAACorrectly(t *testing.T) {
	cats := []CategoryStats{
		{Category: CategoryG, Stats: []AgentCategoryStat{
			{AgentID: 1, Value: 10},
			{AgentID: 2, Value: 5},
		}},
		{Category: CategoryGA, Stats: []AgentCategoryStat{
			{AgentID: 1, Value: 30},
			{AgentID: 2, Value: 20},
		}},
		{Category: CategoryGAA, Stats: []AgentCategoryStat{
			{AgentID: 1, GoalieGA: 30, GoalieTOISeconds: 60000}, // 1.8 GAA
			{AgentID: 2, GoalieGA: 0, GoalieTOISeconds: 0},      // worst-rank
		}},
	}
	got := RankAllCategories(cats)
	require.Len(t, got, 3)

	gByID := pointsByID(got[CategoryG])
	assert.Equal(t, 2.0, gByID[1].RotoPoints, "G higher-better: agent 1 (10) > agent 2 (5)")
	assert.Equal(t, 1.0, gByID[2].RotoPoints)

	gaByID := pointsByID(got[CategoryGA])
	assert.Equal(t, 2.0, gaByID[2].RotoPoints, "GA lower-better: agent 2 (20) < agent 1 (30)")
	assert.Equal(t, 1.0, gaByID[1].RotoPoints)

	gaaByID := pointsByID(got[CategoryGAA])
	assert.Equal(t, 2.0, gaaByID[1].RotoPoints, "agent 1 has TOI; agent 2 (zero-TOI) sorts last")
	assert.Equal(t, 1.0, gaaByID[2].RotoPoints)
	assert.Equal(t, 0.0, gaaByID[2].Value, "zero-TOI value reported as 0, not +Inf")
}

// Per-agent totals are summed across every category and sorted by total
// descending, AgentID ascending on ties.
func TestAgentRotoTotals_SumsAcrossCategoriesAndSortsDescending(t *testing.T) {
	// Designed so two distinct ties occur:
	//   agent 1: 5 + 2 = 7    \
	//   agent 2: 4 + 3 = 7    /  tied at 7
	//   agent 3: 3 + 3 = 6    \
	//   agent 5: 1 + 5 = 6    /  tied at 6
	//   agent 4: 2 + 2 = 4
	rankings := map[Category][]CategoryRanking{
		CategoryG: {
			{AgentID: 1, RotoPoints: 5},
			{AgentID: 2, RotoPoints: 4},
			{AgentID: 3, RotoPoints: 3},
			{AgentID: 4, RotoPoints: 2},
			{AgentID: 5, RotoPoints: 1},
		},
		CategoryA: {
			{AgentID: 1, RotoPoints: 2},
			{AgentID: 2, RotoPoints: 3},
			{AgentID: 3, RotoPoints: 3},
			{AgentID: 4, RotoPoints: 2},
			{AgentID: 5, RotoPoints: 5},
		},
	}
	totals := AgentRotoTotals(rankings)
	require.Len(t, totals, 5)

	// Sorted by total descending; ties broken by AgentID ascending.
	assert.Equal(t, int64(1), totals[0].AgentID, "agent 1 wins the 7-point tie by lower ID")
	assert.Equal(t, 7.0, totals[0].TotalRotoPoints)
	assert.Equal(t, int64(2), totals[1].AgentID)
	assert.Equal(t, 7.0, totals[1].TotalRotoPoints)
	assert.Equal(t, int64(3), totals[2].AgentID, "agent 3 wins the 6-point tie by lower ID")
	assert.Equal(t, 6.0, totals[2].TotalRotoPoints)
	assert.Equal(t, int64(5), totals[3].AgentID)
	assert.Equal(t, 6.0, totals[3].TotalRotoPoints)
	assert.Equal(t, int64(4), totals[4].AgentID)
	assert.Equal(t, 4.0, totals[4].TotalRotoPoints)
}

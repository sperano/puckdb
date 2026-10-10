package simulation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// SnakeDraftOrder / SnakeDirection
// ============================================================================

func TestSnakeDraftOrder_FiveTeamsThreeRounds(t *testing.T) {
	order := []int64{1, 2, 3, 4, 5}
	got := SnakeDraftOrder(order, 3)
	want := []int64{
		1, 2, 3, 4, 5, // round 1: ascending
		5, 4, 3, 2, 1, // round 2: descending
		1, 2, 3, 4, 5, // round 3: ascending again
	}
	assert.Equal(t, want, got)
}

func TestSnakeDraftOrder_DoesNotMutateInput(t *testing.T) {
	order := []int64{10, 20, 30}
	original := slicesClone(order)
	_ = SnakeDraftOrder(order, 4)
	assert.Equal(t, original, order, "agentOrder must not be mutated")
}

func TestSnakeDraftOrder_EdgeCases(t *testing.T) {
	assert.Nil(t, SnakeDraftOrder(nil, 5))
	assert.Nil(t, SnakeDraftOrder([]int64{1, 2}, 0))
	assert.Nil(t, SnakeDraftOrder([]int64{1, 2}, -1))
	assert.Nil(t, SnakeDraftOrder([]int64{}, 5),
		"both empty inputs (zero-length agentOrder OR nonpositive rounds) → nil; consistent shape simplifies caller checks")
}

func TestSnakeDraftOrder_SinglePick(t *testing.T) {
	assert.Equal(t, []int64{42}, SnakeDraftOrder([]int64{42}, 1))
}

// 18 rounds × 5 teams = 90 picks. The last round (18, even) descends.
func TestSnakeDraftOrder_LastRoundIsDescendingOnEvenRoundCount(t *testing.T) {
	got := SnakeDraftOrder([]int64{1, 2, 3, 4, 5}, 18)
	assert.Len(t, got, 90)
	// Round 18 starts at index 85 (5 * 17).
	assert.Equal(t, []int64{5, 4, 3, 2, 1}, got[85:])
}

func TestSnakeDirection(t *testing.T) {
	cases := map[int]string{
		1:  "ascending",
		2:  "descending",
		3:  "ascending",
		17: "ascending",
		18: "descending",
	}
	for round, want := range cases {
		assert.Equal(t, want, SnakeDirection(round), "round %d", round)
	}
}

// slicesClone is a tiny test helper avoiding the slices import in
// tests where its only use would be one Clone call.
func slicesClone[T any](s []T) []T {
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// ============================================================================
// RankSkaters
// ============================================================================

func TestRankSkaters_DescByGPlusA(t *testing.T) {
	in := []SkaterDraftCandidate{
		{PlayerID: 1, Name: "low", PriorG: 5, PriorA: 5},    // 10
		{PlayerID: 2, Name: "high", PriorG: 30, PriorA: 50}, // 80
		{PlayerID: 3, Name: "mid", PriorG: 20, PriorA: 25},  // 45
	}
	got := RankSkaters(in)
	require.Len(t, got, 3)
	assert.Equal(t, "high", got[0].Name)
	assert.Equal(t, "mid", got[1].Name)
	assert.Equal(t, "low", got[2].Name)
}

func TestRankSkaters_TieBrokenByPlayerID(t *testing.T) {
	in := []SkaterDraftCandidate{
		{PlayerID: 30, PriorG: 10, PriorA: 10}, // 20
		{PlayerID: 10, PriorG: 10, PriorA: 10}, // 20 (lowest ID)
		{PlayerID: 20, PriorG: 10, PriorA: 10}, // 20
	}
	got := RankSkaters(in)
	assert.Equal(t, []int64{10, 20, 30}, []int64{got[0].PlayerID, got[1].PlayerID, got[2].PlayerID},
		"tied scores must order by player_id ASC for Temporal-replay determinism")
}

func TestRankSkaters_DoesNotMutateInput(t *testing.T) {
	in := []SkaterDraftCandidate{
		{PlayerID: 30, PriorG: 5}, {PlayerID: 10, PriorG: 50},
	}
	original := slicesClone(in)
	_ = RankSkaters(in)
	assert.Equal(t, original, in)
}

// ============================================================================
// RankGoalies
// ============================================================================

func TestRankGoalies_DescByW(t *testing.T) {
	in := []GoalieDraftCandidate{
		{PlayerID: 1, PriorW: 10},
		{PlayerID: 2, PriorW: 35},
		{PlayerID: 3, PriorW: 25},
	}
	got := RankGoalies(in)
	assert.Equal(t, []int64{2, 3, 1},
		[]int64{got[0].PlayerID, got[1].PlayerID, got[2].PlayerID})
}

func TestRankGoalies_TieOnW_LowerGAWins(t *testing.T) {
	in := []GoalieDraftCandidate{
		{PlayerID: 1, PriorW: 30, PriorGA: 110}, // leakier
		{PlayerID: 2, PriorW: 30, PriorGA: 80},  // stingy
	}
	got := RankGoalies(in)
	assert.Equal(t, int64(2), got[0].PlayerID)
}

func TestRankGoalies_TieOnWAndGA_LowerIDWins(t *testing.T) {
	in := []GoalieDraftCandidate{
		{PlayerID: 99, PriorW: 30, PriorGA: 80},
		{PlayerID: 5, PriorW: 30, PriorGA: 80},
	}
	got := RankGoalies(in)
	assert.Equal(t, int64(5), got[0].PlayerID)
}

// ============================================================================
// SelectAvailableByPosition
// ============================================================================

func TestSelectAvailableByPosition_RespectsPerPositionLimit(t *testing.T) {
	skaters := make([]SkaterDraftCandidate, MaxAvailablePerPosition+5)
	for i := range skaters {
		skaters[i] = SkaterDraftCandidate{
			PlayerID: int64(i + 1), Position: "C", PriorG: MaxAvailablePerPosition - i, PriorA: 0,
		}
	}
	skaters = RankSkaters(skaters)

	byPos, _ := SelectAvailableByPosition(skaters, nil, nil)
	assert.Len(t, byPos["C"], MaxAvailablePerPosition)
	assert.Empty(t, byPos["LW"], "no LW candidates → empty bucket, not nil")
}

func TestSelectAvailableByPosition_ExcludesTaken(t *testing.T) {
	skaters := []SkaterDraftCandidate{
		{PlayerID: 1, Position: "C", PriorG: 50, PriorA: 50},
		{PlayerID: 2, Position: "C", PriorG: 40, PriorA: 40},
		{PlayerID: 3, Position: "C", PriorG: 30, PriorA: 30},
	}
	skaters = RankSkaters(skaters)

	taken := map[int64]struct{}{1: {}}
	byPos, _ := SelectAvailableByPosition(skaters, nil, taken)
	require.Len(t, byPos["C"], 2)
	assert.Equal(t, int64(2), byPos["C"][0].ID,
		"player 1 is taken; player 2 (next highest) should be first")
	assert.Equal(t, int64(3), byPos["C"][1].ID)
}

func TestSelectAvailableByPosition_GoaliesPopulateGSlot(t *testing.T) {
	goalies := []GoalieDraftCandidate{
		{PlayerID: 100, Name: "starter", PriorW: 35, PriorGA: 90},
		{PlayerID: 101, Name: "backup", PriorW: 12, PriorGA: 50},
	}
	goalies = RankGoalies(goalies)

	byPos, _ := SelectAvailableByPosition(nil, goalies, nil)
	require.Len(t, byPos["G"], 2)
	assert.Equal(t, "starter", byPos["G"][0].Player)
	stats, ok := byPos["G"][0].LastSeason.(GoalieStats)
	require.True(t, ok)
	assert.Equal(t, 35, stats.W)
}

func TestSelectAvailableByPosition_BestAvailableOverallSkatersOnly(t *testing.T) {
	// PLAN.md leaves BPA across position categories under-specified.
	// Pin the V1 simplification (top-N skaters by G+A) so a future
	// "compute a unified composite" change is an explicit decision.
	skaters := []SkaterDraftCandidate{
		{PlayerID: 1, Position: "C", PriorG: 60, PriorA: 60},
	}
	goalies := []GoalieDraftCandidate{
		{PlayerID: 200, Position: "G", PriorW: 50},
	}
	_, bpa := SelectAvailableByPosition(RankSkaters(skaters), RankGoalies(goalies), nil)
	require.Len(t, bpa, 1)
	assert.Equal(t, int64(1), bpa[0].ID, "BPA contains only skaters in V1")
}

func TestSelectAvailableByPosition_BPATruncatesToMax(t *testing.T) {
	skaters := make([]SkaterDraftCandidate, MaxBestAvailableOverall+10)
	for i := range skaters {
		skaters[i] = SkaterDraftCandidate{
			PlayerID: int64(i + 1), Position: "C", PriorG: 100 - i,
		}
	}
	_, bpa := SelectAvailableByPosition(RankSkaters(skaters), nil, nil)
	assert.Len(t, bpa, MaxBestAvailableOverall)
}

func TestSelectAvailableByPosition_UnknownPositionSkipped(t *testing.T) {
	// "F" is rejected by EligibleSlots; pin that an LLM-/data-side
	// "F" doesn't crash the bucketing — it just falls out of the
	// available-by-position output.
	skaters := []SkaterDraftCandidate{
		{PlayerID: 1, Position: "F", PriorG: 50, PriorA: 50},
		{PlayerID: 2, Position: "C", PriorG: 30, PriorA: 30},
	}
	byPos, _ := SelectAvailableByPosition(RankSkaters(skaters), nil, nil)
	assert.Empty(t, byPos["F"], "unknown position F must not create a bucket")
	require.Len(t, byPos["C"], 1)
	assert.Equal(t, int64(2), byPos["C"][0].ID)
}

// ============================================================================
// computeSlotsRemaining
// ============================================================================

// v1Need is computeSlotsRemaining's output for the V1 roster before
// the agent's first pick.
func v1Need() map[string]int {
	return computeSlotsRemaining(PoolConfig{RosterPositions: v1FixedRoster}, nil, nil)
}

func TestComputeSlotsRemaining_ConfiguredPositionsOnly(t *testing.T) {
	cfg := PoolConfig{RosterPositions: map[RosterSlot]int{
		SlotC: 1, SlotLW: 3, SlotRW: 1, SlotD: 4, SlotG: 1,
		SlotUtil: 2, SlotBN: 4, SlotIR: 1,
	}}
	got := computeSlotsRemaining(cfg, nil, nil)
	assert.Equal(t, map[string]int{"C": 1, "LW": 3, "RW": 1, "D": 4, "G": 1}, got,
		"configured counts are used as-is; Util/BN/IR carry no positional need")
}

func TestComputeSlotsRemaining_PicksReduceNeedAndClampAtZero(t *testing.T) {
	cfg := PoolConfig{RosterPositions: map[RosterSlot]int{SlotC: 1, SlotD: 2, SlotG: 1}}
	lookup := map[int64]DraftablePlayer{
		1: {ID: 1, Position: "C"},
		2: {ID: 2, Position: "C"}, // second C: C is already at 0
		3: {ID: 3, Position: "D"},
		4: {ID: 4, Position: "LW"}, // LW is not configured
	}
	got := computeSlotsRemaining(cfg, []int64{1, 2, 3, 4, 999}, lookup)
	assert.Equal(t, map[string]int{"C": 0, "D": 1, "G": 1}, got,
		"over-drafted positions stay at 0; picks missing from the lookup are ignored")
}

// ============================================================================
// FallbackDraftPick
// ============================================================================

func TestFallbackDraftPick_EmptyRoster_PrioritizesLargestConfiguredNeed(t *testing.T) {
	// V1 roster, no picks yet: D (3 slots) has the largest need.
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 1, Position: "C", PriorG: 50, PriorA: 50},
		{PlayerID: 2, Position: "D", PriorG: 10, PriorA: 10},
	})
	got := FallbackDraftPick(v1Need(), skaters, nil, nil)
	assert.Equal(t, int64(2), got, "D needs 3 slots, every other position 2")
}

func TestFallbackDraftPick_TieBreaksInFixedOrder(t *testing.T) {
	// All positions equally needed; the fixed tie-break order picks C
	// first. This is the determinism guarantee.
	need := map[string]int{"C": 1, "LW": 1, "RW": 1, "D": 1, "G": 1}
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 1, Position: "C", PriorG: 50, PriorA: 50},
		{PlayerID: 2, Position: "LW", PriorG: 60, PriorA: 60}, // higher score, but LW
	})
	got := FallbackDraftPick(need, skaters, nil, nil)
	assert.Equal(t, int64(1), got, "tie on positional need → fixed C-first ordering picks C")
}

// Regression for the fallback always seeing an empty roster: the
// agent's occupied positions must move the pick to what is still
// needed, computed from the agent's real picks.
func TestFallbackDraftPick_OccupiedPositionsShiftPick(t *testing.T) {
	lookup := map[int64]DraftablePlayer{
		1: {ID: 1, Position: "D"}, 2: {ID: 2, Position: "D"}, 3: {ID: 3, Position: "D"},
		4: {ID: 4, Position: "C"}, 5: {ID: 5, Position: "C"},
	}
	need := computeSlotsRemaining(PoolConfig{RosterPositions: v1FixedRoster}, []int64{1, 2, 3, 4, 5}, lookup)

	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 100, Position: "D", PriorG: 90, PriorA: 90},
		{PlayerID: 101, Position: "C", PriorG: 80, PriorA: 80},
		{PlayerID: 102, Position: "LW", PriorG: 30, PriorA: 30},
	})
	got := FallbackDraftPick(need, skaters, nil, nil)
	assert.Equal(t, int64(102), got,
		"D and C are full; LW (need 2, first in tie order) beats better-scored D and C")
}

func TestFallbackDraftPick_EmptyPositionBeatsPartiallyFilled(t *testing.T) {
	// PLAN.md: "prioritize positions with 0 filled slots". C has one
	// of two slots left, LW both.
	need := map[string]int{"C": 1, "LW": 2}
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 100, Position: "C", PriorG: 99, PriorA: 99},
		{PlayerID: 200, Position: "LW", PriorG: 30, PriorA: 30},
	})
	got := FallbackDraftPick(need, skaters, nil, nil)
	assert.Equal(t, int64(200), got)
}

func TestFallbackDraftPick_SkipsTakenPlayers(t *testing.T) {
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 1, Position: "C", PriorG: 80, PriorA: 80},
		{PlayerID: 2, Position: "C", PriorG: 70, PriorA: 70},
	})
	taken := map[int64]struct{}{1: {}}
	got := FallbackDraftPick(map[string]int{"C": 1}, skaters, nil, taken)
	assert.Equal(t, int64(2), got)
}

func TestFallbackDraftPick_GoaliesPickedWhenOnlyGoalieNeedLeft(t *testing.T) {
	need := map[string]int{"C": 0, "LW": 0, "RW": 0, "D": 0, "G": 1}
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 100, Position: "C", PriorG: 80, PriorA: 80},
	})
	goalies := RankGoalies([]GoalieDraftCandidate{
		{PlayerID: 999, Position: "G", PriorW: 30, PriorGA: 90},
	})
	got := FallbackDraftPick(need, skaters, goalies, nil)
	assert.Equal(t, int64(999), got, "only goalies have need → pick top goalie")
}

func TestFallbackDraftPick_NeededPositionWithoutCandidatesFallsThrough(t *testing.T) {
	// D is most needed but has no candidate; the next-most-needed wins.
	need := map[string]int{"D": 3, "RW": 1}
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 100, Position: "C", PriorG: 80, PriorA: 80},
		{PlayerID: 200, Position: "RW", PriorG: 30, PriorA: 30},
	})
	got := FallbackDraftPick(need, skaters, nil, nil)
	assert.Equal(t, int64(200), got)
}

func TestFallbackDraftPick_NoNeedLeftPicksBPA(t *testing.T) {
	need := map[string]int{"C": 0, "LW": 0, "RW": 0, "D": 0, "G": 0}
	skaters := RankSkaters([]SkaterDraftCandidate{
		{PlayerID: 200, Position: "C", PriorG: 40, PriorA: 40},
	})
	got := FallbackDraftPick(need, skaters, nil, nil)
	assert.Equal(t, int64(200), got, "all positions full → BPA safety net picks the available skater")
}

func TestFallbackDraftPick_NoCandidatesReturnsZero(t *testing.T) {
	got := FallbackDraftPick(v1Need(), nil, nil, nil)
	assert.Equal(t, int64(0), got, "no candidates anywhere → 0 (caller treats as draft-cannot-proceed)")
}

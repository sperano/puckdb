package draft

import (
	"slices"
	"testing"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/require"
)

func testTeamEnvironment() *projection.TeamEnvironment {
	return &projection.TeamEnvironment{
		FromTeamID: 3, FromTeam: "NYR", ToTeamID: 26, ToTeam: "LAK", OtherClubShare: 1,
		Factors: map[projection.Stat]float64{
			projection.StatGoals: 1.04, projection.StatAssists: 1.04,
			projection.StatShotsOnGoal: 0.97, projection.StatPowerPlayPoints: 1.02,
		},
	}
}

func TestTeamEnvironmentExplanation(t *testing.T) {
	t.Parallel()

	require.Equal(t, "team environment: NYR → LAK (100% of weighted history with other clubs); "+
		"goals ×1.040, assists ×1.040, shots ×0.970, power-play points ×1.020; club rates regressed toward league average",
		teamEnvironmentExplanation(*testTeamEnvironment()))

	stayed := *testTeamEnvironment()
	stayed.FromTeamID, stayed.FromTeam, stayed.OtherClubShare = 26, "LAK", 0.3
	stayed.ToTeam = ""
	require.Contains(t, teamEnvironmentExplanation(stayed), "stays with team 26 (30% of weighted history with other clubs)")
}

func TestBuildRanking_ExplainsTeamEnvironment(t *testing.T) {
	t.Parallel()

	moved := testPlayer("moved", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10})
	moved.TeamEnvironment = testTeamEnvironment()
	stayed := testPlayer("stayed", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 8})
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestGoalStat, 1)},
		[]RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}, moved, stayed)

	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	require.NoError(t, err)
	require.True(t, slices.Contains(rankedByKey(t, ranking, "moved").Explanations, teamEnvironmentExplanation(*moved.TeamEnvironment)))
	for _, explanation := range rankedByKey(t, ranking, "stayed").Explanations {
		require.NotContains(t, explanation, "team environment")
	}
}

func TestSeasonLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "2026-27", SeasonLabel(20262027))
	require.Equal(t, "1999-00", SeasonLabel(19992000))
}

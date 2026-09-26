package draft

import (
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/projection"
)

const (
	rankingTestFaceoffsWonStat  = 16
	rankingTestFaceoffsLostStat = 17
	// rankingTestUncertainty is large enough that a per-category penalty
	// on offsetting faceoff contributions would visibly separate the players.
	rankingTestUncertainty = 0.5
	scoreTolerance         = 1e-9
)

// Faceoffs won and lost offset each other: a centre who wins as many as he
// loses nets nothing, so in a head-to-head league he must score the same as
// a winger with equal goals and no faceoffs. Penalizing each category's
// magnitude separately would charge the centre for both halves of the pair.
func TestBuildRanking_HeadToHeadDownsideAppliesToNetTotal(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	skater := func(key string, goals, faceoffs float64) projection.PlayerProjection {
		return testPlayer(key, projection.PlayerKindSkater, rankingTestUncertainty, map[projection.Stat]float64{
			projection.StatGoals: goals, projection.StatFaceoffsWon: faceoffs, projection.StatFaceoffsLost: faceoffs,
		})
	}
	categories := []StatCategory{
		testCategory(rankingTestGoalStat, HigherIsBetter),
		testCategory(rankingTestFaceoffsWonStat, HigherIsBetter),
		testCategory(rankingTestFaceoffsLostStat, LowerIsBetter),
	}
	rules, projected, pool := testRankingInput("head", categories, slots,
		skater("centre", 20, 800), skater("winger", 20, 0),
		skater("depth-centre", 10, 400), skater("depth-winger", 10, 0))
	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	centre, winger := rankedByKey(t, ranking, "centre"), rankedByKey(t, ranking, "winger")
	if math.Abs(centre.OfficialScore-winger.OfficialScore) > scoreTolerance {
		t.Fatalf("offsetting faceoffs changed the head-to-head score: centre %v, winger %v", centre.OfficialScore, winger.OfficialScore)
	}
}

func TestHeadToHeadDownside(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		total       float64
		uncertainty float64
		want        float64
	}{
		{name: "certain total is unchanged", total: 2, uncertainty: 0, want: 2},
		{name: "positive total shrinks", total: 2, uncertainty: 1, want: 1},
		{name: "negative total drops further", total: -2, uncertainty: 1, want: -3},
		{name: "zero total is unchanged", total: 0, uncertainty: 1, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := headToHeadDownside(tt.total, tt.uncertainty); math.Abs(got-tt.want) > scoreTolerance {
				t.Fatalf("headToHeadDownside(%v, %v) = %v, want %v", tt.total, tt.uncertainty, got, tt.want)
			}
		})
	}
}

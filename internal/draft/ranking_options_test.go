package draft

import (
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
)

func TestBuildRankingValidatesOptionPoliciesAndCategoryWeightLimit(t *testing.T) {
	rules, projected, pool := testRankingInput("point",
		[]StatCategory{testWeightedCategory(rankingTestGoalStat, 1)},
		[]RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}},
		testPlayer("center", projection.PlayerKindSkater, 0,
			map[projection.Stat]float64{projection.StatGoals: 1}))

	tests := map[string]struct {
		options RankingOptions
		wantErr bool
	}{
		"maximum category weight": {options: RankingOptions{
			CategoryWeights: map[int]float64{rankingTestGoalStat: MaxCategoryWeight},
		}},
		"category weight above maximum": {options: RankingOptions{
			CategoryWeights: map[int]float64{rankingTestGoalStat: math.Nextafter(MaxCategoryWeight, math.Inf(1))},
		}, wantErr: true},
		"non-finite category weight": {options: RankingOptions{
			CategoryWeights: map[int]float64{rankingTestGoalStat: math.Inf(1)},
		}, wantErr: true},
		"unknown bench policy": {
			options: RankingOptions{BenchPolicy: BenchPolicy("sometimes")}, wantErr: true,
		},
		"unknown workload policy": {
			options: RankingOptions{WorkloadCapPolicy: WorkloadCapPolicy("league-wide")}, wantErr: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := BuildRanking(rules, projected, pool, test.options)
			if test.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

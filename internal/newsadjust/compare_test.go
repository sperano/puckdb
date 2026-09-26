package newsadjust

import (
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDraftSeason  = 2026
	yahooWins        = 19
	yahooGAA         = 23
	yahooSaves       = 25
	yahooSavePct     = 26
	testWinsWeight   = 5
	testSavesWeight  = 0.2
	testMidStarts    = 50
	testRatioTolRank = 1e-6
)

func goalieRules(leagueKey, scoringType string, categories ...draft.StatCategory) draft.Snapshot {
	return draft.Snapshot{Rules: draft.Rules{
		LeagueKey: leagueKey, Season: testDraftSeason, ScoringType: scoringType, NumTeams: 1,
		Categories:  categories,
		RosterSlots: []draft.RosterSlot{{Position: draft.PositionGoalie, Count: 1, Starting: true}},
	}, Source: draft.SourceYahooAPI}
}

func weighted(id int, weight float64) draft.StatCategory {
	return draft.StatCategory{StatID: id, Enabled: true, Weight: &weight}
}

func category(id int, direction draft.Direction) draft.StatCategory {
	return draft.StatCategory{StatID: id, Enabled: true, Direction: direction}
}

func goaliePool(players ...projection.PlayerProjection) []draft.PoolPlayer {
	pool := make([]draft.PoolPlayer, len(players))
	for i, p := range players {
		pool[i] = draft.PoolPlayer{PlayerKey: p.PlayerKey, Name: p.PlayerKey, EligiblePositions: []string{draft.PositionGoalie}}
	}
	return pool
}

func rowFor(t *testing.T, comparison Comparison, key string) ComparisonRow {
	t.Helper()
	for _, row := range comparison.Rows {
		if row.PlayerKey == key {
			return row
		}
	}
	t.Fatalf("comparison has no %s", key)
	return ComparisonRow{}
}

// A suspension removes starts. In a wins/saves points league that cuts the
// goalie's score in proportion; in a GAA/SV% category league the per-start
// ratios are unchanged and only their weight in the team ratio moves.
func TestCompare_SuspensionPropagatesByLeagueRules(t *testing.T) {
	star := testGoalie(testGoalieKey, testGoalieStarts)
	mid := testGoalie(testOtherKey, testMidStarts)
	mid.Values[projection.StatSaves] = exact(testGoalieShots * testMidStarts / testGoalieStarts * 0.905)
	mid.Values[projection.StatSavePercentage] = exact(0.905)
	backup := testGoalie(testBackupKey, 25)
	baseline := testBaseline(star, mid, backup)
	result := mustApply(t, testRequest(baseline,
		testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})))
	pool := goaliePool(star, mid, backup)

	points, err := Compare(goalieRules(testLeague1001, "point", weighted(yahooWins, testWinsWeight), weighted(yahooSaves, testSavesWeight)),
		baseline, result, pool, draft.RankingOptions{})
	require.NoError(t, err)
	ratios, err := Compare(goalieRules(testLeague1002, "roto", category(yahooGAA, draft.LowerIsBetter), category(yahooSavePct, draft.HigherIsBetter)),
		baseline, result, pool, draft.RankingOptions{})
	require.NoError(t, err)

	available := (testSeasonGames - DefaultPolicy().MissedGames[DurationIndefinite].Base) / testSeasonGames
	pointsRow := rowFor(t, points, testGoalieKey)
	assert.InDelta(t, available*pointsRow.Baseline.Score, pointsRow.Scenarios[ScenarioBase].Score, testRatioTolRank)
	assert.Less(t, pointsRow.Scenarios[ScenarioConservative].Score, pointsRow.Scenarios[ScenarioBase].Score)
	assert.Equal(t, 1, pointsRow.Baseline.Rank)
	assert.Greater(t, pointsRow.Scenarios[ScenarioBase].Rank, pointsRow.Baseline.Rank, "the suspension drops the goalie below the healthy starter")
	assert.Equal(t, pointsRow.Baseline, pointsRow.Scenarios[ScenarioOptimistic], "the optimistic scenario assumes no missed games")

	ratioRow := rowFor(t, ratios, testGoalieKey)
	require.Positive(t, ratioRow.Baseline.Score)
	assert.Greater(t, ratioRow.Scenarios[ScenarioBase].Score/ratioRow.Baseline.Score, available,
		"ratio categories lose less than counting stats")
	assert.NotNil(t, ratioRow.Adjustment)
	assert.NotEqual(t, points.BaselineVersion, points.ScenarioVersions[ScenarioBase])
	assert.NotEmpty(t, points.RulesHash)
	assert.NotEqual(t, points.RulesHash, ratios.RulesHash, "each comparison names the rule version it ranked under")
	assert.Contains(t, points.Assumptions[len(points.Assumptions)-1], DefaultCalibration)
}

func TestCompare_RejectsMismatchedInputs(t *testing.T) {
	baseline := testBaseline(testGoalie(testGoalieKey, testGoalieStarts))
	req := testRequest(baseline)
	req.LeagueKey = testLeague1001
	result := mustApply(t, req)
	rules := goalieRules(testLeague1002, "point", weighted(yahooWins, testWinsWeight))

	_, err := Compare(rules, baseline, result, goaliePool(baseline.Players...), draft.RankingOptions{})
	assert.ErrorContains(t, err, "overrides for league")

	other := baseline
	other.SourceDataHash = "other"
	_, err = Compare(goalieRules(testLeague1001, "point", weighted(yahooWins, testWinsWeight)), other, result, goaliePool(baseline.Players...), draft.RankingOptions{})
	assert.ErrorContains(t, err, "another baseline")
}

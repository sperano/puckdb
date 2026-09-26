package draftrank_test

import (
	"testing"

	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rowKeys(rows []draftrank.Row) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.PlayerKey)
	}
	return keys
}

func ranksByKey(rows []draftrank.Row) map[string]int {
	ranks := make(map[string]int, len(rows))
	for _, row := range rows {
		ranks[row.PlayerKey] = row.Placement.OverallRank
	}
	return ranks
}

func TestView_PositionFilterIsInclusiveWithoutDuplicatesOrRenumbering(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	all, err := snapshot.View(draftrank.Query{})
	require.NoError(t, err)
	filtered, err := snapshot.View(draftrank.Query{Positions: []string{"c", " LW ", "C"}})
	require.NoError(t, err)

	keys := rowKeys(filtered.Rows)
	assert.ElementsMatch(t, []string{
		draftfixtures.TopCenter, draftfixtures.CenterWing, draftfixtures.LeftWing, draftfixtures.DepthCenter, draftfixtures.AccentWing,
	}, keys)
	assert.Equal(t, filtered.Total, len(keys), "a C/LW player is listed once")
	allRanks := ranksByKey(all.Rows)
	for _, row := range filtered.Rows {
		assert.Equal(t, allRanks[row.PlayerKey], row.Placement.OverallRank, "filter keeps %s's overall rank", row.PlayerKey)
		assert.Equal(t, row.Placements[draftrank.ScenarioBase].PositionRanks, row.Placement.PositionRanks)
	}
}

func TestView_PositionRankFollowsTheFilter(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	lw, err := snapshot.View(draftrank.Query{Positions: []string{"LW"}, Scenario: draftrank.ScenarioBaseline})
	require.NoError(t, err)
	for _, row := range lw.Rows {
		assert.Equal(t, row.Placement.PositionRanks["LW"], row.PositionRank)
	}
	c, err := snapshot.View(draftrank.Query{PlayerKeys: []string{draftfixtures.CenterWing}, Scenario: draftrank.ScenarioBaseline})
	require.NoError(t, err)
	ranks := c.Rows[0].Placement.PositionRanks
	assert.Equal(t, min(ranks["C"], ranks["LW"]), c.Rows[0].PositionRank, "unfiltered: best eligible position rank")
}

func TestView_PaginationCoversEveryRowOnceInStableOrder(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	for _, sort := range []draftrank.SortField{draftrank.SortOverallRank, draftrank.SortTier, draftrank.SortTeam, draftrank.SortRankChange} {
		all, err := snapshot.View(draftrank.Query{Sort: sort})
		require.NoError(t, err)
		var paged []string
		const pageSize = 3
		for offset := 0; offset < all.Total; offset += pageSize {
			page, err := snapshot.View(draftrank.Query{Sort: sort, Offset: offset, Limit: pageSize})
			require.NoError(t, err)
			assert.Equal(t, all.Total, page.Total)
			paged = append(paged, rowKeys(page.Rows)...)
		}
		assert.Equal(t, rowKeys(all.Rows), paged, "sort %s pages are a partition", sort)
		again, err := snapshot.View(draftrank.Query{Sort: sort})
		require.NoError(t, err)
		assert.Equal(t, rowKeys(all.Rows), rowKeys(again.Rows), "sort %s is deterministic", sort)
	}
	past, err := snapshot.View(draftrank.Query{Offset: 100, Limit: 5})
	require.NoError(t, err)
	assert.Empty(t, past.Rows)
	assert.NotNil(t, past.Rows)
}

func TestView_SortDirections(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	natural, err := snapshot.View(draftrank.Query{Sort: draftrank.SortValue})
	require.NoError(t, err)
	for i := 1; i < len(natural.Rows); i++ {
		assert.GreaterOrEqual(t, natural.Rows[i-1].Placement.Value, natural.Rows[i].Placement.Value, "values sort descending by default")
	}
	ascending, err := snapshot.View(draftrank.Query{Sort: draftrank.SortValue, Direction: draftrank.SortAscending})
	require.NoError(t, err)
	assert.LessOrEqual(t, ascending.Rows[0].Placement.Value, ascending.Rows[len(ascending.Rows)-1].Placement.Value)
	byName, err := snapshot.View(draftrank.Query{Sort: draftrank.SortName, Direction: draftrank.SortDescending})
	require.NoError(t, err)
	assert.Equal(t, "Tim Stützle", byName.Rows[0].Name)
}

func TestView_SearchIgnoresCaseAndAccentsAndNeedsEveryWord(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	accent, err := snapshot.View(draftrank.Query{Search: "STUTZLE"})
	require.NoError(t, err)
	assert.Equal(t, []string{draftfixtures.AccentWing}, rowKeys(accent.Rows))
	both, err := snapshot.View(draftrank.Query{Search: "goalie wpg"})
	require.NoError(t, err)
	assert.Equal(t, []string{draftfixtures.StarterG}, rowKeys(both.Rows))
	none, err := snapshot.View(draftrank.Query{Search: "nobody"})
	require.NoError(t, err)
	assert.Zero(t, none.Total)
}

func TestView_ScenarioSelection(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	defaulted, err := snapshot.View(draftrank.Query{})
	require.NoError(t, err)
	assert.Equal(t, draftrank.ScenarioBase, defaulted.Scenario)
	suspended, err := snapshot.View(draftrank.Query{PlayerKeys: []string{draftfixtures.SuspendedKey}})
	require.NoError(t, err)
	row := suspended.Rows[0]
	assert.Equal(t, row.BaselineRank-row.Placement.OverallRank, row.RankChange)
	assert.Negative(t, row.RankChange, "the suspension moved the player down")

	snapshot.Scenarios = []draftrank.Scenario{draftrank.ScenarioBaseline}
	fallback, err := snapshot.View(draftrank.Query{Scenario: draftrank.ScenarioOptimistic})
	require.NoError(t, err)
	assert.Equal(t, draftrank.ScenarioBaseline, fallback.Scenario)
	require.Len(t, fallback.Issues, 1)
	assert.Equal(t, draftrank.IssueScenarioFallback, fallback.Issues[0].Code)
	plain, err := snapshot.View(draftrank.Query{})
	require.NoError(t, err)
	assert.Equal(t, draftrank.ScenarioBaseline, plain.Scenario)
	assert.Empty(t, plain.Issues)
}

func TestView_RejectsInvalidQueries(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	for name, q := range map[string]draftrank.Query{
		"unknown position":  {Positions: []string{"UTIL"}},
		"negative offset":   {Offset: -1},
		"negative limit":    {Limit: -1},
		"unknown sort":      {Sort: "vibes"},
		"unknown direction": {Direction: "sideways"},
		"unknown scenario":  {Scenario: "pessimistic"},
	} {
		_, err := snapshot.View(q)
		assert.ErrorIs(t, err, draftrank.ErrInvalidQuery, name)
	}
}

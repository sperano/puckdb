package draft

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stat IDs used by the roto and points fixtures (see league-roto.xml,
// league-points.xml, league-points-missing-weight.xml).
const (
	statGoals           = 1
	statAssists         = 2
	statGoalsAgainst    = 22
	statGoalsAgainstAvg = 23
	statShotsAgainst    = 24
	statSaves           = 25
	statShotsOnGoal     = 14
	statBlocks          = 32
	rotoDraftTimeEpoch  = 1758924000
)

func parseLeague(t *testing.T, season, leagueID int, fixture string) store.League {
	t.Helper()
	content, err := resource.League{Season: season, LeagueID: leagueID}.Parse(yahoofixtures.Read(fixture))
	require.NoError(t, err)
	return content.League
}

func parseRotoLeague(t *testing.T) store.League {
	t.Helper()
	return parseLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeague)
}

func parsePointsLeague(t *testing.T, fixture string) store.League {
	t.Helper()
	return parseLeague(t, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, fixture)
}

func categoryByID(t *testing.T, categories []StatCategory, statID int) StatCategory {
	t.Helper()
	for _, c := range categories {
		if c.StatID == statID {
			return c
		}
	}
	t.Fatalf("stat %d not found", statID)
	return StatCategory{}
}

func TestFromLeague_RotoLeague(t *testing.T) {
	t.Parallel()
	rules := FromLeague(parseRotoLeague(t))

	t.Run("categories sorted by stat ID", func(t *testing.T) {
		ids := make([]int, len(rules.Categories))
		for i, c := range rules.Categories {
			ids[i] = c.StatID
		}
		assert.IsIncreasing(t, ids)
	})

	t.Run("directions", func(t *testing.T) {
		goals := categoryByID(t, rules.Categories, statGoals)
		assert.Equal(t, HigherIsBetter, goals.Direction)

		ga := categoryByID(t, rules.Categories, statGoalsAgainst)
		assert.Equal(t, LowerIsBetter, ga.Direction)

		gaa := categoryByID(t, rules.Categories, statGoalsAgainstAvg)
		assert.Equal(t, LowerIsBetter, gaa.Direction)
	})

	t.Run("display-only stats do not score", func(t *testing.T) {
		sa := categoryByID(t, rules.Categories, statShotsAgainst)
		assert.True(t, sa.DisplayOnly)
		assert.False(t, sa.Scores())

		sv := categoryByID(t, rules.Categories, statSaves)
		assert.True(t, sv.DisplayOnly)
		assert.False(t, sv.Scores())
	})

	t.Run("position types", func(t *testing.T) {
		goals := categoryByID(t, rules.Categories, statGoals)
		assert.Equal(t, []string{"P"}, goals.PositionTypes)
	})

	t.Run("game key", func(t *testing.T) {
		assert.Equal(t, yahoofixtures.RotoGameKey, rules.GameKey)
	})

	t.Run("settings map", func(t *testing.T) {
		assert.Equal(t, "1", rules.Settings["can_trade_draft_picks"])
		assert.Equal(t, "4", rules.Settings["max_weekly_adds"])
		assert.NotContains(t, rules.Settings, "persistent_url")
		assert.NotContains(t, rules.Settings, "sendbird_channel_url")
	})

	t.Run("unmodeled settings", func(t *testing.T) {
		assert.Equal(t, []string{"divisions"}, rules.UnmodeledSettings)
	})

	t.Run("draft rules", func(t *testing.T) {
		require.NotNil(t, rules.Draft.Time)
		assert.Equal(t, time.Unix(rotoDraftTimeEpoch, 0).UTC(), *rules.Draft.Time)
		assert.Equal(t, time.UTC, rules.Draft.Time.Location())
		assert.Equal(t, 90, rules.Draft.PickTimeSeconds)
	})
}

func TestFromLeague_PointsLeague(t *testing.T) {
	t.Parallel()
	rules := FromLeague(parsePointsLeague(t, yahoofixtures.PointsLeague))

	ga := categoryByID(t, rules.Categories, statGoalsAgainst)
	require.NotNil(t, ga.Weight)
	assert.Equal(t, -2.0, *ga.Weight)

	sog := categoryByID(t, rules.Categories, statShotsOnGoal)
	require.NotNil(t, sog.Weight)
	assert.Equal(t, 0.4, *sog.Weight)

	goals := categoryByID(t, rules.Categories, statGoals)
	require.Len(t, goals.Bonuses, 1)
	assert.Equal(t, Bonus{Target: "3", Points: "2"}, goals.Bonuses[0])
}

func TestStatCategories_MissingSortOrderStaysUnknown(t *testing.T) {
	t.Parallel()
	league := store.League{Settings: store.Settings{
		StatCategories: store.RosterStatCategories{Stats: store.RosterStats{Slice: []store.RosterStat{
			{StatID: statGoals, Name: "Goals", Abbr: "G"},
		}}},
	}}
	rules := FromLeague(league)
	require.Len(t, rules.Categories, 1)
	assert.Equal(t, DirectionUnknown, rules.Categories[0].Direction)
	assert.Empty(t, rules.Issues)
}

func TestStatCategories_UnknownSortOrderRecordsIssue(t *testing.T) {
	t.Parallel()
	league := store.League{Settings: store.Settings{
		StatCategories: store.RosterStatCategories{Stats: store.RosterStats{Slice: []store.RosterStat{
			{StatID: statGoals, Name: "Goals", Abbr: "G", SortOrder: "2"},
		}}},
	}}
	rules := FromLeague(league)
	require.Len(t, rules.Categories, 1)
	assert.Equal(t, DirectionUnknown, rules.Categories[0].Direction)
	require.Len(t, rules.Issues, 1)
	assert.Contains(t, rules.Issues[0], `stat 1 (G) has unknown sort_order "2"`)
}

func TestStatModifiers_UnreadableWeightRecordsIssueAndNilWeight(t *testing.T) {
	t.Parallel()
	league := store.League{Settings: store.Settings{
		StatCategories: store.RosterStatCategories{Stats: store.RosterStats{Slice: []store.RosterStat{
			{StatID: statGoals, Name: "Goals", Abbr: "G", SortOrder: "1"},
		}}},
		StatModifiers: store.StatModifiers{Stats: []store.StatModifier{
			{StatID: statGoals, Value: "abc"},
		}},
	}}
	rules := FromLeague(league)
	require.Len(t, rules.Categories, 1)
	assert.Nil(t, rules.Categories[0].Weight)
	require.Len(t, rules.Issues, 1)
	assert.Contains(t, rules.Issues[0], `stat 1 has unreadable points weight "abc"`)
}

func TestGameKeyOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		leagueKey string
		want      int
	}{
		{"numeric game key", "453.l.1003", 453},
		{"non-numeric stand-in prefix", "temporary-stand-in.l.1001", 0},
		{"empty key", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, GameKeyOf(tt.leagueKey))
		})
	}
}

func TestRules_Hash(t *testing.T) {
	t.Parallel()
	roto := FromLeague(parseRotoLeague(t))
	changed := FromLeague(parseLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeagueChanged))

	hash1, data1, err := roto.Hash()
	require.NoError(t, err)
	hash2, data2, err := roto.Hash()
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2, "hash is stable across calls despite map ordering")
	assert.Equal(t, data1, data2)

	changedHash, _, err := changed.Hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, changedHash, "changed settings must hash differently")
}

func TestRules_CrossSeasonIdentity(t *testing.T) {
	t.Parallel()
	league453 := parseRotoLeague(t)
	rules453 := FromLeague(league453)

	// The same numeric league ID reused in a later Yahoo game (game 465);
	// only the key's game prefix and the season change.
	league465 := league453
	league465.Key = "465.l.1003"
	league465.Season = yahoofixtures.PointsSeason
	rules465 := FromLeague(league465)

	assert.Equal(t, yahoofixtures.RotoLeagueID, rules453.LeagueID)
	assert.Equal(t, yahoofixtures.RotoLeagueID, rules465.LeagueID, "the numeric league ID is unchanged")
	assert.NotEqual(t, rules453.LeagueKey, rules465.LeagueKey)
	assert.NotEqual(t, rules453.GameKey, rules465.GameKey)
	assert.Equal(t, yahoofixtures.RotoGameKey, rules453.GameKey)
	assert.Equal(t, yahoofixtures.PointsGameKey, rules465.GameKey)
}

func TestDirectionFromStoredSortOrder(t *testing.T) {
	const unknownSortOrder int16 = 2
	tests := map[string]struct {
		sortOrder int16
		valid     bool
		want      Direction
	}{
		"null":    {0, false, DirectionUnknown},
		"higher":  {StoredSortOrderHigher, true, HigherIsBetter},
		"lower":   {StoredSortOrderLower, true, LowerIsBetter},
		"unknown": {unknownSortOrder, true, DirectionUnknown},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, DirectionFromStoredSortOrder(tt.sortOrder, tt.valid))
		})
	}
}

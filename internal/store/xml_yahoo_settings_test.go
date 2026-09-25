package store

import (
	"encoding/xml"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseFixture(t *testing.T, name string) FantasyContent {
	t.Helper()
	var content FantasyContent
	require.NoError(t, xml.Unmarshal(yahoofixtures.Read(name), &content))
	return content
}

func findStat(t *testing.T, stats []RosterStat, id int) RosterStat {
	t.Helper()
	for _, s := range stats {
		if s.StatID == id {
			return s
		}
	}
	t.Fatalf("stat %d not found", id)
	return RosterStat{}
}

func TestSettings_StatCategoryRules(t *testing.T) {
	t.Parallel()
	settings := parseFixture(t, yahoofixtures.RotoLeague).League.Settings
	stats := settings.StatCategories.Stats.Slice

	goals := findStat(t, stats, 1)
	assert.Equal(t, "1", goals.SortOrder)
	assert.Equal(t, "P", goals.PositionType)
	assert.Equal(t, "G", goals.DisplayName)
	assert.Zero(t, goals.IsOnlyDisplayStat)

	gaa := findStat(t, stats, 23)
	assert.Equal(t, "0", gaa.SortOrder, "GAA is lower-is-better")

	shotsAgainst := findStat(t, stats, 24)
	assert.Equal(t, 1, shotsAgainst.IsOnlyDisplayStat)
	require.Len(t, shotsAgainst.PositionTypes, 1)
	assert.Equal(t, StatPositionType{PositionType: "G", IsOnlyDisplayStat: 1}, shotsAgainst.PositionTypes[0])
}

func TestSettings_MissingSortOrderStaysUnknown(t *testing.T) {
	t.Parallel()
	input := `<settings><stat_categories><stats><stat><stat_id>1</stat_id></stat></stats></stat_categories></settings>`
	var settings Settings
	require.NoError(t, xml.Unmarshal([]byte(input), &settings))
	assert.Equal(t, "", settings.StatCategories.Stats.Slice[0].SortOrder)
}

func TestSettings_StatModifiers(t *testing.T) {
	t.Parallel()
	settings := parseFixture(t, yahoofixtures.PointsLeague).League.Settings
	mods := settings.StatModifiers.Stats
	require.NotEmpty(t, mods)
	assert.Equal(t, StatModifier{StatID: 1, Value: "3", Bonuses: []StatBonus{{Target: "3", Points: "2"}}}, mods[0])
	assert.Equal(t, "-2", mods[7].Value, "goals against carries a negative weight")

	roto := parseFixture(t, yahoofixtures.RotoLeague).League.Settings
	assert.Empty(t, roto.StatModifiers.Stats, "category leagues have no stat modifiers")
}

func TestSettings_OtherLeafSettings(t *testing.T) {
	t.Parallel()
	settings := parseFixture(t, yahoofixtures.RotoLeague).League.Settings
	leaves, nested := settings.OtherLeafSettings()

	assert.Equal(t, "1", leaves["can_trade_draft_picks"])
	assert.Equal(t, "4", leaves["max_weekly_adds"])
	assert.Equal(t, "roto", leaves["scoring_type"])
	assert.Equal(t, []string{"divisions"}, nested)
	for _, modeled := range []string{"draft_type", "roster_positions", "stat_categories", "stat_modifiers"} {
		assert.NotContains(t, leaves, modeled, "modeled elements are not repeated in Other")
	}
	assert.Equal(t, "live", settings.DraftType)
	assert.Len(t, settings.RosterPositions.Slice, 8)
}

func TestLeague_PlayersCollection(t *testing.T) {
	t.Parallel()
	league := parseFixture(t, yahoofixtures.PlayersPage).League
	assert.Equal(t, "465.l.77777", league.Key)
	require.Len(t, league.Players.Slice, yahoofixtures.PlayersInPage)

	twoWay := league.Players.Slice[0]
	assert.Equal(t, "465.p.7001", twoWay.Key)
	assert.Equal(t, []string{"C", "LW", "Util"}, twoWay.EligiblePositions)

	hurt := league.Players.Slice[3]
	assert.Equal(t, "IR", hurt.Status)
	assert.Equal(t, 1, hurt.OnDisabledList)
	assert.Contains(t, hurt.EligiblePositions, "IR+")

	assert.Empty(t, league.Players.Slice[5].EligiblePositions)
}

package mcpserver

import (
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Yahoo stat IDs and stored sort orders of the categories the tests use.
const (
	statGoals          int32 = 1
	statFaceoffsWon    int32 = 16
	statWins           int32 = 19
	statGoalsAgainst   int32 = 22
	statGAA            int32 = 23
	statSavePct        int32 = 26
	statUnsupported    int32 = 99
	sortHigher         int16 = 1
	sortLower          int16 = 0
	valuesTestTeam     int32 = 3
	valuesTestOtherTm  int32 = 5
	floatTolerance           = 1e-9
	valuesTestToString       = "2026-11-19"
)

var valuesTestTo = mustDate("2026-11-19")

func mustDate(text string) pgtype.Date {
	day, err := time.Parse(time.DateOnly, text)
	if err != nil {
		panic(err)
	}
	return pgtype.Date{Time: day, Valid: true}
}

func category(statID int32, abbr, positionType string, sortOrder int16, sortKnown bool) sqlcdb.YahooLeagueStatCategory {
	return sqlcdb.YahooLeagueStatCategory{StatID: statID, DisplayName: abbr, Name: abbr + " name", PositionType: positionType, Enabled: true,
		SortOrder: pgtype.Int2{Int16: sortOrder, Valid: sortKnown}}
}

func TestValueCategoriesMapsSupportedCategoriesInStatOrder(t *testing.T) {
	gaa := category(statGAA, "GAA", "", sortLower, true)
	display := category(statGoalsAgainst, "GA", "", sortLower, true)
	display.IsOnlyDisplayStat = true
	disabled := category(statWins, "W", "G", sortHigher, true)
	disabled.Enabled = false
	unnamed := sqlcdb.YahooLeagueStatCategory{StatID: statUnsupported, Abbr: "XYZ", Name: "Mystery", Enabled: true}
	supported, unsupported := valueCategories([]sqlcdb.YahooLeagueStatCategory{
		gaa, category(statGoals, "G", "P", sortHigher, true), display, disabled, unnamed, category(statSavePct, "SV%", "G", sortHigher, true),
	})
	assert.Equal(t, []string{"XYZ"}, unsupported)
	require.Len(t, supported, 3)
	assert.Equal(t, valueCategory{statID: statGoals, abbr: "G", name: "G name", positionType: "P",
		direction: draft.HigherIsBetter, stat: projection.StatGoals}, supported[0])
	// An empty position type comes from the projection kind.
	assert.Equal(t, valueCategory{statID: statGAA, abbr: "GAA", name: "GAA name", positionType: "G",
		direction: draft.LowerIsBetter, stat: projection.StatGoalsAgainstAvg,
		ratio: &ratioStat{numerator: projection.StatGoalsAgainst, denominator: projection.StatTOISeconds, scale: secondsPerHour}},
		supported[1])
	assert.Equal(t, &ratioStat{numerator: projection.StatSaves, denominator: projection.StatShotsAgainst, scale: 1}, supported[2].ratio)
}

func TestCategoryValue(t *testing.T) {
	gaa := valueCategory{stat: projection.StatGoalsAgainstAvg, ratio: &ratioStat{
		numerator: projection.StatGoalsAgainst, denominator: projection.StatTOISeconds, scale: secondsPerHour}}
	value, ok := gaa.value(statTotals{projection.StatGoalsAgainst: 3, projection.StatTOISeconds: 5400})
	require.True(t, ok)
	assert.InDelta(t, 2.0, value, floatTolerance)
	_, ok = gaa.value(statTotals{projection.StatGoalsAgainst: 0})
	assert.False(t, ok, "a ratio with no denominator has no value")
	goals := valueCategory{stat: projection.StatGoals}
	value, ok = goals.value(statTotals{projection.StatGoals: 7})
	assert.True(t, ok)
	assert.Equal(t, 7.0, value)
}

func playerWith(id int32, stat projection.Stat, value float64) windowPlayer {
	return windowPlayer{yahooPlayerID: id, nhlPlayerID: pgtype.Int8{Int64: int64(id), Valid: true},
		positionType: skaterPositionType, lastDate: valuesTestTo, totals: statTotals{stat: value}}
}

func TestScaleZScoresCountingCategory(t *testing.T) {
	goals := valueCategory{statID: statGoals, stat: projection.StatGoals, direction: draft.HigherIsBetter}
	// Values 2, 4, 6: mean 4, population standard deviation sqrt(8/3).
	players := []windowPlayer{playerWith(1, projection.StatGoals, 2), playerWith(2, projection.StatGoals, 4),
		playerWith(3, projection.StatGoals, 6)}
	scale := scaleOf(goals, players)
	z, ok := scale.z(goals, statTotals{projection.StatGoals: 6})
	require.True(t, ok)
	assert.InDelta(t, 2/math.Sqrt(8.0/3), z, floatTolerance)

	lower := goals
	lower.direction = draft.LowerIsBetter
	z, ok = scaleOf(lower, players).z(lower, statTotals{projection.StatGoals: 6})
	require.True(t, ok)
	assert.InDelta(t, -2/math.Sqrt(8.0/3), z, floatTolerance, "a lower-is-better category is flipped")
}

func TestScaleWithoutSpreadOrDirection(t *testing.T) {
	goals := valueCategory{statID: statGoals, stat: projection.StatGoals, direction: draft.HigherIsBetter}
	same := []windowPlayer{playerWith(1, projection.StatGoals, 3), playerWith(2, projection.StatGoals, 3)}
	z, ok := scaleOf(goals, same).z(goals, statTotals{projection.StatGoals: 5})
	assert.True(t, ok)
	assert.Equal(t, 0.0, z, "no spread scores everyone 0")

	_, ok = scaleOf(goals, same[:1]).z(goals, statTotals{projection.StatGoals: 5})
	assert.False(t, ok, "one compared player gives no z-score")

	unknown := goals
	unknown.direction = draft.DirectionUnknown
	_, ok = scaleOf(unknown, same).z(unknown, statTotals{projection.StatGoals: 5})
	assert.False(t, ok, "an unknown direction gives no z-score")
}

func TestScaleScoresRatioByVolume(t *testing.T) {
	gaa := valueCategory{statID: statGAA, stat: projection.StatGoalsAgainstAvg, direction: draft.LowerIsBetter,
		ratio: &ratioStat{numerator: projection.StatGoalsAgainst, denominator: projection.StatTOISeconds, scale: secondsPerHour}}
	goalie := func(id int32, ga, toi float64) windowPlayer {
		p := playerWith(id, projection.StatGoalsAgainst, ga)
		p.totals[projection.StatTOISeconds] = toi
		return p
	}
	// Pooled rate 6 / 10800 s; excess goals against: 0 - 2 = -2, 6 - 4 = 2,
	// so mean 0 and standard deviation 2.
	players := []windowPlayer{goalie(1, 0, 3600), goalie(2, 6, 7200)}
	scale := scaleOf(gaa, players)
	z, ok := scale.z(gaa, players[0].totals)
	require.True(t, ok)
	assert.InDelta(t, 1.0, z, floatTolerance, "fewer goals against than the pooled rate helps")
	z, ok = scale.z(gaa, statTotals{})
	require.True(t, ok)
	assert.Equal(t, 0.0, z, "no minutes scores exactly 0, not rounding noise")

	_, ok = scaleOf(gaa, []windowPlayer{goalie(1, 0, 0), goalie(2, 0, 0)}).z(gaa, statTotals{})
	assert.False(t, ok, "a comparison with no minutes gives no z-score")
}

func windowRow(team, yahooID int32, nhlID int64, positionType, last string) sqlcdb.ListYahooLeaguePlayerWindowTotalsRow {
	return sqlcdb.ListYahooLeaguePlayerWindowTotalsRow{TeamID: team, YahooPlayerID: yahooID, PositionType: positionType,
		NhlPlayerID: pgtype.Int8{Int64: nhlID, Valid: nhlID != 0}, LastDate: mustDate(last), Days: 1}
}

func TestComparisonPlayersAreTheMatchedPlayersRosteredOnTo(t *testing.T) {
	traded := windowRow(valuesTestOtherTm, 1, 101, skaterPositionType, "2026-11-01")
	traded.Goals = 2
	tradedNow := windowRow(valuesTestTeam, 1, 101, skaterPositionType, valuesTestToString)
	tradedNow.Goals = 3
	goalie := windowRow(valuesTestTeam, 2, 102, "", valuesTestToString)
	goalie.NhlPosition = nhlGoaliePosition
	rows := []sqlcdb.ListYahooLeaguePlayerWindowTotalsRow{
		traded, tradedNow, goalie,
		windowRow(valuesTestTeam, 3, 103, skaterPositionType, "2026-11-10"),     // dropped before to
		windowRow(valuesTestTeam, 4, 0, skaterPositionType, valuesTestToString), // unmatched
	}
	comparison := comparisonPlayers(rows, valuesTestTo)
	require.Len(t, comparison[skaterPositionType], 1)
	skater := comparison[skaterPositionType][0]
	assert.Equal(t, int32(1), skater.yahooPlayerID)
	assert.Equal(t, 5.0, skater.totals[projection.StatGoals], "totals sum over every team of the window")
	assert.Equal(t, 2, skater.days)
	require.Len(t, comparison[goaliePositionType], 1, "the NHL position names a goalie without a Yahoo position type")
	assert.Equal(t, int32(2), comparison[goaliePositionType][0].yahooPlayerID)
}

func TestWindowPlayerGamesFollowThePositionType(t *testing.T) {
	row := windowRow(valuesTestTeam, 1, 101, goaliePositionType, valuesTestToString)
	row.SkaterGames, row.GoalieGames = 1, 4
	assert.Equal(t, 4.0, windowPlayerOf(row).games())
	row.PositionType = skaterPositionType
	assert.Equal(t, 1.0, windowPlayerOf(row).games())
}

func TestValuePlayersRanksRosteredPlayersByValue(t *testing.T) {
	goals := valueCategory{statID: statGoals, stat: projection.StatGoals, direction: draft.HigherIsBetter}
	low, high := playerWith(1, projection.StatGoals, 1), playerWith(2, projection.StatGoals, 5)
	high.name = "High"
	dropped := playerWith(3, projection.StatGoals, 9)
	dropped.lastDate = mustDate("2026-11-01")
	comparison := []windowPlayer{low, high, playerWith(4, projection.StatGoals, 3)}
	valued := valuePlayers([]windowPlayer{dropped, low, high}, []valueCategory{goals}, comparison, nil, valuesTestTo)
	require.Len(t, valued, 3)
	assert.Equal(t, []int32{2, 1, 3}, []int32{valued[0].yahooPlayerID, valued[1].yahooPlayerID, valued[2].yahooPlayerID})
	assert.Equal(t, []int{1, 2, 0}, []int{valued[0].rank, valued[1].rank, valued[2].rank})
	assert.False(t, valued[2].onRoster)
	assert.InDelta(t, valued[0].z[statGoals], valued[0].value, floatTolerance)
	assert.Empty(t, valued[0].pace, "no projection, no pace")
}

func TestProjectedPace(t *testing.T) {
	goals := valueCategory{statID: statGoals, stat: projection.StatGoals}
	projected := statTotals{projection.StatGamesPlayed: 80, projection.StatGoals: 40,
		projection.StatSaves: 900, projection.StatShotsAgainst: 1000}
	pace, ok := projectedPace(goals, projected, 10)
	require.True(t, ok)
	assert.InDelta(t, 5.0, pace, floatTolerance)

	savePct := valueCategory{statID: statSavePct, stat: projection.StatSavePercentage,
		ratio: &ratioStat{numerator: projection.StatSaves, denominator: projection.StatShotsAgainst, scale: 1}}
	pace, ok = projectedPace(savePct, projected, 10)
	require.True(t, ok)
	assert.InDelta(t, 0.9, pace, floatTolerance, "a ratio's pace is the projected ratio")

	_, ok = projectedPace(goals, nil, 10)
	assert.False(t, ok, "an unprojected player")
	_, ok = projectedPace(valueCategory{statID: statFaceoffsWon, stat: projection.StatFaceoffsWon}, projected, 10)
	assert.False(t, ok, "a stat the projection lacks")
	_, ok = projectedPace(goals, statTotals{projection.StatGoals: 40}, 10)
	assert.False(t, ok, "no projected games")
}

func TestProjectedTotalsKeepsEachPlayersFirstKey(t *testing.T) {
	totals := projectedTotals([]sqlcdb.ListProjectionValuesByNHLPlayersRow{
		{NhlPlayerID: 7, PlayerKey: "a", Stat: string(projection.StatGoals), Mean: 10},
		{NhlPlayerID: 7, PlayerKey: "b", Stat: string(projection.StatGoals), Mean: 99},
		{NhlPlayerID: 8, PlayerKey: "c", Stat: string(projection.StatWins), Mean: 30},
	})
	assert.Equal(t, map[int64]statTotals{7: {projection.StatGoals: 10}, 8: {projection.StatWins: 30}}, totals)
}

package mcpserver

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// fixturePoolSize is the number of players in the fixture snapshot.
	fixturePoolSize = 8
	// fullPoolSize is the pool of the 12-team league the tool was sized
	// for; fullPoolBudgetBytes is the response size it must stay under,
	// well below the 100 KB an agent's context can afford.
	fullPoolSize        = 838
	fullPoolBudgetBytes = 80 * 1024
	// fullPoolGoalieShare is one goalie every that many players.
	fullPoolGoalieShare = 11
	// fullPoolInjuredShare is one injured player every that many players.
	fullPoolInjuredShare = 9
	fullPoolGameKey      = 465
	fullPoolFirstID      = 3000
)

func callDraftRankings(t *testing.T, source draftRankingsSource, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := draftRankingsTool(source, newLeagueGuard(newFakeYahooQueries(), nil))
	return callTool(t, tool.Handler, args)
}

func draftRankingsArgs(extra map[string]any) map[string]any {
	args := map[string]any{leagueArg: draftfixtures.LeagueKey, seasonYearParam.name: float64(draftfixtures.Season)}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

// headerFields splits a "# key=value ..." header into its fields.
func headerFields(t *testing.T, header string) map[string]string {
	t.Helper()
	fields := map[string]string{}
	rest := strings.TrimPrefix(header, commentPrefix)
	for rest != "" {
		key, value, found := strings.Cut(rest, "=")
		require.True(t, found, "malformed header %q", header)
		if strings.HasPrefix(value, `"`) {
			quoted, err := strconv.QuotedPrefix(value)
			require.NoError(t, err)
			unquoted, err := strconv.Unquote(quoted)
			require.NoError(t, err)
			fields[key], rest = unquoted, strings.TrimPrefix(value[len(quoted):], " ")
			continue
		}
		value, rest, _ = strings.Cut(value, " ")
		fields[key] = value
	}
	return fields
}

func TestDraftRankingsReturnsWholePoolInOneCall(t *testing.T) {
	source, snapshotID := newDraftPlayerDetailSource(t)
	result := callDraftRankings(t, source, draftRankingsArgs(nil))
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	out := parseSettingsOutput(t, text)

	header := headerFields(t, out.header)
	assert.Equal(t, draftfixtures.LeagueKey, header["league_key"])
	assert.Equal(t, draftfixtures.LeagueName, header["league"])
	assert.Equal(t, snapshotID.String(), header["snapshot_id"])
	assert.Equal(t, snapshotID.String(), header["latest_snapshot_id"])
	assert.Equal(t, string(draftrank.DefaultScenario), header["scenario"])
	assert.Equal(t, strconv.Itoa(fixturePoolSize), header["total"])
	assert.Equal(t, strconv.Itoa(fixturePoolSize), header["rows"])
	assert.Empty(t, header["next_offset"], "one call holds the whole pool")
	assert.Equal(t, "false", header["provisional"])
	assert.Equal(t, string(draft.SourceYahooAPI), header["rules_source"])
	require.Len(t, source.queries, 1)
	assert.Equal(t, maxDraftRankingsLimit, source.queries[0].Limit, "the default limit covers a whole pool")

	assert.Equal(t, [][]string{
		{"column", "stat_id", "abbr", "name", "position_types", "direction", "weight"},
		{"g", "1", "G", "Goals", "P", "", "2"},
		{"a", "2", "A", "Assists", "P", "", "1"},
		{"w", "19", "W", "Wins", "G", "", "3"},
	}, out.sections[draftCategoriesSection])
	assert.Len(t, out.sections[draftRosterSlotsSection], 1+4)
	assert.Contains(t, out.sections, draftIssuesSection)

	players := out.sections[draftPlayersSection]
	require.Len(t, players, 1+fixturePoolSize)
	assert.Equal(t, append(slices.Clone(draftPlayerColumns), "g", "a", "w"), players[0])
	assert.Equal(t, sequentialRanks(fixturePoolSize), playerColumn(players, "rank"))
	assert.NotContains(t, text, "{", "no stored JSON is returned")
	assert.NotContains(t, text, "\n\n")
}

func sequentialRanks(n int) []string {
	ranks := make([]string, n)
	for i := range ranks {
		ranks[i] = strconv.Itoa(i + 1)
	}
	return ranks
}

func playerColumn(records [][]string, column string) []string {
	i := slices.Index(records[0], column)
	values := make([]string, 0, len(records)-1)
	for _, record := range records[1:] {
		values = append(values, record[i])
	}
	return values
}

func TestDraftRankingsRowsCarryProjectedCategoriesOfTheirPositionType(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	result := callDraftRankings(t, source, draftRankingsArgs(map[string]any{draftScenarioArg: "BASELINE"}))
	require.False(t, result.IsError, resultText(t, result))
	rows := detailRowsByKey(t, parseSettingsOutput(t, resultText(t, result)).sections[draftPlayersSection])

	skater, goalie := rows[draftfixtures.CenterWing], rows[draftfixtures.StarterG]
	assert.Equal(t, "Dual Eligible", skater["name"])
	assert.Equal(t, "TOR", skater["team"])
	assert.Equal(t, "C;LW", skater["positions"])
	assert.NotEmpty(t, skater["g"])
	assert.NotEmpty(t, skater["a"])
	assert.Empty(t, skater["w"], "a skater has no goalie category")
	assert.NotEmpty(t, goalie["w"])
	assert.Empty(t, goalie["g"], "a goalie has no skater category")
	for _, row := range rows {
		assert.Equal(t, "0", row["rank_change"], "the baseline scenario never moves a player")
		assert.NotEmpty(t, row["tier"])
		assert.NotEmpty(t, row["pos_rank"])
	}
}

func TestDraftRankingsPassesTheViewToTheService(t *testing.T) {
	source, snapshotID := newDraftPlayerDetailSource(t)
	result := callDraftRankings(t, source, draftRankingsArgs(map[string]any{
		draftSnapshotIDArg: snapshotID.String(), draftScenarioArg: " Optimistic ", draftPositionsArg: []any{"c", "LW"},
		draftSearchArg: " winger ", draftSortArg: "NAME", draftDirectionArg: "Desc",
		draftOffsetParam.name: float64(1), draftLimitParam.name: "2",
	}))
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, source.queries, 1)
	assert.Equal(t, draftrank.Query{
		Scenario: draftrank.ScenarioOptimistic, Positions: []string{"c", "LW"}, Search: "winger",
		Sort: draftrank.SortName, Direction: draftrank.SortDescending, Offset: 1, Limit: 2,
	}, source.queries[0])
	assert.Equal(t, []uuid.UUID{snapshotID}, source.snapshotIDs)
}

func TestDraftRankingsPagesStayOnOneSnapshot(t *testing.T) {
	const pageSize = 3
	source, snapshotID := newDraftPlayerDetailSource(t)
	var keys, ranks []string
	args := draftRankingsArgs(map[string]any{draftLimitParam.name: float64(pageSize)})
	for page := 0; ; page++ {
		require.Less(t, page, fixturePoolSize, "paging never ends")
		result := callDraftRankings(t, source, args)
		require.False(t, result.IsError, resultText(t, result))
		out := parseSettingsOutput(t, resultText(t, result))
		header := headerFields(t, out.header)
		require.Equal(t, snapshotID.String(), header["snapshot_id"])
		keys = append(keys, playerColumn(out.sections[draftPlayersSection], "player_key")...)
		ranks = append(ranks, playerColumn(out.sections[draftPlayersSection], "rank")...)
		if header["next_offset"] == "" {
			break
		}
		next, err := strconv.Atoi(header["next_offset"])
		require.NoError(t, err)
		args[draftOffsetParam.name], args[draftSnapshotIDArg] = float64(next), header["snapshot_id"]
	}
	assert.Equal(t, sequentialRanks(fixturePoolSize), ranks, "pages neither overlap nor skip a player")
	assert.Len(t, slices.Compact(slices.Sorted(slices.Values(keys))), fixturePoolSize)
	assert.Equal(t, []uuid.UUID{uuid.Nil, snapshotID, snapshotID}, source.snapshotIDs,
		"pages after the first pin the first page's snapshot")
}

func TestDraftRankingsRejectsMalformedArgumentsBeforeReading(t *testing.T) {
	invalid := map[string]map[string]any{
		"negative offset":     {draftOffsetParam.name: float64(-1)},
		"fractional offset":   {draftOffsetParam.name: 1.5},
		"zero limit":          {draftLimitParam.name: float64(0)},
		"limit above max":     {draftLimitParam.name: float64(maxDraftRankingsLimit + 1)},
		"positions not array": {draftPositionsArg: "C"},
		"position not string": {draftPositionsArg: []any{float64(1)}},
		"sort not string":     {draftSortArg: float64(1)},
		"snapshot not UUID":   {draftSnapshotIDArg: "latest"},
		"scenario not string": {draftScenarioArg: true},
	}
	for name, extra := range invalid {
		t.Run(name, func(t *testing.T) {
			source, _ := newDraftPlayerDetailSource(t)
			result := callDraftRankings(t, source, draftRankingsArgs(extra))
			assert.True(t, result.IsError)
			assert.Empty(t, source.queries, "a malformed argument must not reach the service")
		})
	}
}

func TestDraftRankingsReportsInvalidViewValuesFromTheService(t *testing.T) {
	invalid := map[string]map[string]any{
		"scenario":  {draftScenarioArg: "pessimistic"},
		"position":  {draftPositionsArg: []any{"F"}},
		"sort":      {draftSortArg: "points"},
		"direction": {draftDirectionArg: "up"},
	}
	for name, extra := range invalid {
		t.Run(name, func(t *testing.T) {
			source, _ := newDraftPlayerDetailSource(t)
			result := callDraftRankings(t, source, draftRankingsArgs(extra))
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), draftrank.ErrInvalidQuery.Error())
		})
	}
}

func TestDraftRankingsReportsSourceErrors(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	source.err = errors.New("snapshot store unavailable")
	result := callDraftRankings(t, source, draftRankingsArgs(nil))
	assert.True(t, result.IsError)
	assert.Equal(t, source.err.Error(), resultText(t, result))
}

func TestDraftRankingsLeagueWithoutSnapshot(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	result := callDraftRankings(t, source, map[string]any{
		leagueArg: draftfixtures.OtherKey, seasonYearParam.name: float64(draftfixtures.Season),
	})
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))
	header := headerFields(t, out.header)
	assert.Equal(t, string(draftrank.StatusFailed), header["status"])
	assert.Empty(t, header["snapshot_id"])
	assert.Equal(t, "0", header["total"])
	assert.Equal(t, [][]string{{noResultsText}}, out.sections[draftPlayersSection])
	assert.Contains(t, playerColumn(out.sections[draftIssuesSection], "code"), string(draftrank.IssueRefreshFailed))
}

func TestDraftRankingsToolSchema(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	tool := draftRankingsTool(source, newLeagueGuard(newFakeYahooQueries(), nil)).Tool
	assert.ElementsMatch(t, []string{leagueArg, seasonYearParam.name}, tool.InputSchema.Required)
	enum := func(name string) any { return tool.InputSchema.Properties[name].(map[string]any)["enum"] }
	assert.ElementsMatch(t, stringValues(draftrank.Scenarios), enum(draftScenarioArg))
	assert.ElementsMatch(t, draftSortValues, enum(draftSortArg))
}

func TestDraftRankingsSortFieldsAreAllValid(t *testing.T) {
	page := draftrank.Snapshot{SnapshotInfo: draftrank.SnapshotInfo{Meta: draftrank.Meta{Scenarios: draftrank.Scenarios}}}
	for _, field := range draftSortFields {
		_, err := page.View(draftrank.Query{Sort: field})
		assert.NoError(t, err, field)
	}
}

func TestCompactFloat(t *testing.T) {
	cases := map[float64]string{
		0: "0", 245.3: "245", 1234.6: "1235", 31.24: "31", 30.0: "30", 7.11: "7.1", 2.714: "2.7", 2.0: "2",
		0.9123: "0.912", 0.1: "0.1", 0.0123456: "0.0123", 0.000012: "1e-05", -0.000012: "-1e-05", 0.00004: "4e-05", -4.567: "-4.6",
		-12.5: "-12", math.Inf(1): "+Inf",
	}
	for value, want := range cases {
		assert.Equal(t, want, compactFloat(value), "%v", value)
	}
	assert.Equal(t, "0", compactFloat(math.Copysign(0, -1)), "negative zero")
}

// TestDraftRankingsPrintsCategoryWeightsExactly checks a scoring weight is
// not rounded like the players section: it is a league rule.
func TestDraftRankingsPrintsCategoryWeightsExactly(t *testing.T) {
	page := draftrank.Page{League: draftrank.League{Categories: []draftrank.Category{
		{StatID: 1, Abbr: "G", Name: "Goals", Weight: 1.25},
		{StatID: 2, Abbr: "A", Name: "Assists", Weight: 12.5},
	}}}
	text, err := draftRankingsText(page)
	require.NoError(t, err)
	categories := parseSettingsOutput(t, text).sections[draftCategoriesSection]
	require.Len(t, categories, 3)
	weight := slices.Index(categories[0], "weight")
	assert.Equal(t, "1.25", categories[1][weight])
	assert.Equal(t, "12.5", categories[2][weight])
}

func TestDraftRankingsRejectsOversizedOutput(t *testing.T) {
	page := draftrank.Page{View: draftrank.View{Total: 1, Rows: []draftrank.Row{{Player: draftrank.Player{
		PlayerKey: draftfixtures.TopCenter, InjuryNote: strings.Repeat("x", maxDraftRankingsOutputBytes),
	}}}}}
	result, err := draftRankingsResult(page)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "page with offset and snapshot_id")
}

// TestDraftRankingsFullPoolFitsAnAgentContext renders a 12-category pool
// the size of a real 12-team league's and checks one call stays under the
// budget.
func TestDraftRankingsFullPoolFitsAnAgentContext(t *testing.T) {
	page := fullPoolPage()
	result, err := draftRankingsResult(page)
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	t.Logf("%d players: %d bytes", fullPoolSize, len(text))
	assert.Less(t, len(text), fullPoolBudgetBytes)
	assert.Len(t, parseSettingsOutput(t, text).sections[draftPlayersSection], 1+fullPoolSize)
}

var fullPoolCategories = []draftrank.Category{
	{StatID: 1, Abbr: "G", PositionTypes: []string{"P"}}, {StatID: 2, Abbr: "A", PositionTypes: []string{"P"}},
	{StatID: 4, Abbr: "+/-", PositionTypes: []string{"P"}}, {StatID: 5, Abbr: "PIM", PositionTypes: []string{"P"}},
	{StatID: 8, Abbr: "PPP", PositionTypes: []string{"P"}}, {StatID: 14, Abbr: "SOG", PositionTypes: []string{"P"}},
	{StatID: 31, Abbr: "HIT", PositionTypes: []string{"P"}}, {StatID: 32, Abbr: "BLK", PositionTypes: []string{"P"}},
	{StatID: 19, Abbr: "W", PositionTypes: []string{"G"}}, {StatID: 23, Abbr: "GAA", PositionTypes: []string{"G"}},
	{StatID: 26, Abbr: "SV%", PositionTypes: []string{"G"}}, {StatID: 27, Abbr: "SHO", PositionTypes: []string{"G"}},
}

// fullPoolNames average 14 characters, like NHL player names.
var fullPoolNames = []string{
	"Connor McDavid", "Nathan MacKinnon", "Cale Makar", "Auston Matthews", "Nikita Kucherov", "Leon Draisaitl",
	"Igor Shesterkin", "Tim Stützle", "Brady Tkachuk", "Quinn Hughes", "Jack Eichel", "Mikko Rantanen",
}

func fullPoolPage() draftrank.Page {
	rows := make([]draftrank.Row, fullPoolSize)
	for i := range rows {
		rows[i] = fullPoolRow(i)
	}
	return draftrank.Page{
		League: draftrank.League{Season: draftfixtures.Season, LeagueKey: draftfixtures.LeagueKey, Name: draftfixtures.LeagueName,
			Categories: fullPoolCategories},
		Snapshot: &draftrank.SnapshotInfo{ID: uuid.New(), AsOf: draftfixtures.AsOf},
		View:     draftrank.View{Scenario: draftrank.ScenarioBase, Total: fullPoolSize, Limit: maxDraftRankingsLimit, Rows: rows},
	}
}

// fullPoolRow is a player with realistic name, team and non-round values.
func fullPoolRow(i int) draftrank.Row {
	goalie := i%fullPoolGoalieShare == 0
	positions, positionType := []string{draft.PositionCenter, draft.PositionLeftWing}, "P"
	if goalie {
		positions, positionType = []string{draft.PositionGoalie}, "G"
	}
	scale := 1 - float64(i)/fullPoolSize
	var contributions []draftrank.Contribution
	for j, c := range fullPoolCategories {
		if slices.Contains(c.PositionTypes, positionType) {
			contributions = append(contributions, draftrank.Contribution{StatID: c.StatID, Projected: (float64(j)*13.37 + 7.123) * scale})
		}
	}
	row := draftrank.Row{
		Player: draftrank.Player{
			PlayerKey: fmt.Sprintf(yahooDraftPlayerKeyFormat, fullPoolGameKey, fullPoolFirstID+i),
			Name:      fullPoolNames[i%len(fullPoolNames)], Team: "TOR", EligiblePositions: positions,
		},
		Placement: draftrank.Placement{OverallRank: i + 1, Tier: 1 + i/100, Value: 12.3456 * scale, AdjustedValue: 11.789 * scale,
			Uncertainty: 0.1234, Contributions: contributions},
		PositionRank: 1 + i/4, RankChange: i%7 - 3,
	}
	if i%fullPoolInjuredShare == 0 {
		row.Status, row.InjuryNote = "DTD", "Upper Body"
	}
	return row
}

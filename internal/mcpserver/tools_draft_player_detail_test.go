package mcpserver

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingDraftPlayerDetailSource struct {
	*draftrank.Service
	refs        []draftrank.LeagueRef
	snapshotIDs []uuid.UUID
	queries     []draftrank.Query
	err         error
}

func (s *recordingDraftPlayerDetailSource) Rankings(ctx context.Context, ref draftrank.LeagueRef, snapshotID uuid.UUID, query draftrank.Query) (draftrank.Page, error) {
	s.refs = append(s.refs, ref)
	s.snapshotIDs = append(s.snapshotIDs, snapshotID)
	s.queries = append(s.queries, query)
	if s.err != nil {
		return draftrank.Page{}, s.err
	}
	return s.Service.Rankings(ctx, ref, snapshotID, query)
}

func newDraftPlayerDetailSource(t *testing.T) (*recordingDraftPlayerDetailSource, uuid.UUID) {
	t.Helper()
	store, snapshotID := draftLeaguesStore(t)
	return &recordingDraftPlayerDetailSource{Service: draftLeaguesService(store)}, snapshotID
}

func callDraftPlayerDetail(t *testing.T, source draftPlayerDetailSource, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := draftPlayerDetailTool(source, newLeagueGuard(newFakeYahooQueries(), nil))
	return callTool(t, tool.Handler, args)
}

func draftPlayerDetailArgs(keys ...string) map[string]any {
	items := make([]any, len(keys))
	for i, key := range keys {
		items[i] = key
	}
	return map[string]any{
		leagueArg: draftfixtures.LeagueKey, seasonYearParam.name: float64(draftfixtures.Season), draftPlayerKeysArg: items,
	}
}

func detailRowsByKey(t *testing.T, records [][]string) map[string]map[string]string {
	t.Helper()
	require.NotEmpty(t, records)
	out := make(map[string]map[string]string, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(record))
		for i, heading := range records[0] {
			row[heading] = record[i]
		}
		out[row["player_key"]] = row
	}
	return out
}

func TestDraftPlayerDetailReturnsEveryScenarioAndExplanation(t *testing.T) {
	source, snapshotID := newDraftPlayerDetailSource(t)
	args := draftPlayerDetailArgs(draftfixtures.SuspendedKey, draftfixtures.CenterWing)
	args[draftSnapshotIDArg] = snapshotID.String()
	result := callDraftPlayerDetail(t, source, args)
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))

	assert.Contains(t, out.header, "league_key="+draftfixtures.LeagueKey)
	assert.Contains(t, out.header, "snapshot_id="+snapshotID.String())
	assert.Contains(t, out.header, "players=2")
	players := detailRowsByKey(t, out.sections["players"])
	require.Len(t, players, 2)
	assert.Equal(t, "Dual Eligible", players[draftfixtures.CenterWing]["name"])
	assert.Equal(t, "C;LW", players[draftfixtures.CenterWing]["eligible_positions"])
	assert.NotEmpty(t, players[draftfixtures.SuspendedKey]["baseline_uncertainty"])

	placements := out.sections["placements"]
	require.Len(t, placements, 1+2*len(draftrank.Scenarios))
	for _, key := range []string{draftfixtures.SuspendedKey, draftfixtures.CenterWing} {
		var scenarios []string
		for _, row := range placements[1:] {
			if row[0] == key {
				scenarios = append(scenarios, row[1])
			}
		}
		assert.Equal(t, stringSlice(draftrank.Scenarios), scenarios)
	}
	assert.Greater(t, len(out.sections["contributions"]), 1)
	assert.Greater(t, len(out.sections["explanations"]), 1)
	assert.Equal(t, []draftrank.LeagueRef{{Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID, LeagueKey: draftfixtures.LeagueKey}}, source.refs)
	assert.Equal(t, []uuid.UUID{snapshotID}, source.snapshotIDs)
	assert.Equal(t, []string{draftfixtures.SuspendedKey, draftfixtures.CenterWing}, source.queries[0].PlayerKeys)
}

func stringSlice[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func TestDraftPlayerDetailFlattensNewsAdjustment(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	result := callDraftPlayerDetail(t, source, draftPlayerDetailArgs(draftfixtures.SuspendedKey))
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))

	reasons := out.sections["adjustment_reasons"]
	require.Len(t, reasons, 2)
	assert.Equal(t, "news-event:1", reasons[1][columnIndex(t, reasons[0], "event_id")])
	assert.Equal(t, "suspension", reasons[1][columnIndex(t, reasons[0], "type")])
	evidence := out.sections["adjustment_evidence"]
	require.Len(t, evidence, 2)
	assert.Equal(t, "NHL.com", evidence[1][columnIndex(t, evidence[0], "publisher")])
	assert.Equal(t, "suspended for 20 games", evidence[1][columnIndex(t, evidence[0], "quote")])
	assert.Len(t, out.sections["adjustment_effects"], 1+len(draftrank.Scenarios)-1)
	assert.Greater(t, len(out.sections["adjustment_changes"]), 1)
	assert.Equal(t, [][]string{{noResultsText}}, out.sections["adjustment_overrides"])
	assert.Greater(t, len(out.sections[draftIssuesSection]), 1, "the fixture reports stale news coverage")
}

func TestDraftPlayerDetailReasonIncludesReportedRole(t *testing.T) {
	teamID := int64(10)
	row := newDraftDetailReasonRow(draftfixtures.CenterWing, newsadjust.Reason{Role: &newsadjust.RoleChange{
		TeamID: &teamID, IceTime: newsadjust.DirectionUp, PowerPlay: newsadjust.DirectionDown,
		GoalieRole: newsadjust.GoalieRoleStarter,
	}})
	assert.Equal(t, "10", row.RoleTeamID)
	assert.Equal(t, "up", row.RoleIceTime)
	assert.Equal(t, "down", row.RolePowerPlay)
	assert.Equal(t, "starter", row.RoleGoalieRole)
}

func TestDraftPlayerDetailAcceptsNumericLeagueAndLatestSnapshot(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	args := draftPlayerDetailArgs(draftfixtures.CenterWing, draftfixtures.CenterWing)
	args[leagueArg] = strconv.Itoa(draftfixtures.LeagueID)
	result := callDraftPlayerDetail(t, source, args)
	require.False(t, result.IsError, resultText(t, result))

	require.Len(t, source.queries, 1)
	assert.Equal(t, []string{draftfixtures.CenterWing}, source.queries[0].PlayerKeys, "duplicate keys are removed")
	assert.Equal(t, uuid.Nil, source.snapshotIDs[0])
	assert.Contains(t, resultText(t, result), "players=1")
}

func TestDraftPlayerDetailValidatesPlayerKeysBeforeRanking(t *testing.T) {
	tests := map[string]struct {
		value any
		want  string
	}{
		"missing":       {nil, "player_keys is required"},
		"not array":     {draftfixtures.CenterWing, "want an array of strings"},
		"empty":         {[]any{}, "want 1 to 10 player keys"},
		"blank":         {[]any{"  "}, "want a non-empty string"},
		"non string":    {[]any{float64(1)}, "want a non-empty string"},
		"wrong marker":  {[]any{"465.x.2"}, "invalid Yahoo player key"},
		"foreign game":  {[]any{"464.p.2"}, "does not belong to game 465"},
		"zero player":   {[]any{"465.p.0"}, "does not belong to game 465"},
		"extra part":    {[]any{"465.p.2.extra"}, "invalid Yahoo player key"},
		"above maximum": {repeatPlayerKeys(maxDraftPlayerDetailPlayers + 1), "want 1 to 10 player keys"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source, _ := newDraftPlayerDetailSource(t)
			args := draftPlayerDetailArgs(draftfixtures.CenterWing)
			if test.value == nil {
				delete(args, draftPlayerKeysArg)
			} else {
				args[draftPlayerKeysArg] = test.value
			}
			result := callDraftPlayerDetail(t, source, args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), test.want)
			assert.Empty(t, source.queries)
		})
	}
}

func TestDraftDetailFloatPreservesExactValue(t *testing.T) {
	tests := map[string]struct {
		value float64
		want  string
	}{
		"fraction":      {0.123456, "0.123456"},
		"small":         {0.0000001, "1e-07"},
		"negative zero": {math.Copysign(0, -1), "-0"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			text := draftDetailFloat(test.value)
			assert.Equal(t, test.want, text)
			parsed, err := strconv.ParseFloat(text, draftDetailFloatBitSize)
			require.NoError(t, err)
			assert.Equal(t, math.Float64bits(test.value), math.Float64bits(parsed))
		})
	}
}

func repeatPlayerKeys(count int) []any {
	keys := make([]any, count)
	for i := range count {
		keys[i] = fmt.Sprintf("465.p.%d", i+1)
	}
	return keys
}

func TestDraftPlayerDetailValidatesSnapshotID(t *testing.T) {
	for name, value := range map[string]any{"not UUID": "latest", "not string": float64(1)} {
		t.Run(name, func(t *testing.T) {
			source, _ := newDraftPlayerDetailSource(t)
			args := draftPlayerDetailArgs(draftfixtures.CenterWing)
			args[draftSnapshotIDArg] = value
			result := callDraftPlayerDetail(t, source, args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "snapshot_id")
			assert.Empty(t, source.queries)
		})
	}
}

func TestDraftPlayerDetailReportsSourceErrors(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	source.err = fmt.Errorf("ranking store unavailable")
	result := callDraftPlayerDetail(t, source, draftPlayerDetailArgs(draftfixtures.CenterWing))
	assert.True(t, result.IsError)
	assert.Equal(t, "ranking store unavailable", resultText(t, result))
}

func TestDraftPlayerDetailToolSchemaBoundsPlayers(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	tool := draftPlayerDetailTool(source, newLeagueGuard(newFakeYahooQueries(), nil)).Tool
	assert.ElementsMatch(t, []string{leagueArg, seasonYearParam.name, draftPlayerKeysArg}, tool.InputSchema.Required)
	property := tool.InputSchema.Properties[draftPlayerKeysArg].(map[string]any)
	assert.Equal(t, 1, property["minItems"])
	assert.Equal(t, maxDraftPlayerDetailPlayers, property["maxItems"])
}

func TestDraftPlayerDetailSectionsStayUnique(t *testing.T) {
	source, _ := newDraftPlayerDetailSource(t)
	result := callDraftPlayerDetail(t, source, draftPlayerDetailArgs(draftfixtures.SuspendedKey))
	out := parseSettingsOutput(t, resultText(t, result))
	want := []string{
		"players", "placements", "contributions", "explanations", "adjustment_reasons", "adjustment_evidence",
		"adjustment_effects", "adjustment_changes", "adjustment_overrides", "adjustment_notes", draftIssuesSection,
	}
	assert.ElementsMatch(t, want, slices.Collect(maps.Keys(out.sections)))
	assert.NotContains(t, resultText(t, result), "{", "stored JSON is flattened into CSV sections")
	assert.NotContains(t, resultText(t, result), "\n\n")
	assert.True(t, strings.HasSuffix(resultText(t, result), "\n"))
}

func TestDraftPlayerDetailRejectsOversizedOutput(t *testing.T) {
	page := draftrank.Page{Rows: []draftrank.Row{{Player: draftrank.Player{
		PlayerKey: draftfixtures.CenterWing,
		Placements: map[draftrank.Scenario]draftrank.Placement{
			draftrank.ScenarioBaseline: {
				Scenario:     draftrank.ScenarioBaseline,
				Explanations: []string{strings.Repeat("x", maxDraftDetailOutputBytes)},
			},
		},
	}}}}
	result, err := draftPlayerDetailResult(page, []string{draftfixtures.CenterWing})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "request fewer player_keys")
}

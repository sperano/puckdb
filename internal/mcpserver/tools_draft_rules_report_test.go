package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callDraftRulesReport(t *testing.T, q *draftPoolQueries, source draftPoolSource, leagues string) string {
	t.Helper()
	tool := draftRulesReportTool(q, newLeagueGuard(q, nil), source)
	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season), draftRulesLeaguesArg: leagues,
	})
	require.False(t, result.IsError, resultText(t, result))
	return resultText(t, result)
}

func TestDraftRulesReportMatchesComparisonWriter(t *testing.T) {
	q := newDraftPoolQueries(t)
	source := newDraftPoolSource()

	got := callDraftRulesReport(t, q, source, draftfixtures.LeagueKey)
	report, err := draft.LoadLeagueReport(context.Background(), q, draftfixtures.Season,
		draftfixtures.LeagueID, source.Now(), source.StaleAfter())
	require.NoError(t, err)
	var want strings.Builder
	require.NoError(t, draft.WriteComparison(&want, []draft.LeagueReport{report}, draft.ComparisonOptions{
		GeneratedAt: source.Now(), MaxAge: source.StaleAfter(),
	}))

	assert.Equal(t, want.String(), got)
	assert.Contains(t, got, "# Yahoo league rules comparison")
	assert.Contains(t, got, "settings are stale")
}

func TestDraftRulesReportAcceptsSeasonScopedLeagueID(t *testing.T) {
	q := newDraftPoolQueries(t)
	got := callDraftRulesReport(t, q, newDraftPoolSource(), fmt.Sprint(draftfixtures.LeagueID))
	assert.Contains(t, got, fmt.Sprintf("%d league %d", draftfixtures.Season, draftfixtures.LeagueID))
}

func TestDraftRulesReportRequiresInputs(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftRulesReportTool(q, newLeagueGuard(q, nil), newDraftPoolSource())
	assert.Contains(t, tool.Tool.InputSchema.Required, "season")
	assert.Contains(t, tool.Tool.InputSchema.Required, draftRulesLeaguesArg)
	assert.Equal(t, "season is required", resultText(t, callTool(t, tool.Handler, map[string]any{
		draftRulesLeaguesArg: draftfixtures.LeagueKey,
	})))
	assert.Equal(t, "leagues is required", resultText(t, callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season),
	})))
}

func TestDraftRulesReportGuardsEveryRequestedLeague(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftRulesReportTool(q, newLeagueGuard(q, []string{draftfixtures.LeagueKey}), newDraftPoolSource())
	result := callTool(t, tool.Handler, map[string]any{
		"season":             float64(draftfixtures.Season),
		draftRulesLeaguesArg: strings.Join([]string{draftfixtures.LeagueKey, draftfixtures.OtherKey}, leagueKeyListSeparator),
	})
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("unknown league %q", draftfixtures.OtherKey), resultText(t, result))
}

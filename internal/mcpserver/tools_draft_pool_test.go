package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	draftPoolTestPlayerCount = 51
	draftPoolTestMaxAge      = 6 * time.Hour
)

type draftPoolQueries struct {
	*yahooPoolQueries
	poolRows []sqlcdb.ListYahooLeaguePlayersWithNHLRow
}

type yahooPoolQueries struct {
	*fakeYahooQueries
	latest sqlcdb.YahooLeagueRuleSnapshot
}

func (q *draftPoolQueries) GetLatestYahooLeagueRuleSnapshot(context.Context, sqlcdb.GetLatestYahooLeagueRuleSnapshotParams) (sqlcdb.YahooLeagueRuleSnapshot, error) {
	return q.latest, nil
}

func (q *draftPoolQueries) CountYahooLeagueRuleSnapshots(context.Context, sqlcdb.CountYahooLeagueRuleSnapshotsParams) (int64, error) {
	return 1, nil
}

func (q *draftPoolQueries) GetYahooOwnedTeam(context.Context, int32) (sqlcdb.YahooTeam, error) {
	return sqlcdb.YahooTeam{}, pgx.ErrNoRows
}

func (q *draftPoolQueries) ListYahooLeaguePlayersWithNHL(context.Context, string) ([]sqlcdb.ListYahooLeaguePlayersWithNHLRow, error) {
	return q.poolRows, nil
}

var _ draft.Queries = (*draftPoolQueries)(nil)

func newDraftPoolQueries(t *testing.T) *draftPoolQueries {
	t.Helper()
	rules, err := json.Marshal(draftfixtures.Rules().Rules)
	require.NoError(t, err)
	fetchedAt := pgtype.Timestamptz{Time: draftfixtures.FetchedAt, Valid: true}
	q := &draftPoolQueries{
		yahooPoolQueries: &yahooPoolQueries{
			fakeYahooQueries: newFakeYahooQueries(),
			latest: sqlcdb.YahooLeagueRuleSnapshot{
				ID: 1, Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID,
				LeagueKey: draftfixtures.LeagueKey, Source: string(draft.SourceYahooAPI),
				FetchedAt: fetchedAt, FirstSeenAt: fetchedAt, LastSeenAt: fetchedAt, Rules: rules,
			},
		},
	}
	q.leagues = []sqlcdb.YahooLeague{{ID: int32(draftfixtures.LeagueID), LeagueKey: draftfixtures.LeagueKey, Season: int32(draftfixtures.Season)}}
	for i := range draftPoolTestPlayerCount {
		q.poolRows = append(q.poolRows, sqlcdb.ListYahooLeaguePlayersWithNHLRow{
			LeagueKey: draftfixtures.LeagueKey, Season: draftfixtures.Season,
			LeagueID: draftfixtures.LeagueID, PlayerID: int32(i + 1),
			FullName: fmt.Sprintf("Pool player %d", i+1), FetchedAt: fetchedAt,
		})
	}
	return q
}

func newDraftPoolSource() *draftrank.Service {
	return draftrank.NewService(draftfixtures.NewStore(), nil, draftrank.ServiceOptions{
		Now:        func() time.Time { return draftfixtures.FetchedAt.Add(3 * draftPoolTestMaxAge) },
		StaleAfter: draftPoolTestMaxAge,
	})
}

func TestDraftPoolCoverageToolUsesBoundedPoolReport(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftPoolCoverageTool(q, newLeagueGuard(q, nil), newDraftPoolSource())
	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season), leagueArg: draftfixtures.LeagueKey,
	})
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	assert.Contains(t, text, fmt.Sprintf("## %d league %d (%s)", draftfixtures.Season, draftfixtures.LeagueID, draftfixtures.LeagueKey))
	assert.Contains(t, text, "- Players: 51")
	assert.Contains(t, text, "- ... and 1 more")
	assert.NotContains(t, text, "Pool player 51")
	assert.Contains(t, text, "player pool is stale")
}

func TestDraftPoolCoverageToolRejectsInvalidSeasonBeforeLoading(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftPoolCoverageTool(q, newLeagueGuard(q, nil), newDraftPoolSource())
	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(20252026.5), leagueArg: draftfixtures.LeagueKey,
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid season")
	assert.Zero(t, q.lookups)
}

func TestDraftPoolCoverageToolRequiresSeasonAndLeague(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftPoolCoverageTool(q, newLeagueGuard(q, nil), newDraftPoolSource())
	assert.Contains(t, tool.Tool.InputSchema.Required, "season")
	assert.Contains(t, tool.Tool.InputSchema.Required, leagueArg)
	assert.Equal(t, "season is required", resultText(t, callTool(t, tool.Handler, map[string]any{
		leagueArg: draftfixtures.LeagueKey,
	})))
	assert.Equal(t, "league is required", resultText(t, callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season),
	})))
}

func TestDraftPoolCoverageToolAcceptsSeasonScopedLeagueID(t *testing.T) {
	q := newDraftPoolQueries(t)
	tool := draftPoolCoverageTool(q, newLeagueGuard(q, []string{draftfixtures.LeagueKey}), newDraftPoolSource())
	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season), leagueArg: fmt.Sprint(draftfixtures.LeagueID),
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Contains(t, resultText(t, result), draftfixtures.LeagueKey)
}

func TestDraftPoolCoverageToolGuardsSeasonScopedLeagueIDByKey(t *testing.T) {
	store := draftfixtures.NewStore()
	rules := draftfixtures.Rules()
	rules.Rules.LeagueID = draftfixtures.OtherLeague
	rules.Rules.LeagueKey = draftfixtures.OtherKey
	store.Rules[draftfixtures.Key(draftfixtures.Season, draftfixtures.OtherLeague)] = rules
	store.Keys[draftfixtures.OtherKey] = [2]int{draftfixtures.Season, draftfixtures.OtherLeague}
	source := draftrank.NewService(store, nil, draftrank.ServiceOptions{})
	q := newDraftPoolQueries(t)
	tool := draftPoolCoverageTool(q, newLeagueGuard(q, []string{draftfixtures.LeagueKey}), source)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(draftfixtures.Season), leagueArg: fmt.Sprint(draftfixtures.OtherLeague),
	})
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("unknown league %q", fmt.Sprint(draftfixtures.OtherLeague)), resultText(t, result))
	assert.Zero(t, q.lookups, "the refused league must not reach the pool report queries")
}

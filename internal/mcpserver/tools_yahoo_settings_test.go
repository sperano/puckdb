package mcpserver

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	settingsToolName       = "get_yahoo_league_settings"
	settingsSeason   int32 = 20262027
	standInSeason    int32 = 20252026
	standInKey             = "465.l.4004"
	settingsHash           = "abc123"
)

var (
	settingsFetchedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	settingsLastSeen  = time.Date(2026, 9, 30, 8, 30, 0, 0, time.UTC)
)

// fakeSettingsQueries adds the league settings queries to fakeYahooQueries.
type fakeSettingsQueries struct {
	*fakeYahooQueries
	categories   []sqlcdb.YahooLeagueStatCategory
	slots        []sqlcdb.YahooLeagueRosterPosition
	snapshot     *sqlcdb.YahooLeagueRuleSnapshot
	snapshotArgs []sqlcdb.GetLatestYahooLeagueRuleSnapshotParams
	categoryErr  error
}

func newFakeSettingsQueries(t *testing.T) *fakeSettingsQueries {
	base := newFakeYahooQueries()
	for i := range base.leagues {
		base.leagues[i].Season = settingsSeason
		base.leagues[i].ScoringType = "head"
		base.leagues[i].NumTeams = 10
	}
	base.leagues[0].Name = "Allowed League"
	return &fakeSettingsQueries{
		fakeYahooQueries: base,
		categories: []sqlcdb.YahooLeagueStatCategory{
			{LeagueID: allowedLeagueID, StatID: 1, Name: "Goals", Abbr: "G", StatGroup: "offense", Enabled: true,
				PositionType: "P", SortOrder: pgtype.Int2{Int16: sortOrderHigherIsBetter, Valid: true},
				Value: pgtype.Float4{Float32: 1.5, Valid: true}},
			{LeagueID: allowedLeagueID, StatID: 23, Name: "Goals Against Average", Abbr: "GAA", StatGroup: "goaltending",
				Enabled: true, PositionType: "G", SortOrder: pgtype.Int2{Int16: sortOrderLowerIsBetter, Valid: true}},
			{LeagueID: allowedLeagueID, StatID: 29, Name: "Shots Against", Abbr: "SA", StatGroup: "goaltending",
				Enabled: true, PositionType: "G", IsOnlyDisplayStat: true},
		},
		slots: []sqlcdb.YahooLeagueRosterPosition{
			{LeagueID: allowedLeagueID, Position: "C", PositionType: "P", Count: 2, IsStartingPosition: true},
			{LeagueID: allowedLeagueID, Position: "BN", Count: 4},
		},
		snapshot: ruleSnapshotRow(t, draft.SourceYahooAPI, settingsSeason, allowedLeagueKey),
	}
}

// ruleSnapshotRow builds a stored rules snapshot of the allowed league.
func ruleSnapshotRow(t *testing.T, source draft.Source, sourceSeason int32, sourceKey string) *sqlcdb.YahooLeagueRuleSnapshot {
	t.Helper()
	rules, err := json.Marshal(draft.Rules{
		LeagueKey: allowedLeagueKey, ScoringType: "headpoint", NumTeams: 12,
		Issues: []string{"stat 99 (X) has unknown sort_order \"2\""},
	})
	require.NoError(t, err)
	return &sqlcdb.YahooLeagueRuleSnapshot{
		ID: 7, Season: settingsSeason, LeagueID: allowedLeagueID, LeagueKey: allowedLeagueKey,
		Source: string(source), SourceSeason: sourceSeason, SourceLeagueKey: sourceKey,
		FetchedAt:  pgtype.Timestamptz{Time: settingsFetchedAt, Valid: true},
		LastSeenAt: pgtype.Timestamptz{Time: settingsLastSeen, Valid: true},
		RulesHash:  settingsHash, Rules: rules,
	}
}

func (f *fakeSettingsQueries) GetYahooLeagueStatCategories(_ context.Context, leagueID int32) ([]sqlcdb.YahooLeagueStatCategory, error) {
	if f.categoryErr != nil {
		return nil, f.categoryErr
	}
	return f.categories, nil
}

func (f *fakeSettingsQueries) GetYahooLeagueRosterPositions(_ context.Context, leagueID int32) ([]sqlcdb.YahooLeagueRosterPosition, error) {
	return f.slots, nil
}

func (f *fakeSettingsQueries) GetLatestYahooLeagueRuleSnapshot(_ context.Context, arg sqlcdb.GetLatestYahooLeagueRuleSnapshotParams) (sqlcdb.YahooLeagueRuleSnapshot, error) {
	f.snapshotArgs = append(f.snapshotArgs, arg)
	if f.snapshot == nil {
		return sqlcdb.YahooLeagueRuleSnapshot{}, pgx.ErrNoRows
	}
	return *f.snapshot, nil
}

// callSettingsTool calls get_yahoo_league_settings for leagueID.
func callSettingsTool(t *testing.T, q *fakeSettingsQueries, leagueKeys []string, leagueID int32) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTestServer(q, leagueKeys).GetTool(settingsToolName)
	require.NotNil(t, tool)
	return callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(leagueID)})
}

// settingsOutput is a parsed get_yahoo_league_settings result.
type settingsOutput struct {
	header   string
	sections map[string][][]string
}

// parseSettingsOutput splits the header line from the "# title" CSV sections.
func parseSettingsOutput(t *testing.T, text string) settingsOutput {
	t.Helper()
	header, body, found := strings.Cut(text, "\n")
	require.True(t, found, "no sections after the header: %q", text)
	out := settingsOutput{header: header, sections: map[string][][]string{}}
	for _, part := range strings.Split(body, "\n"+commentPrefix) {
		title, rows, ok := strings.Cut(strings.TrimPrefix(part, commentPrefix), "\n")
		require.True(t, ok, "empty section %q", part)
		r := csv.NewReader(strings.NewReader(rows))
		r.FieldsPerRecord = -1
		records, err := r.ReadAll()
		require.NoError(t, err)
		out.sections[title] = records
	}
	return out
}

func TestYahooLeagueSettingsReportsCategoriesSlotsAndRules(t *testing.T) {
	q := newFakeSettingsQueries(t)
	result := callSettingsTool(t, q, []string{allowedLeagueKey}, allowedLeagueID)
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))

	assert.Equal(t, fmt.Sprintf(`# league_id=%d league_key=%s season=%d name="Allowed League" format=headpoint num_teams=12 `+
		`rules_source=yahoo_api stand_in=false rules_hash=%s fetched_at=2026-09-01T12:00:00Z last_seen_at=2026-09-30T08:30:00Z rules_issues=1`,
		allowedLeagueID, allowedLeagueKey, settingsSeason, settingsHash), out.header)
	assert.Equal(t, [][]string{
		{"stat_id", "abbr", "name", "group", "position_type", "direction", "points_weight", "enabled", "display_only"},
		{"1", "G", "Goals", "offense", "P", "higher", "1.5", "true", "false"},
		{"23", "GAA", "Goals Against Average", "goaltending", "G", "lower", "", "true", "false"},
		{"29", "SA", "Shots Against", "goaltending", "G", "", "", "true", "true"},
	}, out.sections[categoriesSection])
	assert.Equal(t, [][]string{
		{"position", "position_type", "count", "starting"},
		{"C", "P", "2", "true"},
		{"BN", "", "4", "false"},
	}, out.sections[rosterSlotsSection])
	assert.Equal(t, []sqlcdb.GetLatestYahooLeagueRuleSnapshotParams{{Season: settingsSeason, LeagueID: allowedLeagueID}},
		q.snapshotArgs, "the snapshot must be read for the league's own season")
}

func TestYahooLeagueSettingsFlagsStandInRules(t *testing.T) {
	q := newFakeSettingsQueries(t)
	q.snapshot = ruleSnapshotRow(t, draft.SourceTemporaryStandIn, standInSeason, standInKey)

	out := parseSettingsOutput(t, resultText(t, callSettingsTool(t, q, nil, allowedLeagueID)))
	assert.Contains(t, out.header, fmt.Sprintf(" rules_source=temporary_stand_in stand_in=true source_season=%d source_league_key=%s ",
		standInSeason, standInKey))
}

func TestYahooLeagueSettingsWithoutRulesSnapshot(t *testing.T) {
	q := newFakeSettingsQueries(t)
	q.snapshot = nil
	q.categories = nil
	q.slots = nil

	result := callSettingsTool(t, q, nil, allowedLeagueID)
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))
	assert.Equal(t, fmt.Sprintf(`# league_id=%d league_key=%s season=%d name="Allowed League" format=head num_teams=10 rules_snapshot=none`,
		allowedLeagueID, allowedLeagueKey, settingsSeason), out.header)
	assert.Equal(t, [][]string{{noResultsText}}, out.sections[categoriesSection])
	assert.Equal(t, [][]string{{noResultsText}}, out.sections[rosterSlotsSection])
}

// TestYahooLeagueSettingsMissingLeague pins that an unrestricted server,
// whose guard does not look leagues up, still answers a missing league with
// the guard's unknown-league error instead of empty settings.
func TestYahooLeagueSettingsMissingLeague(t *testing.T) {
	q := newFakeSettingsQueries(t)
	result := callSettingsTool(t, q, nil, missingLeagueID)
	assert.True(t, result.IsError)
	assert.Equal(t, unknownLeagueResult(missingLeagueID), result)
	assert.Empty(t, q.snapshotArgs)
}

func TestYahooLeagueSettingsQueryErrorIsToolError(t *testing.T) {
	q := newFakeSettingsQueries(t)
	q.categoryErr = errors.New("connection reset")

	result := callSettingsTool(t, q, nil, allowedLeagueID)
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("load stat categories of league %d: connection reset", allowedLeagueID), resultText(t, result))
}

func TestCategoryDirection(t *testing.T) {
	const unknownSortOrder = 2
	tests := map[string]struct {
		sortOrder pgtype.Int2
		want      draft.Direction
	}{
		"null":    {pgtype.Int2{}, draft.DirectionUnknown},
		"higher":  {pgtype.Int2{Int16: sortOrderHigherIsBetter, Valid: true}, draft.HigherIsBetter},
		"lower":   {pgtype.Int2{Int16: sortOrderLowerIsBetter, Valid: true}, draft.LowerIsBetter},
		"unknown": {pgtype.Int2{Int16: unknownSortOrder, Valid: true}, draft.DirectionUnknown},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, categoryDirection(tt.sortOrder))
		})
	}
}

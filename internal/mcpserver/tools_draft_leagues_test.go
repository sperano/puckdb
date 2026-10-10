package mcpserver

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// failedLeagueName names the fixture season's second league, whose only
	// refresh failed.
	failedLeagueName = "Failed League"
	// priorLeagueID and priorLeagueKey are a league of the season before
	// the fixture's.
	priorLeagueID  = 3003
	priorLeagueKey = "453.l.3003"
	// unimportedLeagueKey is an allowlisted key without imported rules.
	unimportedLeagueKey = "465.l.9999"
	failedRefreshError  = "the league has no draftable pool"
)

var (
	draftLeaguesNow    = draftfixtures.AsOf.Add(time.Hour)
	failedRefreshStart = draftfixtures.AsOf.Add(-time.Hour)
	failedRefreshEnd   = failedRefreshStart.Add(time.Minute)
)

// draftLeaguesStore holds three leagues: the fixture league with a served
// snapshot, a second league of the same season whose only refresh failed,
// and a league of the prior season.
func draftLeaguesStore(t *testing.T) (*draftfixtures.Store, uuid.UUID) {
	t.Helper()
	store := draftfixtures.NewStore()
	snapshotID := uuid.New()
	snapshot, err := draftfixtures.Snapshot(snapshotID, draftfixtures.AsOf)
	require.NoError(t, err)
	store.Add(snapshot)

	addLeagueRules(store, draftfixtures.Season, draftfixtures.OtherLeague, draftfixtures.OtherKey, failedLeagueName)
	store.Refreshes[draftfixtures.Key(draftfixtures.Season, draftfixtures.OtherLeague)] = &draftrank.Refresh{
		ID: uuid.New(), RunID: "run-1", State: draftrank.RefreshFailed, Code: draftrank.IssueMissingPool,
		Error: failedRefreshError, StartedAt: failedRefreshStart, FinishedAt: failedRefreshEnd,
	}
	addLeagueRules(store, draftfixtures.Season-1, priorLeagueID, priorLeagueKey, "Prior League")
	return store, snapshotID
}

func addLeagueRules(store *draftfixtures.Store, season, leagueID int, leagueKey, name string) {
	rules := draftfixtures.Rules()
	rules.Rules.Season, rules.Rules.LeagueID, rules.Rules.LeagueKey, rules.Rules.Name = season, leagueID, leagueKey, name
	store.Rules[draftfixtures.Key(season, leagueID)] = rules
	store.Keys[leagueKey] = [2]int{season, leagueID}
}

func draftLeaguesService(store *draftfixtures.Store) *draftrank.Service {
	return draftrank.NewService(store, store, draftrank.ServiceOptions{Now: func() time.Time { return draftLeaguesNow }})
}

// recordingDraftSource records what the tool asks the service for.
type recordingDraftSource struct {
	draftLeagueSource
	seasons []int
	refs    []draftrank.LeagueRef
	err     error
}

func (s *recordingDraftSource) Leagues(ctx context.Context, season int, configured []int) ([]draftrank.LeagueSummary, error) {
	s.seasons = append(s.seasons, season)
	if s.err != nil {
		return nil, s.err
	}
	return s.draftLeagueSource.Leagues(ctx, season, configured)
}

func (s *recordingDraftSource) League(ctx context.Context, ref draftrank.LeagueRef) (draftrank.LeagueSummary, error) {
	s.refs = append(s.refs, ref)
	if s.err != nil {
		return draftrank.LeagueSummary{}, s.err
	}
	return s.draftLeagueSource.League(ctx, ref)
}

func newRecordingDraftSource(t *testing.T) *recordingDraftSource {
	store, _ := draftLeaguesStore(t)
	return &recordingDraftSource{draftLeagueSource: draftLeaguesService(store)}
}

func callDraftLeagues(t *testing.T, source draftLeagueSource, leagueKeys []string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := draftLeaguesTool(source, newLeagueGuard(newFakeYahooQueries(), leagueKeys))
	return callTool(t, tool.Handler, args)
}

// rowsByLeagueKey maps each CSV row (after the header) to its league_key
// column, as a column-name → value map.
func rowsByLeagueKey(t *testing.T, records [][]string) map[string]map[string]string {
	t.Helper()
	require.NotEmpty(t, records)
	header := records[0]
	keyColumn := columnIndex(t, header, "league_key")
	rows := map[string]map[string]string{}
	for _, record := range records[1:] {
		row := map[string]string{}
		for i, name := range header {
			row[name] = record[i]
		}
		rows[record[keyColumn]] = row
	}
	return rows
}

func TestDraftLeaguesListsEveryLeagueOfTheSeason(t *testing.T) {
	store, snapshotID := draftLeaguesStore(t)
	result := callDraftLeagues(t, draftLeaguesService(store), nil, map[string]any{"season": float64(draftfixtures.Season)})
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	assert.NotContains(t, text, "\n\n", "sections follow each other without blank lines")
	out := parseSettingsOutput(t, text)

	assert.Equal(t, "# season=2026 leagues=2", out.header)
	leagues := rowsByLeagueKey(t, out.sections[draftLeaguesSection])
	require.Len(t, leagues, 2, "the prior season's league is not listed")
	assert.Equal(t, []string{
		"league_id", "league_key", "name", "num_teams", "scoring_type", "format", "provisional", "rules_source", "status",
		"snapshot_id", "snapshot_as_of", "pool_size", "scenarios", "refresh_state", "refresh_code",
		"refresh_started_at", "refresh_finished_at", "issues",
	}, out.sections[draftLeaguesSection][0])
	assert.Equal(t, strconv.Itoa(draftfixtures.LeagueID), out.sections[draftLeaguesSection][1][0], "rows are ordered by league ID")

	ready := leagues[draftfixtures.LeagueKey]
	assert.Equal(t, draftfixtures.LeagueName, ready["name"])
	assert.Equal(t, string(draftrank.StatusReady), ready["status"])
	assert.Equal(t, snapshotID.String(), ready["snapshot_id"])
	assert.Equal(t, draftfixtures.AsOf.Format(time.RFC3339), ready["snapshot_as_of"])
	assert.Equal(t, strconv.Itoa(len(draftfixtures.Pool())), ready["pool_size"])
	assert.NotEmpty(t, ready["scenarios"])
	assert.Empty(t, ready["refresh_state"], "the fixture league has no refresh row")

	failed := leagues[draftfixtures.OtherKey]
	assert.Equal(t, failedLeagueName, failed["name"])
	assert.Equal(t, string(draftrank.StatusFailed), failed["status"])
	assert.Empty(t, failed["snapshot_id"])
	assert.Empty(t, failed["pool_size"], "no snapshot, no pool size (not 0)")
	assert.Equal(t, string(draftrank.RefreshFailed), failed["refresh_state"])
	assert.Equal(t, string(draftrank.IssueMissingPool), failed["refresh_code"])
	assert.Equal(t, failedRefreshStart.Format(time.RFC3339), failed["refresh_started_at"])
	assert.Equal(t, failedRefreshEnd.Format(time.RFC3339), failed["refresh_finished_at"])
	assert.Equal(t, string(draftrank.IssueRefreshFailed), failed["issues"])

	issues := out.sections[draftIssuesSection]
	assert.Equal(t, []string{"league_key", "code", "message"}, issues[0])
	assert.Contains(t, issues, []string{draftfixtures.OtherKey, string(draftrank.IssueRefreshFailed),
		"the latest ranking refresh failed (MISSING_POOL): " + failedRefreshError})
	for _, code := range splitCell(ready["issues"]) {
		assert.Contains(t, issueCodesOf(issues, draftfixtures.LeagueKey), code, "every issue code of a row has its message")
	}
}

func splitCell(cell string) []string {
	if cell == "" {
		return nil
	}
	return strings.Split(cell, cellListSeparator)
}

func issueCodesOf(issues [][]string, leagueKey string) []string {
	var codes []string
	for _, issue := range issues[1:] {
		if issue[0] == leagueKey {
			codes = append(codes, issue[1])
		}
	}
	return codes
}

// TestDraftLeaguesReadsOnlyAllowlistedLeagues checks a restricted server
// never reads a league outside the allowlist, and skips an allowlisted key
// of another season or without imported rules.
func TestDraftLeaguesReadsOnlyAllowlistedLeagues(t *testing.T) {
	source := newRecordingDraftSource(t)
	allowed := []string{priorLeagueKey, unimportedLeagueKey, draftfixtures.LeagueKey}
	result := callDraftLeagues(t, source, allowed, map[string]any{"season": float64(draftfixtures.Season)})
	require.False(t, result.IsError, resultText(t, result))
	out := parseSettingsOutput(t, resultText(t, result))

	assert.Equal(t, "# season=2026 leagues=1", out.header)
	leagues := rowsByLeagueKey(t, out.sections[draftLeaguesSection])
	assert.Equal(t, []string{draftfixtures.LeagueKey}, slices.Collect(maps.Keys(leagues)))
	assert.Empty(t, source.seasons, "a restricted server must not list every league")
	assert.ElementsMatch(t, []draftrank.LeagueRef{
		{LeagueKey: priorLeagueKey}, {LeagueKey: unimportedLeagueKey}, {LeagueKey: draftfixtures.LeagueKey},
	}, source.refs, "only allowlisted keys are read")
	for _, issue := range out.sections[draftIssuesSection][1:] {
		assert.Equal(t, draftfixtures.LeagueKey, issue[0])
	}
}

func TestDraftLeaguesEmptySeason(t *testing.T) {
	store, _ := draftLeaguesStore(t)
	result := callDraftLeagues(t, draftLeaguesService(store), nil, map[string]any{"season": float64(draftfixtures.Season + 1)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, "# season=2027 leagues=0\n# leagues\nno results\n# issues\nno results\n", resultText(t, result))
}

func TestDraftLeaguesAcceptsStartYearOrSeasonID(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want int
	}{
		{"default", map[string]any{}, nhl.Current().StartYear()},
		{"start year", map[string]any{"season": float64(2026)}, 2026},
		{"season ID", map[string]any{"season": float64(20262027)}, 2026},
		{"season ID string", map[string]any{"season": "20252026"}, 2025},
		{"first NHL season", map[string]any{"season": float64(firstNHLSeasonID)}, firstNHLStartYear},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := newRecordingDraftSource(t)
			result := callDraftLeagues(t, source, nil, tt.args)
			require.False(t, result.IsError, resultText(t, result))
			assert.Equal(t, []int{tt.want}, source.seasons)
		})
	}
}

func TestDraftLeaguesRejectsInvalidSeasonsBeforeReading(t *testing.T) {
	for name, season := range map[string]any{
		"before the NHL":       float64(firstNHLStartYear - 1),
		"five digits":          float64(20262),
		"non-consecutive":      float64(20262028),
		"ID before the NHL":    float64(19161917),
		"fractional":           2026.5,
		"non-numeric":          "abc",
		"beyond any season ID": float64(lastSeasonID + 1),
	} {
		t.Run(name, func(t *testing.T) {
			source := newRecordingDraftSource(t)
			result := callDraftLeagues(t, source, nil, map[string]any{"season": season})
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "invalid season")
			assert.Empty(t, source.seasons)
			assert.Empty(t, source.refs)
		})
	}
}

func TestDraftLeaguesReportsServiceErrors(t *testing.T) {
	for name, leagueKeys := range map[string][]string{"unrestricted": nil, "restricted": {draftfixtures.LeagueKey}} {
		t.Run(name, func(t *testing.T) {
			source := newRecordingDraftSource(t)
			source.err = errors.New("database is down")
			result := callDraftLeagues(t, source, leagueKeys, map[string]any{})
			assert.True(t, result.IsError)
			assert.Equal(t, "database is down", resultText(t, result))
		})
	}
}

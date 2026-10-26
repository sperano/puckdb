package mcpserver

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// Override IDs of draftOverridesStore.
	activeOverrideID      = "active"
	allLeaguesOverrideID  = "all-leagues"
	expiredOverrideID     = "expired"
	resetOverrideID       = "reset"
	otherLeagueOverrideID = "other-league"
	overrideCreator       = "manager"
	overrideMissedGames   = 12
	overrideTOIPerGame    = 1080.5
)

// draftOverridesStore adds overrides to the draft leagues fixture: an
// active one of the fixture league, an active one covering every league,
// an expired and a reset one, created 1, 2, 3 and 4 hours ago in that order
// and listed out of that order, and an active one of another league,
// created now.
func draftOverridesStore(t *testing.T) *draftfixtures.Store {
	t.Helper()
	store, _ := draftLeaguesStore(t)
	created := func(hoursAgo int) time.Time { return draftLeaguesNow.Add(-time.Duration(hoursAgo) * time.Hour) }
	store.Overrides = []newsadjust.Override{
		{ID: expiredOverrideID, PlayerKey: draftfixtures.TopCenter, LeagueKey: draftfixtures.LeagueKey,
			Kind: newsadjust.OverrideMissedGames, Value: overrideMissedGames, Reason: "camp injury",
			CreatedAt: created(3), ExpiresAt: draftLeaguesNow},
		{ID: activeOverrideID, PlayerKey: draftfixtures.TopCenter, LeagueKey: draftfixtures.LeagueKey,
			Kind: newsadjust.OverrideInput, Input: newsadjust.InputTOIPerGame, Scenario: newsadjust.ScenarioBase,
			Value: overrideTOIPerGame, Reason: "top line", CreatedBy: overrideCreator, CreatedAt: created(1)},
		{ID: resetOverrideID, PlayerKey: draftfixtures.LeftWing, LeagueKey: draftfixtures.LeagueKey,
			Kind: newsadjust.OverrideExcludeEvent, EventID: "event-1", Reason: "rumor",
			CreatedAt: created(4), ResetAt: created(1), ResetReason: "confirmed"},
		{ID: allLeaguesOverrideID, PlayerKey: draftfixtures.CenterWing,
			Kind: newsadjust.OverrideMissedGames, Value: overrideMissedGames, Reason: "suspension", CreatedAt: created(2)},
		{ID: otherLeagueOverrideID, PlayerKey: draftfixtures.TopCenter, LeagueKey: draftfixtures.OtherKey,
			Kind: newsadjust.OverrideMissedGames, Value: overrideMissedGames, Reason: "other league", CreatedAt: created(0)},
	}
	return store
}

// recordingDraftOverrideSource records the filters the tool asks for.
type recordingDraftOverrideSource struct {
	*draftrank.Service
	filters []draftrank.OverrideFilter
	err     error
}

func (s *recordingDraftOverrideSource) Overrides(ctx context.Context, filter draftrank.OverrideFilter) ([]draftrank.OverrideStatus, error) {
	s.filters = append(s.filters, filter)
	if s.err != nil {
		return nil, s.err
	}
	return s.Service.Overrides(ctx, filter)
}

func newDraftOverrideSource(t *testing.T) *recordingDraftOverrideSource {
	t.Helper()
	return &recordingDraftOverrideSource{Service: draftLeaguesService(draftOverridesStore(t))}
}

func callDraftOverrides(t *testing.T, source draftOverrideSource, leagueKeys []string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := draftOverridesTool(source, newLeagueGuard(newFakeYahooQueries(), leagueKeys))
	return callTool(t, tool.Handler, args)
}

func draftOverridesArgs(extra map[string]any) map[string]any {
	args := map[string]any{leagueArg: draftfixtures.LeagueKey, seasonYearParam.name: float64(draftfixtures.Season)}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

// overrideRows calls the tool and returns its CSV header and the override
// IDs in order, with each row by ID.
func overrideRows(t *testing.T, source draftOverrideSource, args map[string]any) (header, ids []string, rows map[string]map[string]string) {
	t.Helper()
	result := callDraftOverrides(t, source, nil, args)
	require.False(t, result.IsError, resultText(t, result))
	records := parseCSV(t, resultText(t, result))
	require.NotEmpty(t, records)
	rows = make(map[string]map[string]string, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(record))
		for i, heading := range records[0] {
			row[heading] = record[i]
		}
		ids = append(ids, row["id"])
		rows[row["id"]] = row
	}
	return records[0], ids, rows
}

func TestDraftOverridesListsActiveOverridesOfTheLeagueNewestFirst(t *testing.T) {
	source := newDraftOverrideSource(t)
	header, ids, rows := overrideRows(t, source, draftOverridesArgs(nil))
	assert.Equal(t, []string{
		"id", "player_key", "league_key", "kind", "event_id", "scenario", "input", "value", "state", "reason",
		"created_by", "created_at", "expires_at", "reset_at", "reset_reason",
	}, header)
	assert.Equal(t, []string{activeOverrideID, allLeaguesOverrideID}, ids,
		"active only, the other league's override left out, newest first")
	active := rows[activeOverrideID]
	assert.Equal(t, draftfixtures.TopCenter, active["player_key"])
	assert.Equal(t, draftfixtures.LeagueKey, active["league_key"])
	assert.Equal(t, string(newsadjust.OverrideInput), active["kind"])
	assert.Equal(t, string(newsadjust.InputTOIPerGame), active["input"])
	assert.Equal(t, string(newsadjust.ScenarioBase), active["scenario"])
	assert.Equal(t, strconv.FormatFloat(overrideTOIPerGame, 'g', -1, 64), active["value"])
	assert.Equal(t, string(draftrank.OverrideActive), active["state"])
	assert.Equal(t, overrideCreator, active["created_by"])
	assert.Equal(t, draftLeaguesNow.Add(-time.Hour).Format(time.RFC3339), active["created_at"])
	assert.Empty(t, active["expires_at"], "no expiry, not the zero time")
	assert.Empty(t, active["reset_at"])
	assert.Empty(t, rows[allLeaguesOverrideID]["league_key"], "an override covering every league has no league key")

	assert.Equal(t, []draftrank.OverrideFilter{{LeagueKey: draftfixtures.LeagueKey}}, source.filters)
}

func TestDraftOverridesIncludeInactive(t *testing.T) {
	_, ids, rows := overrideRows(t, newDraftOverrideSource(t), draftOverridesArgs(map[string]any{draftIncludeInactiveArg: true}))
	assert.Equal(t, []string{activeOverrideID, allLeaguesOverrideID, expiredOverrideID, resetOverrideID}, ids)
	assert.Equal(t, string(draftrank.OverrideExpired), rows[expiredOverrideID]["state"])
	assert.Equal(t, draftLeaguesNow.Format(time.RFC3339), rows[expiredOverrideID]["expires_at"])
	reset := rows[resetOverrideID]
	assert.Equal(t, string(draftrank.OverrideReset), reset["state"])
	assert.Equal(t, "event-1", reset["event_id"])
	assert.Equal(t, "confirmed", reset["reset_reason"])
	assert.Equal(t, draftLeaguesNow.Add(-time.Hour).Format(time.RFC3339), reset["reset_at"])
}

func TestDraftOverridesFiltersByPlayer(t *testing.T) {
	_, ids, _ := overrideRows(t, newDraftOverrideSource(t), draftOverridesArgs(map[string]any{
		draftPlayerKeyArg: " " + draftfixtures.TopCenter + " ", draftIncludeInactiveArg: "true",
	}))
	assert.Equal(t, []string{activeOverrideID, expiredOverrideID}, ids)
}

func TestDraftOverridesResolvesNumericLeagueIDToItsKey(t *testing.T) {
	source := newDraftOverrideSource(t)
	_, ids, _ := overrideRows(t, source, draftOverridesArgs(map[string]any{leagueArg: strconv.Itoa(draftfixtures.LeagueID)}))
	assert.Equal(t, []string{activeOverrideID, allLeaguesOverrideID}, ids)
	require.Len(t, source.filters, 1)
	assert.Equal(t, draftfixtures.LeagueKey, source.filters[0].LeagueKey)
}

func TestDraftOverridesLimitKeepsTheNewest(t *testing.T) {
	_, ids, _ := overrideRows(t, newDraftOverrideSource(t), draftOverridesArgs(map[string]any{
		draftIncludeInactiveArg: true, draftOverridesLimitParam.name: float64(1),
	}))
	assert.Equal(t, []string{activeOverrideID}, ids)
}

func TestDraftOverridesBoundsReasons(t *testing.T) {
	store := draftOverridesStore(t)
	store.Overrides[1].Reason = strings.Repeat("long reason ", maxDraftOverrideTextRunes)
	_, ids, rows := overrideRows(t, draftLeaguesService(store), draftOverridesArgs(nil))
	require.Contains(t, ids, activeOverrideID)
	reason := rows[activeOverrideID]["reason"]
	assert.Equal(t, maxDraftOverrideTextRunes, utf8.RuneCountInString(reason))
	assert.True(t, strings.HasSuffix(reason, truncationMark))
}

func TestDraftOverridesNoResults(t *testing.T) {
	result := callDraftOverrides(t, newDraftOverrideSource(t), nil, draftOverridesArgs(map[string]any{
		draftPlayerKeyArg: draftfixtures.BackupG,
	}))
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, noResultsText, resultText(t, result))
}

// TestDraftOverridesRejectsInvalidArgumentsBeforeReading checks a bad
// argument is refused without reading overrides.
func TestDraftOverridesRejectsInvalidArgumentsBeforeReading(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"player key of another game", map[string]any{draftPlayerKeyArg: "453.p.1"}, `invalid player_key "453.p.1"`},
		{"non-canonical player key", map[string]any{draftPlayerKeyArg: "465.p.01"}, "invalid Yahoo player key"},
		{"player key not a string", map[string]any{draftPlayerKeyArg: float64(1)}, "invalid player_key 1: want a string"},
		{"include_inactive not a boolean", map[string]any{draftIncludeInactiveArg: "maybe"}, `invalid include_inactive "maybe"`},
		{"zero limit", map[string]any{draftOverridesLimitParam.name: float64(0)}, "invalid limit 0"},
		{"limit over the maximum", map[string]any{draftOverridesLimitParam.name: float64(maxDraftOverridesLimit + 1)}, "invalid limit"},
		{"missing league", map[string]any{leagueArg: " "}, "league is required"},
		{"missing season", map[string]any{seasonYearParam.name: nil}, "season"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := newDraftOverrideSource(t)
			result := callDraftOverrides(t, source, nil, draftOverridesArgs(tt.extra))
			require.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tt.want)
			assert.Empty(t, source.filters)
		})
	}
}

func TestDraftOverridesRefusesLeagueOutsideAllowlist(t *testing.T) {
	source := newDraftOverrideSource(t)
	result := callDraftOverrides(t, source, []string{draftfixtures.OtherKey}, draftOverridesArgs(nil))
	require.True(t, result.IsError)
	assert.Equal(t, `unknown league "`+draftfixtures.LeagueKey+`"`, resultText(t, result))
	assert.Empty(t, source.filters)
}

func TestDraftOverridesReportsServiceErrors(t *testing.T) {
	source := newDraftOverrideSource(t)
	source.err = errors.New("database is down")
	result := callDraftOverrides(t, source, nil, draftOverridesArgs(nil))
	require.True(t, result.IsError)
	assert.Equal(t, "database is down", resultText(t, result))

	result = callDraftOverrides(t, emptyDraftService(), nil, draftOverridesArgs(nil))
	require.True(t, result.IsError)
	assert.Equal(t, draftrank.ErrOverridesUnavailable.Error(), resultText(t, result))
}

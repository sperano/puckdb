package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeObservationsReportsReplayConditions(t *testing.T) {
	start := time.Date(2026, time.October, 4, 1, 0, 0, 0, time.UTC)
	observations := []draftwatch.Observation{
		{PolledAt: start.Add(90 * time.Second), ErrorClass: draftwatch.ErrorClassRateLimited},
		{PolledAt: start.Add(60 * time.Second), Duration: 3 * time.Second, Success: true, Changed: true, Authoritative: true, ParsedCount: 2},
		{PolledAt: start.Add(30 * time.Second), Duration: 2 * time.Second, Success: true, ParsedCount: 1},
		{PolledAt: start, ErrorClass: draftwatch.ErrorClassAuthentication},
	}

	got := summarizeObservations(observations)

	assert.Equal(t, 2, got.successes)
	assert.Equal(t, 1, got.changes)
	assert.Equal(t, 1, got.partial)
	assert.Equal(t, 1, got.authentication)
	assert.Equal(t, 1, got.rateLimited)
	assert.Equal(t, 2*time.Second, got.minimum)
	assert.Equal(t, 3*time.Second, got.median)
	assert.Equal(t, 3*time.Second, got.maximum)
	assert.Equal(t, 30*time.Second, got.maxChangeGap)
}

func TestRootExposesDraftSessionAndSyncDraft(t *testing.T) {
	root := Root()
	for _, path := range [][]string{{"sync", "draft"}, {"draft", "session", "add"}, {"draft", "session", "resolve"}} {
		command, _, err := root.Find(path)
		require.NoError(t, err)
		assert.Equal(t, path[len(path)-1], command.Name())
	}
}

func TestWriteDraftSyncOutcomeReportsSkippedPickReasons(t *testing.T) {
	var out bytes.Buffer
	outcome := draftwatch.Outcome{
		Session: draftwatch.Session{Identity: testDraftCLIIdentity()},
		Report: draftsession.Report{Skipped: []draftsession.SkippedPick{
			{Index: 1, Key: draftsession.PickKey{Round: 2, Pick: 13}, Reason: "unmapped player key"},
		}},
	}

	require.NoError(t, writeDraftSyncOutcome(&out, outcome))
	assert.Contains(t, out.String(), "skipped=1")
	assert.Contains(t, out.String(), "response_index=2 round=2 pick=13 reason=unmapped player key")
}

func testDraftCLIIdentity() draftwatch.Identity {
	return draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
}

// sessionLeagueCommands are the commands that resolve a draft session league
// through draftSessionLeagueFromFlags.
var sessionLeagueCommands = [][]string{{"sync", "draft"}, {"draft", "session", "status"}, {"draft", "session", "add"}}

func TestSessionCommandsTakeDraftLeagueAndHideTheLegacyAlias(t *testing.T) {
	for _, path := range sessionLeagueCommands {
		command, _, err := Root().Find(path)
		require.NoError(t, err)
		league := command.Flag(config.FlagDraftLeague)
		require.NotNil(t, league, "%v", path)
		assert.Empty(t, league.Deprecated, "%v", path)
		legacy := command.Flag(config.FlagDraftLegacyLeague)
		require.NotNil(t, legacy, "%v keeps --%s working", path, config.FlagDraftLegacyLeague)
		assert.True(t, legacy.Hidden, "%v", path)
		assert.NotEmpty(t, legacy.Deprecated, "%v", path)
		assert.Contains(t, command.Long, "--"+config.FlagDraftSeason, "%v explains numeric IDs", path)
	}
}

func TestDraftSessionLeagueFromParsedFlags(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"draft-league":           {[]string{"--draft-league", "500.l.5621"}, "500.l.5621"},
		"legacy alias":           {[]string{"--league", "5621"}, "5621"},
		"both naming one league": {[]string{"--league", "5621", "--draft-league", " 5621 "}, "5621"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			command, _, err := Root().Find([]string{"sync", "draft"})
			require.NoError(t, err)
			command.Flags().SetOutput(&bytes.Buffer{})
			require.NoError(t, command.ParseFlags(tc.args))
			require.NoError(t, command.PreRunE(command, nil))
			got, err := draftSessionLeagueFromFlags()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDraftSessionLeagueFromFlagsRejectsTwoDifferentLeagues(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagDraftLeague, "500.l.5621")
	viper.Set(config.FlagDraftLegacyLeague, "500.l.29249")
	_, err := draftSessionLeagueFromFlags()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name different leagues")
}

func TestWriteDraftSessionReportsTheResolvedSeason(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeDraftSession(&out, draftwatch.Session{Identity: testDraftCLIIdentity()}))
	assert.Contains(t, out.String(), "league=500.l.5621 season=2026 ")
}

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

// draftSessionLeagueFor parses args as `sync draft` does, with env (name to
// value) in the environment, and returns the session league it reads and the
// warnings it and pflag print. Tests calling it must not use
// t.Parallel: viper is process-global.
func draftSessionLeagueFor(t *testing.T, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	for name, value := range env {
		t.Setenv(name, value)
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	config.SetupViper()
	command, _, err := Root().Find([]string{"sync", "draft"})
	require.NoError(t, err)
	var warnings bytes.Buffer
	command.SetOut(&warnings)
	require.NoError(t, command.ParseFlags(args))
	require.NoError(t, command.PreRunE(command, nil))
	league, err := draftSessionLeagueFromFlags(command)
	return league, warnings.String(), err
}

func TestDraftSessionLeagueFromParsedFlags(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"draft-league":           {nil, []string{"--draft-league", "500.l.5621"}, "500.l.5621"},
		"legacy alias":           {nil, []string{"--league", "5621"}, "5621"},
		"both naming one league": {nil, []string{"--league", "5621", "--draft-league", " 5621 "}, "5621"},
		"typed alias over environment league": {map[string]string{"PUCKDB_DRAFT_LEAGUE": "500.l.29249"},
			[]string{"--league", "500.l.5621"}, "500.l.5621"},
		"typed league over environment alias": {map[string]string{"PUCKDB_LEAGUE": "500.l.29249"},
			[]string{"--draft-league", "500.l.5621"}, "500.l.5621"},
		"environment alias alone": {map[string]string{"PUCKDB_LEAGUE": "500.l.5621"}, nil, "500.l.5621"},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := draftSessionLeagueFor(t, tc.env, tc.args...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDraftSessionLeagueFromFlagsWarnsAboutTheLegacyEnvironmentVariable(t *testing.T) {
	const envWarning = "Environment variable PUCKDB_LEAGUE has been deprecated, use PUCKDB_DRAFT_LEAGUE"
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"environment alias alone": {map[string]string{"PUCKDB_LEAGUE": "500.l.5621"}, nil, envWarning},
		"typed alias":             {nil, []string{"--league", "5621"}, "Flag --league has been deprecated"},
		"typed league over environment alias": {map[string]string{"PUCKDB_LEAGUE": "500.l.29249"},
			[]string{"--draft-league", "500.l.5621"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, warnings, err := draftSessionLeagueFor(t, tc.env, tc.args...)
			require.NoError(t, err)
			if tc.want == "" {
				assert.Empty(t, warnings)
				return
			}
			assert.Contains(t, warnings, tc.want)
			if tc.want != envWarning {
				assert.NotContains(t, warnings, envWarning, "a typed alias is warned about once, by pflag")
			}
		})
	}
}

func TestDraftSessionLeagueFromFlagsRejectsTwoDifferentLeagues(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"both typed": {nil, []string{"--draft-league", "500.l.5621", "--league", "500.l.29249"},
			`--draft-league "500.l.5621" and the deprecated --league "500.l.29249" name different leagues; use --draft-league only`},
		"both in the environment": {map[string]string{
			"PUCKDB_DRAFT_LEAGUE": "500.l.5621", "PUCKDB_LEAGUE": "500.l.29249"}, nil,
			`PUCKDB_DRAFT_LEAGUE "500.l.5621" and the deprecated PUCKDB_LEAGUE "500.l.29249" name different leagues; set PUCKDB_DRAFT_LEAGUE only`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := draftSessionLeagueFor(t, tc.env, tc.args...)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestWriteDraftSessionReportsTheResolvedSeason(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeDraftSession(&out, draftwatch.Session{Identity: testDraftCLIIdentity()}))
	assert.Contains(t, out.String(), "league=500.l.5621 season=2026 ")
}

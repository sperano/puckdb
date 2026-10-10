package cmd

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// boundIntFlagValue is the value TestCommandsBindTheirFlagGroups sets on int
// flags; it differs from every int flag default.
const boundIntFlagValue = 4242

// preRunEExceptions are commands with a PreRunE that is not a plain
// bindFlagsPreRunE: metrics also binds its local port alias.
var preRunEExceptions = []string{"puckdb metrics"}

// commandFlagGroups is every command whose PreRunE is built with
// bindFlagsPreRunE, keyed by command path, with the groups it must bind.
func commandFlagGroups() map[string][]*config.FlagGroup {
	adminAuth := []*config.FlagGroup{&config.AdminAuthFlags, &config.APIBasicAuthFlags}
	slot := &config.DraftManualSlotFlags
	session := func(local ...*config.FlagGroup) []*config.FlagGroup {
		return slices.Concat(draftSessionCLIFlagGroups, local)
	}
	return map[string][]*config.FlagGroup{
		"puckdb api":                      apiFlagGroups,
		"puckdb worker":                   workerFlagGroups,
		"puckdb mcp-server":               mcpServerFlagGroups,
		"puckdb maurice":                  mauriceFlagGroups,
		"puckdb db migrate":               dbMigrateFlagGroups,
		"puckdb db force-version":         dbMigrateFlagGroups,
		"puckdb db init":                  dbInitFlagGroups,
		"puckdb db drop":                  adminAuth,
		"puckdb db provision":             dbProvisionFlagGroups,
		"puckdb db check-teams":           dbCheckTeamsFlagGroups,
		"puckdb draft rules":              draftFlagGroups,
		"puckdb draft pool":               draftFlagGroups,
		"puckdb draft rankings":           draftRankingsFlagGroups,
		"puckdb draft session status":     session(),
		"puckdb draft session capability": session(),
		"puckdb draft session add":        session(slot, &config.DraftManualPickFlags),
		"puckdb draft session correct":    session(slot, &config.DraftManualPickFlags),
		"puckdb draft session undo":       session(slot),
		"puckdb draft session resolve":    session(slot, &config.DraftResolutionFlags),
		"puckdb news report":              newsReportFlagGroups,
		"puckdb news events":              newsEventsFlagGroups,
		"puckdb news eval":                newsEvalFlagGroups,
		"puckdb redis flush":              slices.Concat([]*config.FlagGroup{&config.APIServerAddrFlags}, adminAuth),
		"puckdb sim draft":                simDraftFlagGroups,
		"puckdb sim tail":                 simTailFlagGroups,
		"puckdb sync draft":               draftSyncFlagGroups,
		"puckdb yahoo signout":            {&config.RedisFlags},
		"puckdb yahoo check-access":       yahooCheckFlagGroups,
	}
}

// commandsWithPreRunE returns every command in the tree rooted at cmd that
// has a PreRunE, keyed by command path.
func commandsWithPreRunE(cmd *cobra.Command) map[string]*cobra.Command {
	found := map[string]*cobra.Command{}
	if cmd.PreRunE != nil {
		found[cmd.CommandPath()] = cmd
	}
	for _, child := range cmd.Commands() {
		for path, c := range commandsWithPreRunE(child) {
			found[path] = c
		}
	}
	return found
}

// distinctFlagValue returns a value for f that differs from its default, so
// a viper key that reports it must be bound to f.
func distinctFlagValue(t *testing.T, f *pflag.Flag) string {
	t.Helper()
	switch f.Value.Type() {
	case "string":
		return "bound-" + f.Name
	case "int":
		return strconv.Itoa(boundIntFlagValue)
	case "bool":
		current, err := strconv.ParseBool(f.Value.String())
		require.NoError(t, err)
		return strconv.FormatBool(!current)
	}
	t.Fatalf("flag %s has unsupported type %s", f.Name, f.Value.Type())
	return ""
}

// requireGroupsBound sets every flag of groups on flags and checks viper
// reports the flag's value under its name.
func requireGroupsBound(t *testing.T, flags *pflag.FlagSet, groups []*config.FlagGroup) {
	t.Helper()
	for _, g := range groups {
		for _, def := range g.Flags {
			f := flags.Lookup(def.Name)
			require.NotNil(t, f, "flag %s is not registered", def.Name)
			require.NoError(t, flags.Set(def.Name, distinctFlagValue(t, f)))
			require.Equal(t, f.Value.String(), viper.GetString(def.Name), "flag %s is not bound", def.Name)
		}
	}
}

// TestCommandsBindTheirFlagGroups runs each command's PreRunE through the
// real command tree and checks it binds exactly the expected groups, and that
// no command with a PreRunE is missing from the table. Not parallel: viper is
// process-global.
func TestCommandsBindTheirFlagGroups(t *testing.T) {
	expected := commandFlagGroups()
	commands := commandsWithPreRunE(Root())
	for _, path := range preRunEExceptions {
		require.Contains(t, commands, path)
		delete(commands, path)
	}
	for path := range commands {
		require.Contains(t, expected, path, "command with PreRunE missing from commandFlagGroups")
	}
	for path, groups := range expected {
		t.Run(strings.TrimPrefix(path, "puckdb "), func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			cmd, ok := commands[path]
			require.True(t, ok, "command %q has no PreRunE", path)
			require.NoError(t, cmd.ParseFlags(nil))
			require.NoError(t, cmd.PreRunE(cmd, nil))
			requireGroupsBound(t, cmd.Flags(), groups)
		})
	}
}

// TestBindFlagsPreRunEReportsBindError checks the PreRunE returns the same
// error as config.BindFlags and stops at the first failing group.
func TestBindFlagsPreRunEReportsBindError(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	missing := &config.FlagGroup{Flags: []config.FlagDef{{Name: "not-registered", Default: ""}}}
	later := &config.FlagGroup{Flags: []config.FlagDef{{Name: "later", Default: "x"}}}
	cmd := &cobra.Command{Use: "test"}
	later.Init(cmd.Flags())

	err := bindFlagsPreRunE(missing, later)(cmd, nil)
	require.Error(t, err)
	require.Equal(t, config.BindFlags(cmd.Flags(), missing).Error(), err.Error())
	require.Nil(t, viper.Get("later"), "groups after the failing one must not be bound")
}

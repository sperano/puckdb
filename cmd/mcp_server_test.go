package cmd

import (
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/mcpserver"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// resolveMCPTransportFor builds the mcp-server command, parses args through
// cobra, runs the PreRunE binding step and returns the resolved transport.
// It mirrors the real command boundary so the test exercises the same
// flag → viper path that the RunE hook relies on. Tests calling this must
// not use t.Parallel: viper is process-global.
func resolveMCPTransportFor(t *testing.T, args ...string) mcpTransport {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	config.SetupViper()

	cmd := cmdMCPServer()
	require.NoError(t, cmd.ParseFlags(args))
	require.NoError(t, cmd.PreRunE(cmd, nil))
	return resolveMCPTransport()
}

// resolveMCPOptionsFor is resolveMCPTransportFor for the toolset and league
// selection. Tests calling this must not use t.Parallel either.
func resolveMCPOptionsFor(t *testing.T, args ...string) (mcpserver.Options, error) {
	t.Helper()
	resolveMCPTransportFor(t, args...)
	return resolveMCPOptions()
}

func TestMCPServerTransportDefaults(t *testing.T) {
	got := resolveMCPTransportFor(t)
	require.Equal(t, mcpTransport{Stdio: false, Port: config.DefaultMCPPort}, got)
	require.Equal(t, fmt.Sprintf(":%d", config.DefaultMCPPort), got.Addr())
}

func TestMCPServerTransportPortFlag(t *testing.T) {
	got := resolveMCPTransportFor(t, "--mcp-port", "9123")
	require.Equal(t, 9123, got.Port)
	require.False(t, got.Stdio)
}

func TestMCPServerTransportPortEnv(t *testing.T) {
	t.Setenv("PUCKDB_MCP_PORT", "9200")
	got := resolveMCPTransportFor(t)
	require.Equal(t, 9200, got.Port)
}

// TestMCPServerTransportFlagBeatsEnv pins the documented precedence: an
// explicit CLI flag wins over the environment, which wins over the default.
func TestMCPServerTransportFlagBeatsEnv(t *testing.T) {
	t.Setenv("PUCKDB_MCP_PORT", "9200")
	got := resolveMCPTransportFor(t, "--mcp-port", "9123")
	require.Equal(t, 9123, got.Port)
}

func TestMCPServerTransportStdioFlag(t *testing.T) {
	got := resolveMCPTransportFor(t, "--mcp-stdio")
	require.True(t, got.Stdio)
}

func TestMCPServerTransportStdioEnv(t *testing.T) {
	t.Setenv("PUCKDB_MCP_STDIO", "true")
	got := resolveMCPTransportFor(t)
	require.True(t, got.Stdio)
}

// TestMCPServerTransportRejectsUnparsablePortEnv guards the other silent
// port-0 path: viper coerces a non-numeric env value to 0 instead of erroring.
func TestMCPServerTransportRejectsUnparsablePortEnv(t *testing.T) {
	t.Setenv("PUCKDB_MCP_PORT", "abc")
	got := resolveMCPTransportFor(t)
	require.Equal(t, 0, got.Port)
	require.ErrorContains(t, got.Validate(), "--mcp-port 0")
}

func TestMCPServerTransportRejectsZeroPortFlag(t *testing.T) {
	got := resolveMCPTransportFor(t, "--mcp-port", "0")
	require.Error(t, got.Validate())
}

func TestMCPServerTransportStdioIgnoresPort(t *testing.T) {
	got := resolveMCPTransportFor(t, "--mcp-stdio", "--mcp-port", "0")
	require.NoError(t, got.Validate())
}

// TestMCPServerToolsetsDefaultIsNHL pins the safe default: the Yahoo league
// tools are opt-in.
func TestMCPServerToolsetsDefaultIsNHL(t *testing.T) {
	got, err := resolveMCPOptionsFor(t)
	require.NoError(t, err)
	require.Equal(t, mcpserver.Options{Toolsets: []mcpserver.Toolset{mcpserver.ToolsetNHL}}, got)
}

func TestMCPServerToolsetsFlag(t *testing.T) {
	got, err := resolveMCPOptionsFor(t, "--mcp-toolsets", "yahoo", "--mcp-yahoo-leagues", "465.l.1001, 465.l.2002")
	require.NoError(t, err)
	require.Equal(t, mcpserver.Options{
		Toolsets:     []mcpserver.Toolset{mcpserver.ToolsetYahoo},
		YahooLeagues: []string{"465.l.1001", "465.l.2002"},
	}, got)
}

func TestMCPServerToolsetsEnv(t *testing.T) {
	t.Setenv("PUCKDB_MCP_TOOLSETS", "yahoo,nhl")
	t.Setenv("PUCKDB_MCP_YAHOO_LEAGUES", "465.l.1001")
	got, err := resolveMCPOptionsFor(t)
	require.NoError(t, err)
	require.Equal(t, []mcpserver.Toolset{mcpserver.ToolsetNHL, mcpserver.ToolsetYahoo}, got.Toolsets)
	require.Equal(t, []string{"465.l.1001"}, got.YahooLeagues)
}

func TestMCPServerToolsetsRejectsUnknownToolset(t *testing.T) {
	_, err := resolveMCPOptionsFor(t, "--mcp-toolsets", "nhl,fantasy")
	require.ErrorContains(t, err, `invalid --mcp-toolsets: unknown toolset "fantasy"`)
}

func TestMCPServerToolsetsRejectsEmptyToolsets(t *testing.T) {
	_, err := resolveMCPOptionsFor(t, "--mcp-toolsets", "")
	require.ErrorContains(t, err, "invalid --mcp-toolsets: no toolset selected")
}

// TestMCPServerYahooLeaguesRejectsBareIDs guards the season ambiguity: a
// bare league ID could match another season's league.
func TestMCPServerYahooLeaguesRejectsBareIDs(t *testing.T) {
	_, err := resolveMCPOptionsFor(t, "--mcp-toolsets", "yahoo", "--mcp-yahoo-leagues", "1001")
	require.ErrorContains(t, err, `invalid --mcp-yahoo-leagues: invalid league key "1001"`)
}

// TestMCPServerYahooLeaguesWithoutYahooToolsetStarts keeps a shared
// PUCKDB_MCP_YAHOO_LEAGUES from stopping an nhl-only instance.
func TestMCPServerYahooLeaguesWithoutYahooToolsetStarts(t *testing.T) {
	t.Setenv("PUCKDB_MCP_YAHOO_LEAGUES", "465.l.1001")
	got, err := resolveMCPOptionsFor(t)
	require.NoError(t, err)
	require.False(t, got.HasToolset(mcpserver.ToolsetYahoo))
}

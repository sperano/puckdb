package cmd

import (
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/config"
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

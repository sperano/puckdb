package maurice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DefaultConfigPath ---

// DefaultConfigPath builds ~/.puckdb/maurice.yaml from os.UserHomeDir().
// Setting HOME via t.Setenv is sufficient on Linux (where the test runs);
// on macOS/Windows os.UserHomeDir reads different env vars but the
// behavior under test is purely path joining.
func TestDefaultConfigPath_UsesHomeDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-123")
	expected := filepath.Join("/tmp/test-home-123", DefaultConfigDir, DefaultConfigFile)
	assert.Equal(t, expected, DefaultConfigPath())
}

// On Unix, os.UserHomeDir errors when $HOME is empty. The function under
// test then returns "" rather than a partial path with a leading slash.
func TestDefaultConfigPath_EmptyHomeReturnsEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	assert.Equal(t, "", DefaultConfigPath())
}

// --- LoadConfig ---

// LoadConfig has three branches: missing file (silent empty config),
// valid YAML (parsed), invalid YAML (error wrap).

func TestLoadConfig_MissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cfg, err := LoadConfig(path)
	require.NoError(t, err, "missing file is treated as empty config, not an error")
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.MCPServers)
}

func TestLoadConfig_ValidYAMLParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maurice.yaml")
	yamlBody := `mcp_servers:
  - name: puckdb
    url: http://localhost:9090
    tools:
      - get_player
      - list_seasons
  - name: external
    url: http://other:9091
`
	require.NoError(t, os.WriteFile(path, []byte(yamlBody), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.MCPServers, 2)

	assert.Equal(t, "puckdb", cfg.MCPServers[0].Name)
	assert.Equal(t, "http://localhost:9090", cfg.MCPServers[0].URL)
	assert.Equal(t, []string{"get_player", "list_seasons"}, cfg.MCPServers[0].Tools)

	assert.Equal(t, "external", cfg.MCPServers[1].Name)
	// Empty Tools means "all tools" (the field is omitempty + nil).
	assert.Empty(t, cfg.MCPServers[1].Tools)
}

func TestLoadConfig_InvalidYAMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	// Indentation that the YAML parser can't reconcile.
	require.NoError(t, os.WriteFile(path, []byte("mcp_servers: [name: x\n  url: y]"), 0o600))

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "parse maurice config")
}

func TestLoadConfig_PermissionDeniedErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file mode checks")
	}
	path := filepath.Join(t.TempDir(), "no-read.yaml")
	require.NoError(t, os.WriteFile(path, []byte("mcp_servers: []"), 0o000))
	t.Cleanup(func() { os.Chmod(path, 0o600) }) // so t.TempDir cleanup can remove it

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	// Must not be the missing-file branch — the file exists but isn't readable.
	assert.Contains(t, err.Error(), "read maurice config")
}

// --- BuildMCPClient ---

// BuildMCPClient has three branches: LoadConfig error propagates, no
// servers configured falls back to noop, servers configured returns a
// MultiClient. mcp.NewClient is lazy (no dial), so the configured-servers
// path is in-process safe.

func TestBuildMCPClient_LoadConfigError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	require.NoError(t, os.WriteFile(path, []byte("mcp_servers: [{"), 0o600))

	client, err := BuildMCPClient(path)
	require.Error(t, err)
	assert.Nil(t, client)
}

func TestBuildMCPClient_NoServersFallsBackToNoop(t *testing.T) {
	// Missing config file -> LoadConfig returns empty Config -> noop client.
	path := filepath.Join(t.TempDir(), "missing.yaml")

	client, err := BuildMCPClient(path)
	require.NoError(t, err)
	require.NotNil(t, client)
	// The noop client returns no tools and a non-nil error on CallTool —
	// pin via behavior rather than reflecting the concrete type.
	tools, err := client.ListTools(t.Context())
	require.NoError(t, err)
	assert.Empty(t, tools)
}

func TestBuildMCPClient_ServersConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.yaml")
	yamlBody := `mcp_servers:
  - name: puckdb
    url: http://localhost:9090
`
	require.NoError(t, os.WriteFile(path, []byte(yamlBody), 0o600))

	client, err := BuildMCPClient(path)
	require.NoError(t, err)
	require.NotNil(t, client)
	// Don't call ListTools — that would dial the URL. The construction
	// itself is the unit under test; it must not have errored.
}

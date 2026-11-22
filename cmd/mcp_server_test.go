package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/mcpserver"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	mcpserversdk "github.com/mark3labs/mcp-go/server"
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

// resolveMCPAuthFor is resolveMCPTransportFor for the authentication
// selection. Tests calling this must not use t.Parallel either.
func resolveMCPAuthFor(t *testing.T, args ...string) (mcpAuth, error) {
	t.Helper()
	resolveMCPTransportFor(t, args...)
	return resolveMCPAuth()
}

// writeMCPAPIKeysFile writes an API keys file under t.TempDir and returns
// its path.
func writeMCPAPIKeysFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp-api-keys.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
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

const (
	importedMCPLeagueKey = "465.l.1001"
	staleMCPLeagueKey    = "500.l.1001"
)

// fakeMCPLeagueLister serves one imported league, or err.
type fakeMCPLeagueLister struct{ err error }

func (f fakeMCPLeagueLister) GetAllYahooLeagues(context.Context) ([]sqlcdb.YahooLeague, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []sqlcdb.YahooLeague{{LeagueKey: importedMCPLeagueKey}}, nil
}

func yahooMCPOptions(keys ...string) mcpserver.Options {
	return mcpserver.Options{Toolsets: []mcpserver.Toolset{mcpserver.ToolsetYahoo}, YahooLeagues: keys}
}

func TestCheckMCPLeagueKeysAcceptsImportedKey(t *testing.T) {
	t.Parallel()
	require.NoError(t, checkMCPLeagueKeys(t.Context(), fakeMCPLeagueLister{}, yahooMCPOptions(importedMCPLeagueKey)))
}

// TestCheckMCPLeagueKeysBlamesFlagForMissingKey: a key matching no imported
// league is a configuration error, reported against the flag.
func TestCheckMCPLeagueKeysBlamesFlagForMissingKey(t *testing.T) {
	t.Parallel()
	err := checkMCPLeagueKeys(t.Context(), fakeMCPLeagueLister{}, yahooMCPOptions(staleMCPLeagueKey))
	require.ErrorIs(t, err, mcpserver.ErrLeagueKeysNotImported)
	require.ErrorContains(t, err, "invalid --mcp-yahoo-leagues: configured yahoo league keys not imported: "+staleMCPLeagueKey)
}

// TestCheckMCPLeagueKeysDoesNotBlameFlagForLookupFailure: a database outage
// must not point the operator at --mcp-yahoo-leagues.
func TestCheckMCPLeagueKeysDoesNotBlameFlagForLookupFailure(t *testing.T) {
	t.Parallel()
	queryErr := errors.New("connection refused")
	err := checkMCPLeagueKeys(t.Context(), fakeMCPLeagueLister{err: queryErr}, yahooMCPOptions(importedMCPLeagueKey))
	require.ErrorIs(t, err, queryErr)
	require.EqualError(t, err, "list yahoo leagues: connection refused")
}

// TestMCPServerAuthDefaultRefusesEverything pins the fail-closed default:
// with neither --mcp-api-keys-file nor --mcp-allow-unauthenticated set, the
// command still starts but its APIKeys has no entries, so RequireAPIKey
// refuses every request.
func TestMCPServerAuthDefaultRefusesEverything(t *testing.T) {
	auth, err := resolveMCPAuthFor(t)
	require.NoError(t, err)
	require.False(t, auth.AllowUnauthenticated)
	require.Equal(t, 0, auth.Keys.Len())
}

func TestMCPServerAuthAllowUnauthenticatedFlag(t *testing.T) {
	auth, err := resolveMCPAuthFor(t, "--mcp-allow-unauthenticated")
	require.NoError(t, err)
	require.True(t, auth.AllowUnauthenticated)
}

func TestMCPServerAuthAPIKeysFileFlag(t *testing.T) {
	path := writeMCPAPIKeysFile(t, "kelos: key-one\n")
	auth, err := resolveMCPAuthFor(t, "--mcp-api-keys-file", path)
	require.NoError(t, err)
	require.False(t, auth.AllowUnauthenticated)
	require.Equal(t, 1, auth.Keys.Len())
	_, ok := auth.Keys.Lookup("key-one")
	require.True(t, ok)
}

func TestMCPServerAuthRejectsBothKeysFileAndAllowUnauthenticated(t *testing.T) {
	path := writeMCPAPIKeysFile(t, "kelos: key-one\n")
	_, err := resolveMCPAuthFor(t, "--mcp-api-keys-file", path, "--mcp-allow-unauthenticated")
	require.ErrorContains(t, err, "--mcp-api-keys-file and --mcp-allow-unauthenticated are mutually exclusive")
}

func TestMCPServerAuthRejectsMalformedKeysFile(t *testing.T) {
	path := writeMCPAPIKeysFile(t, "")
	_, err := resolveMCPAuthFor(t, "--mcp-api-keys-file", path)
	require.ErrorContains(t, err, "invalid --mcp-api-keys-file")
	require.ErrorContains(t, err, "no client keys listed")
}

func TestMCPServerAuthRejectsMissingKeysFile(t *testing.T) {
	_, err := resolveMCPAuthFor(t, "--mcp-api-keys-file", filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "invalid --mcp-api-keys-file")
}

// TestMCPServerAuthStdioSkipsResolution pins that the stdio transport, which
// has no HTTP listener, never runs the HTTP auth flag validation: even a
// mutually-exclusive flag combination that would fail resolveMCPAuth outright
// must not fail the stdio run.
func TestMCPServerAuthStdioSkipsResolution(t *testing.T) {
	path := writeMCPAPIKeysFile(t, "kelos: key-one\n")
	resolveMCPTransportFor(t, "--mcp-api-keys-file", path, "--mcp-allow-unauthenticated")

	auth, err := resolveMCPAuthForTransport(mcpTransport{Stdio: true})
	require.NoError(t, err)
	require.Equal(t, mcpAuth{}, auth)
}

// TestMCPServerAuthStdioWarnsNothing pins that the missing-auth-flag startup
// warning, which only makes sense for the HTTP endpoint, never fires for the
// stdio transport.
func TestMCPServerAuthStdioWarnsNothing(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = previous })

	_, err := resolveMCPAuthForTransport(mcpTransport{Stdio: true})
	require.NoError(t, err)
	require.Empty(t, buf.Bytes())
}

// TestMCPServerAuthHTTPStillWarns pins that the HTTP transport keeps the
// missing-auth-flag startup warning resolveMCPAuthForTransport now gates.
func TestMCPServerAuthHTTPStillWarns(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = previous })
	viper.Reset()
	t.Cleanup(viper.Reset)
	config.SetupViper()

	_, err := resolveMCPAuthForTransport(mcpTransport{Stdio: false})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "--mcp-api-keys-file")
	require.Contains(t, buf.String(), "--mcp-allow-unauthenticated")
}

// requireMCPHTTPRequest posts an empty body to the server newMCPHTTPServer
// builds and reports the response status, so the auth wrapping and mounting
// in newMCPHTTPServer itself — not RequireAPIKey alone — are under test.
// requireMCPHTTPRequest starts the server newMCPHTTPServer builds on a real
// listener (StreamableHTTPServer.ServeHTTP ignores the preset http.Server's
// Handler, so the auth wrapping only runs through the Start/ListenAndServe
// path the production serveMCPHTTP uses, not a direct ServeHTTP call) and
// reports the response status to one POST.
func requireMCPHTTPRequest(t *testing.T, auth mcpAuth, authHeader string) int {
	t.Helper()
	srv := mcpserversdk.NewMCPServer("test", "0.0.1")
	httpSrv := newMCPHTTPServer(srv, auth)
	addr := reservePort(t)

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Start(addr) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), runHTTPServerTestTimeout)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		<-errCh
	})
	waitForListening(t, addr)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+mcpEndpointPath, bytes.NewReader(nil))
	require.NoError(t, err)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestMCPHTTPServerRefusesWithoutHeader pins that newMCPHTTPServer wraps the
// MCP handler in the auth middleware by default: an unconfigured request
// never reaches the MCP handler.
func TestMCPHTTPServerRefusesWithoutHeader(t *testing.T) {
	status := requireMCPHTTPRequest(t, mcpAuth{}, "")
	require.Equal(t, http.StatusUnauthorized, status)
}

// TestMCPHTTPServerAcceptsConfiguredKey pins that a request bearing a
// configured key passes newMCPHTTPServer's wrapping and reaches the MCP
// handler (which answers something other than 401 to a malformed MCP body).
func TestMCPHTTPServerAcceptsConfiguredKey(t *testing.T) {
	keys, err := mcpserver.ParseAPIKeys([]byte("kelos: key-one\n"))
	require.NoError(t, err)
	status := requireMCPHTTPRequest(t, mcpAuth{Keys: keys}, "Bearer key-one")
	require.NotEqual(t, http.StatusUnauthorized, status)
}

// TestMCPHTTPServerAllowUnauthenticatedSkipsWrapping pins that
// AllowUnauthenticated bypasses RequireAPIKey entirely: a request with no
// header at all is not refused with 401.
func TestMCPHTTPServerAllowUnauthenticatedSkipsWrapping(t *testing.T) {
	status := requireMCPHTTPRequest(t, mcpAuth{AllowUnauthenticated: true}, "")
	require.NotEqual(t, http.StatusUnauthorized, status)
}

// TestMCPHTTPServerWithNoKeysRefusesEverything pins the fail-closed default:
// a zero-value mcpAuth (no --mcp-api-keys-file configured) refuses even a
// request carrying some bearer token, through the full newMCPHTTPServer
// wiring.
func TestMCPHTTPServerWithNoKeysRefusesEverything(t *testing.T) {
	status := requireMCPHTTPRequest(t, mcpAuth{}, "Bearer anything")
	require.Equal(t, http.StatusUnauthorized, status)
}

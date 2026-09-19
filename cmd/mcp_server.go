package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/mcpserver"

	mcpserversdk "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// mcpServerShutdownTimeout bounds graceful shutdown of the MCP HTTP server
// after the command context is canceled.
const mcpServerShutdownTimeout = 10 * time.Second

// mcpServerFlagGroups lists every flag group the mcp-server command
// exposes. Defined once and shared by InitFlags and BindFlags so the two
// can never drift.
var mcpServerFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.MCPServerFlags,
}

// mcpTransport is the resolved transport selection for the MCP server.
type mcpTransport struct {
	Stdio bool
	Port  int
}

// Addr returns the HTTP listen address; meaningless when Stdio is set.
func (t mcpTransport) Addr() string {
	return fmt.Sprintf(":%d", t.Port)
}

// Validate rejects an HTTP transport without a usable port. viper coerces an
// unparsable PUCKDB_MCP_PORT to 0, which net.Listen would silently turn into
// an ephemeral port — the same failure shape the flag binding fix removed.
func (t mcpTransport) Validate() error {
	if !t.Stdio && t.Port <= 0 {
		return fmt.Errorf("invalid --%s %d: must be a positive port", config.FlagMCPPort, t.Port)
	}
	return nil
}

func cmdMCPServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp-server",
		Short: "Start the PuckDB MCP server",
		Long:  "Serve curated NHL data tools via the Model Context Protocol (MCP)",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), mcpServerFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			transport := resolveMCPTransport()
			if err := transport.Validate(); err != nil {
				return err
			}
			return runMCPServer(cmd, transport)
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags, mcpServerFlagGroups...)
	config.InitLoggingFlags(flags, config.LogLevelInfo, config.DefaultLogFile)
	return cmd
}

// resolveMCPTransport reads the transport selection through viper so the
// flag default, PUCKDB_MCP_* environment variables and explicit CLI flags
// all follow the same precedence as every other command. It must run after
// the command's flags have been bound (PreRunE).
func resolveMCPTransport() mcpTransport {
	return mcpTransport{
		Stdio: viper.GetBool(config.FlagMCPStdio),
		Port:  viper.GetInt(config.FlagMCPPort),
	}
}

func runMCPServer(cmd *cobra.Command, transport mcpTransport) error {
	ctx := cmd.Context()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	queries := database.NewQueries(pool)
	srv := mcpserver.NewServer(queries)

	if transport.Stdio {
		log.Info().Msg("Starting PuckDB MCP server (stdio)")
		return mcpserversdk.ServeStdio(srv)
	}

	httpSrv := mcpserversdk.NewStreamableHTTPServer(srv)
	log.Info().Int("port", transport.Port).Msg("Starting PuckDB MCP server (HTTP)")
	return serveMCPHTTP(ctx, httpSrv, transport.Addr())
}

// serveMCPHTTP runs httpSrv until ctx is canceled, then shuts it down
// gracefully, bounded by mcpServerShutdownTimeout. Mirrors runHTTPServer:
// mcp-go's Start has no signal or context handling of its own (unlike
// ServeStdio, which installs its own SIGINT/SIGTERM handler), so without
// this the command would ignore the root signal context's cancellation.
func serveMCPHTTP(ctx context.Context, httpSrv *mcpserversdk.StreamableHTTPServer, addr string) error {
	errCh := make(chan error, 1)
	go func() {
		err := httpSrv.Start(addr)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), mcpServerShutdownTimeout)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return <-errCh
	}
}

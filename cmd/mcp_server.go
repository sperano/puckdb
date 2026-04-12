package cmd

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/mcpserver"

	mcpserversdk "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultMCPServerPort = 8790
	flagMCPServerPort    = "mcp-port"
	flagMCPServerStdio   = "stdio"
)

func cmdMCPServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp-server",
		Short: "Start the PuckDB MCP server",
		Long:  "Serve curated NHL data tools via the Model Context Protocol (MCP)",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(),
				&config.PostgresFlags,
			)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServer(cmd)
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags, &config.PostgresFlags)
	config.InitLoggingFlags(flags, config.LogLevelInfo, config.DefaultLogFile)
	flags.Int(flagMCPServerPort, defaultMCPServerPort, "MCP server HTTP port")
	flags.Bool(flagMCPServerStdio, false, "Serve over stdio instead of HTTP")
	return cmd
}

func runMCPServer(cmd *cobra.Command) error {
	ctx := cmd.Context()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	queries := database.NewQueries(pool)
	srv := mcpserver.NewServer(queries)

	stdio, _ := cmd.Flags().GetBool(flagMCPServerStdio)
	if stdio {
		log.Info().Msg("Starting PuckDB MCP server (stdio)")
		return mcpserversdk.ServeStdio(srv)
	}

	port := viper.GetInt(flagMCPServerPort)
	addr := fmt.Sprintf(":%d", port)

	httpSrv := mcpserversdk.NewStreamableHTTPServer(srv)
	log.Info().Int("port", port).Msg("Starting PuckDB MCP server (HTTP)")
	return httpSrv.Start(addr)
}

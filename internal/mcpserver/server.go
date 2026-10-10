package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	serverVersion = "0.1.0"
	// defaultResultLimit caps the rows of tools that take an optional limit.
	defaultResultLimit = 100
)

// NewServer creates an MCP server that exposes the curated puckdb read
// queries of the selected toolsets as tools. Its name follows the toolsets
// (see ServerName).
func NewServer(queries *sqlcdb.Queries, opts Options) (*server.MCPServer, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	hooks := &server.Hooks{}
	hooks.AddBeforeCallTool(func(_ context.Context, _ any, msg *mcp.CallToolRequest) {
		log.Debug().
			Str("tool", msg.Params.Name).
			Any("args", msg.Params.Arguments).
			Msg("mcp tool call")
	})
	hooks.AddAfterCallTool(func(_ context.Context, _ any, msg *mcp.CallToolRequest, _ any) {
		log.Debug().
			Str("tool", msg.Params.Name).
			Msg("mcp tool call complete")
	})
	hooks.AddOnError(func(_ context.Context, _ any, method mcp.MCPMethod, _ any, err error) {
		log.Error().
			Str("method", string(method)).
			Err(err).
			Msg("mcp error")
	})

	srv := server.NewMCPServer(ServerName(opts.Toolsets), serverVersion,
		server.WithToolCapabilities(false),
		server.WithHooks(hooks),
	)
	registerToolsets(srv, queries, opts)
	return srv, nil
}

func registerToolsets(srv *server.MCPServer, queries *sqlcdb.Queries, opts Options) {
	for _, ts := range opts.Toolsets {
		switch ts {
		case ToolsetNHL:
			registerNHLTools(srv, queries)
		case ToolsetYahoo:
			registerYahooTools(srv, queries, newLeagueGuard(queries, opts.YahooLeagues))
		}
	}
}

// registerNHLTools adds the public NHL data tools.
func registerNHLTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	registerResolveTools(srv, queries)
	registerPlayerTools(srv, queries)
	registerGameTools(srv, queries)
	registerStatsTools(srv, queries)
	registerStandingsTools(srv, queries)
	registerSeasonStatsTools(srv, queries)
	registerEdgeTools(srv, queries)
	registerPlayoffTools(srv, queries)
	registerPlayEventTools(srv, queries)
	registerLinemateTools(srv, queries)
}

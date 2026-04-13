package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/sqlcdb"
)

const (
	serverName    = "puckdb"
	serverVersion = "0.1.0"
)

// NewServer creates an MCP server that exposes curated puckdb read queries as tools.
func NewServer(queries *sqlcdb.Queries) *server.MCPServer {
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

	srv := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(false),
		server.WithHooks(hooks),
	)
	registerAll(srv, queries)
	return srv
}

func registerAll(srv *server.MCPServer, queries *sqlcdb.Queries) {
	registerResolveTools(srv, queries)
	registerPlayerTools(srv, queries)
	registerGameTools(srv, queries)
	registerStatsTools(srv, queries)
	registerStandingsTools(srv, queries)
	registerFantasyTools(srv, queries)
}

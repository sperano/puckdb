package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

const (
	serverName    = "puckdb"
	serverVersion = "0.1.0"
)

// NewServer creates an MCP server that exposes curated puckdb read queries as tools.
func NewServer(queries *sqlcdb.Queries) *server.MCPServer {
	srv := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(false),
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

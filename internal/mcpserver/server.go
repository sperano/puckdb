// Package mcpserver exposes curated, read-only puckdb queries as MCP tools
// for Maurice and other MCP clients (the `mcp-server` command), in three
// toolsets (Toolset, toolsets.go): nhl (public NHL data, the default),
// yahoo (private Yahoo league data, league-scoped by leagueGuard in
// league_guard.go) and news (news coverage and quoted player news). A
// server instance registers one or more toolsets; registerToolsets and
// registerNHLTools below, and registerYahooTools in tools_yahoo.go, are
// the authoritative map from toolset to tool — check them, not a file
// name, when adding or looking up a tool.
//
// A tools_*.go file does not reliably say which toolset, or even which
// other file, registers its tools: several are builder-only files whose
// server.ServerTool values are folded into a same-area file's registration
// list rather than registered on their own, e.g. tools_game_summary.go and
// tools_game_log.go into tools_games.go/tools_stats.go, tools_player_lookup.go
// into tools_players.go, tools_edge_leaders.go/tools_edge_metrics.go into
// tools_edge.go, and every tools_yahoo_*.go/tools_draft_*.go file into
// tools_yahoo.go's single registerYahooTools. Two names are easy to swap:
// tools_stats.go (box-score and bulk season-total queries) vs.
// tools_season_stats.go (sorted, filtered season leaderboards) — both nhl.
// tools_news.go (nhl toolset: "get_recent_news_events", a short validated
// headline list) is not part of the news toolset; the news toolset's tools
// are tools_news_coverage.go (source freshness) and tools_player_news.go
// (quoted articles for one player). tools_common.go, format.go and
// draft_player_keys.go hold helpers shared across toolsets rather than
// tools of their own.
package mcpserver

import (
	"context"
	"time"

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
			registerYahooTools(srv, queries, newLeagueGuard(queries, opts.YahooLeagues), opts.Draft)
		case ToolsetNews:
			registerNewsCoverageTools(srv, queries, opts.NewsSources)
			registerPlayerNewsTools(srv, queries)
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
	registerRecentFormTools(srv, queries, time.Now)
	registerEdgeTools(srv, queries)
	registerPlayoffTools(srv, queries)
	registerPlayEventTools(srv, queries)
	registerLinemateTools(srv, queries)
	registerTeamSummaryTools(srv, queries)
	registerTeamScheduleTools(srv, queries)
	registerNewsTools(srv, queries)
}

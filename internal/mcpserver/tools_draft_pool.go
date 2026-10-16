package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
)

const draftPoolToolName = "get_draft_pool_coverage"

type draftPoolSource interface {
	leagueKeyResolver
	Now() time.Time
	StaleAfter() time.Duration
}

func draftPoolCoverageTool(q draft.Queries, guard leagueGuard, source draftPoolSource) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftPoolToolName,
			mcp.WithDescription(fmt.Sprintf("Report draftable player-pool coverage for a Yahoo league: pool size, eligibility gaps, unmatched NHL players, freshness, and warnings. Each gap list is capped at %d players.", draft.PoolReportListLimit)),
			mcp.WithNumber(seasonYearParam.name, mcp.Required(), mcp.Description("Season as a start year (2025) or season ID (20252026)")),
			mcp.WithString(leagueArg, mcp.Required(), mcp.Description("Yahoo league key (465.l.1001) or numeric ID (1001); an ID is resolved within season")),
		),
		Handler: guard.scopedKey(source, func(ctx context.Context, _ mcp.CallToolRequest, ref draftrank.LeagueRef) (*mcp.CallToolResult, error) {
			report, err := draft.LoadLeagueReport(ctx, q, ref.Season, ref.LeagueID, source.Now(), source.StaleAfter())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var output strings.Builder
			if err := draft.WritePoolReport(&output, report); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(output.String()), nil
		}),
	}
}

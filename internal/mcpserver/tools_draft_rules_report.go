package mcpserver

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
)

const (
	draftRulesReportToolName = "get_draft_rules_report"
	draftRulesLeaguesArg     = "leagues"
)

func draftRulesReportTool(q draft.Queries, guard leagueGuard, source draftPoolSource) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftRulesReportToolName,
			mcp.WithDescription("Render the same Markdown rules comparison as `draft rules`, including scoring, roster, draft, settings, and stale-data warnings. "+
				"Provide comma-separated Yahoo league keys or season-scoped IDs in `leagues`; every league must be served by this MCP server."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season as a start year (2025) or season ID (20252026)")),
			mcp.WithString(draftRulesLeaguesArg, mcp.Required(), mcp.Description("Comma-separated Yahoo league keys (465.l.1001) or numeric IDs (1001); IDs are resolved within season")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requiredDraftSeason(req)
			if errResult != nil {
				return errResult, nil
			}
			leagueValues, errResult := requiredDraftRuleLeagues(req)
			if errResult != nil {
				return errResult, nil
			}
			return renderDraftRulesReport(ctx, q, guard, source, season, leagueValues)
		},
	}
}

func requiredDraftRuleLeagues(req mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	value, errResult := optionalString(req, draftRulesLeaguesArg)
	if errResult != nil {
		return nil, errResult
	}
	var leagues []string
	for _, field := range strings.Split(value, leagueKeyListSeparator) {
		field = strings.TrimSpace(field)
		if field != "" {
			leagues = append(leagues, field)
		}
	}
	if len(leagues) == 0 {
		return nil, mcp.NewToolResultError(draftRulesLeaguesArg + " is required")
	}
	return leagues, nil
}

func renderDraftRulesReport(ctx context.Context, q draft.Queries, guard leagueGuard, source draftPoolSource, season int, leagueValues []string) (*mcp.CallToolResult, error) {
	refs, errResult := resolveDraftRuleLeagues(ctx, guard, source, season, leagueValues)
	if errResult != nil {
		return errResult, nil
	}
	now := source.Now()
	reports := make([]draft.LeagueReport, 0, len(refs))
	for _, ref := range refs {
		report, err := draft.LoadLeagueReport(ctx, q, ref.Season, ref.LeagueID, now, source.StaleAfter())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		reports = append(reports, report)
	}
	var output strings.Builder
	if err := draft.WriteComparison(&output, reports, draft.ComparisonOptions{GeneratedAt: now, MaxAge: source.StaleAfter()}); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(output.String()), nil
}

func resolveDraftRuleLeagues(ctx context.Context, guard leagueGuard, resolver leagueKeyResolver, season int, values []string) ([]draftrank.LeagueRef, *mcp.CallToolResult) {
	refs := make([]draftrank.LeagueRef, 0, len(values))
	for _, value := range values {
		ref, errResult := guard.resolveKey(ctx, resolver, value, season)
		if errResult != nil {
			return nil, errResult
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

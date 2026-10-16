package mcpserver

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/news"
)

const (
	newsCoverageToolName = "get_news_coverage"
	// newsCoverageSection titles the one CSV section of get_news_coverage.
	newsCoverageSection = "coverage"
)

// registerNewsCoverageTools adds the news coverage tool of the news toolset.
func registerNewsCoverageTools(srv *server.MCPServer, queries news.ReportQueries, sources []news.Source) {
	srv.AddTools(newsCoverageTool(queries, sources))
}

// newsCoverageTool reports which news sources are fresh, failing, stale or
// missing, so a caller knows whether "no news" is real. It reads the same
// coverage the `puckdb news report` command prints.
func newsCoverageTool(q news.ReportQueries, sources []news.Source) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(newsCoverageToolName,
			mcp.WithDescription("Report the freshness of every enabled player news source: fresh, failing, stale or missing, "+
				"so a caller can tell whether 'no news' is real or just missing data. "+
				"Prints a '# season=... sources=... generated_at=...' header, then a '# coverage' CSV section: one row per "+
				"source with its publisher, kind, priority, scope, status, the newest good data (as_of), last attempt and "+
				"success, consecutive failures and last error."),
			mcp.WithNumber(seasonYearParam.name, mcp.Description(
				"Season as a start year (2025) or a season ID (20252026); default: the current season")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := seasonYear(req)
			if errResult != nil {
				return errResult, nil
			}
			states, err := news.LoadFetchStates(ctx, q)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			now := time.Now()
			coverage := news.EvaluateCoverage(sources, states, season, now)
			text, err := newsCoverageText(season, now, coverage)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// newsCoverageRow is one source's coverage as CSV.
type newsCoverageRow struct {
	SourceID            string    `json:"source_id"`
	Publisher           string    `json:"publisher"`
	Kind                string    `json:"kind"`
	Priority            int       `json:"priority"`
	Scope               string    `json:"scope"`
	Status              string    `json:"status"`
	AsOf                time.Time `json:"as_of"`
	LastAttemptAt       time.Time `json:"last_attempt_at"`
	LastSuccessAt       time.Time `json:"last_success_at"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastError           string    `json:"last_error"`
}

// newsCoverageText renders the header and coverage section.
func newsCoverageText(season int, now time.Time, coverage []news.SourceCoverage) (string, error) {
	rows := make([]newsCoverageRow, 0, len(coverage))
	for _, c := range coverage {
		rows = append(rows, newsCoverageRow{
			SourceID: c.Source.ID, Publisher: c.Source.Publisher, Kind: string(c.Source.Kind),
			Priority: c.Source.Priority, Scope: c.Scope, Status: string(c.Status), AsOf: c.AsOf,
			LastAttemptAt: c.State.LastAttemptAt, LastSuccessAt: c.State.LastSuccessAt,
			ConsecutiveFailures: c.State.ConsecutiveFailures, LastError: c.State.LastError,
		})
	}
	var h metadataHeader
	h.add("season", season)
	h.add("sources", len(rows))
	h.add("generated_at", now.UTC().Format(time.RFC3339))
	section, err := csvSection(newsCoverageSection, rows)
	if err != nil {
		return "", err
	}
	return h.String() + "\n" + section, nil
}

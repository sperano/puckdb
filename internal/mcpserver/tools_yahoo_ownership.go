package mcpserver

import (
	"context"
	"math"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// maxYahooPlayerOwnershipRanges bounds the CSV response for unusually
	// long or fragmented ownership histories.
	maxYahooPlayerOwnershipRanges = 1000
	yahooPlayerIDArg              = "yahoo_player_id"
	ownershipStartDateLayout      = time.DateOnly
)

var yahooPlayerIDParam = intArg{name: yahooPlayerIDArg, min: minimumPositiveInteger, max: math.MaxInt32}

type yahooPlayerOwnershipRange struct {
	TeamID    int32  `json:"team_id"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func yahooPlayerOwnershipTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_player_ownership",
			mcp.WithDescription("Get the Yahoo fantasy team roster history for a player as compact date ranges, merging consecutive calendar days on the same team. Returns up to 1000 ranges, oldest first."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber(yahooPlayerIDArg, mcp.Required(), mcp.Description("Yahoo player ID")),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int32](req, yahooPlayerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			rows, err := q.GetYahooTeamRostersByPlayer(ctx, sqlcdb.GetYahooTeamRostersByPlayerParams{
				LeagueID: leagueID,
				PlayerID: playerID,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(compactYahooPlayerOwnership(rows))
		}),
	}
}

func compactYahooPlayerOwnership(rows []sqlcdb.YahooTeamRoster) []yahooPlayerOwnershipRange {
	ranges := make([]yahooPlayerOwnershipRange, 0, len(rows))
	for _, row := range rows {
		if !row.Date.Valid {
			continue
		}
		if len(ranges) > 0 && extendsOwnershipRange(&ranges[len(ranges)-1], row) {
			continue
		}
		ranges = append(ranges, yahooPlayerOwnershipRange{
			TeamID:    row.TeamID,
			StartDate: ownershipDate(row.Date.Time),
			EndDate:   ownershipDate(row.Date.Time),
		})
	}
	if len(ranges) > maxYahooPlayerOwnershipRanges {
		return ranges[:maxYahooPlayerOwnershipRanges]
	}
	return ranges
}

func extendsOwnershipRange(current *yahooPlayerOwnershipRange, row sqlcdb.YahooTeamRoster) bool {
	if current.TeamID != row.TeamID {
		return false
	}
	date, err := time.Parse(ownershipStartDateLayout, current.EndDate)
	if err != nil {
		return false
	}
	rowDate := row.Date.Time
	if !row.Date.Valid || rowDate.After(date.AddDate(0, 0, 1)) {
		return false
	}
	current.EndDate = ownershipDate(rowDate)
	return true
}

func ownershipDate(date time.Time) string {
	return date.Format(ownershipStartDateLayout)
}

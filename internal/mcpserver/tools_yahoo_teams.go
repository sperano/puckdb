package mcpserver

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Rendering of a team's managers in one CSV cell, e.g.
// "Alice (commissioner, current login); Bob".
const (
	managerSeparator        = "; "
	managerFlagsOpen        = " ("
	managerFlagsClose       = ")"
	managerFlagSeparator    = ", "
	managerFlagCommissioner = "commissioner"
	managerFlagLogin        = "current login"
)

// yahooTeamRow is one get_yahoo_teams_by_league row: the team plus its
// managers. Managers come only from GetYahooTeamManagersByLeague, whose
// explicit column list leaves out the personal guid and email columns.
type yahooTeamRow struct {
	LeagueID              int32              `json:"league_id"`
	ID                    int32              `json:"id"`
	TeamKey               string             `json:"team_key"`
	Name                  string             `json:"name"`
	Url                   string             `json:"url"`
	LogoUrl               string             `json:"logo_url"`
	DraftPosition         pgtype.Int4        `json:"draft_position"`
	WaiverPriority        pgtype.Int4        `json:"waiver_priority"`
	NumberOfMoves         int32              `json:"number_of_moves"`
	NumberOfTrades        int32              `json:"number_of_trades"`
	IsOwnedByCurrentLogin bool               `json:"is_owned_by_current_login"`
	Managers              string             `json:"managers"`
	CreatedAt             pgtype.Timestamptz `json:"created_at"`
	UpdatedAt             pgtype.Timestamptz `json:"updated_at"`
}

func yahooTeamsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_teams_by_league",
			mcp.WithDescription("List all teams in a Yahoo fantasy league: draft position, waiver priority, moves, trades, "+
				"whether the current login owns the team, and its managers. managers lists the nicknames separated by \"; \", "+
				"each followed by (commissioner) and/or (current login) when that applies."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			rows, err := yahooTeamRows(ctx, q, leagueID)
			return toolResult(rows, err)
		}),
	}
}

// yahooTeamRows loads a league's teams and managers in two queries and joins
// them by team ID.
func yahooTeamRows(ctx context.Context, q yahooQueries, leagueID int32) ([]yahooTeamRow, error) {
	teams, err := q.GetYahooTeamsByLeague(ctx, leagueID)
	if err != nil {
		return nil, err
	}
	managers, err := q.GetYahooTeamManagersByLeague(ctx, leagueID)
	if err != nil {
		return nil, err
	}
	byTeam := make(map[int32][]string, len(teams))
	for _, m := range managers {
		byTeam[m.TeamID] = append(byTeam[m.TeamID], formatManager(m))
	}
	rows := make([]yahooTeamRow, len(teams))
	for i, team := range teams {
		rows[i] = newYahooTeamRow(team, strings.Join(byTeam[team.ID], managerSeparator))
	}
	return rows, nil
}

func newYahooTeamRow(t sqlcdb.YahooTeam, managers string) yahooTeamRow {
	return yahooTeamRow{
		LeagueID:              t.LeagueID,
		ID:                    t.ID,
		TeamKey:               t.TeamKey,
		Name:                  t.Name,
		Url:                   t.Url,
		LogoUrl:               t.LogoUrl,
		DraftPosition:         t.DraftPosition,
		WaiverPriority:        t.WaiverPriority,
		NumberOfMoves:         t.NumberOfMoves,
		NumberOfTrades:        t.NumberOfTrades,
		IsOwnedByCurrentLogin: t.IsOwnedByCurrentLogin,
		Managers:              managers,
		CreatedAt:             t.CreatedAt,
		UpdatedAt:             t.UpdatedAt,
	}
}

// formatManager renders one manager as its nickname followed by its flags.
func formatManager(m sqlcdb.GetYahooTeamManagersByLeagueRow) string {
	var flags []string
	if m.IsCommissioner {
		flags = append(flags, managerFlagCommissioner)
	}
	if m.IsCurrentLogin {
		flags = append(flags, managerFlagLogin)
	}
	if len(flags) == 0 {
		return m.Nickname
	}
	return m.Nickname + managerFlagsOpen + strings.Join(flags, managerFlagSeparator) + managerFlagsClose
}

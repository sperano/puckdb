package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerPlayerTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(getPlayerTool(queries))

	srv.AddTool(
		mcp.NewTool("get_players_by_team",
			mcp.WithDescription("Get all players currently assigned to a team."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			players, err := queries.GetPlayersByTeam(ctx, pgtype.Int8{Int64: int64(teamID), Valid: true})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_players_by_position",
			mcp.WithDescription("Get all players who play a given position."),
			mcp.WithString("position", mcp.Required(), mcp.Description("Position code: C, LW, RW, F, D, or G")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pos, err := req.RequireString("position")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			players, err := queries.GetPlayersByPosition(ctx, sqlcdb.NullPlayerPosition{
				PlayerPosition: sqlcdb.PlayerPosition(pos),
				Valid:          true,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_players_by_birthplace",
			mcp.WithDescription("Get NHL players (across history) born in a given place, joined with their NHL regular-season career totals (games, goals, assists, points, PIM). Default sort is career_points DESC, so this answers both 'list players born in X' (use a large limit) and 'top N born in X' (use a small limit) in one tool. Goalies will appear at the bottom — for goalie-specific ranking, use a goalie-stat tool or sort client-side by a different column."),
			mcp.WithString("birth_country", mcp.Required(), mcp.Description("ISO 3166 alpha-3 country code, uppercase (e.g., CAN, USA, FRA, SWE, FIN, RUS, CZE).")),
			mcp.WithString("birth_city", mcp.Description("City name pattern, ILIKE match. Use SQL wildcards for fuzzy matching (e.g., 'Montr%al%' to cover 'Montréal' / 'Montreal-Nord'). Optional.")),
			mcp.WithString("birth_state_province", mcp.Description("State / province pattern, ILIKE match (e.g., 'Quebec', 'Ontario', 'MN'). Optional.")),
			mcp.WithNumber("limit", mcp.Description("Max rows to return. Defaults to 200 — pass a smaller number for top-N queries.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			country, err := req.RequireString("birth_country")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			limitN := int32(req.GetInt("limit", 200))
			if limitN <= 0 {
				limitN = 200
			}
			params := sqlcdb.GetPlayersByBirthplaceParams{
				BirthCountry: country,
				LimitN:       limitN,
			}
			if city := req.GetString("birth_city", ""); city != "" {
				params.BirthCity = pgtype.Text{String: city, Valid: true}
			}
			if state := req.GetString("birth_state_province", ""); state != "" {
				params.BirthStateProvince = pgtype.Text{String: state, Valid: true}
			}
			rows, err := queries.GetPlayersByBirthplace(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_active_players",
			mcp.WithDescription("Get all currently active NHL players."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			players, err := queries.GetActivePlayers(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_career_totals",
			mcp.WithDescription("Get a player's career season-by-season totals (goals, assists, points, +/-, PIM) across all leagues."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			totals, err := queries.GetPlayerNHLSeasonTotals(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(totals)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_awards",
			mcp.WithDescription("Get all awards/trophies won by a player (Hart, Norris, Vezina, etc.)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			awards, err := queries.GetPlayerAwards(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(awards)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_roster_history",
			mcp.WithDescription("Get a player's roster history — which teams they played for each season, with position, jersey number, and physical info."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			roster, err := queries.GetSeasonRosterByPlayer(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(roster)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_three_stars",
			mcp.WithDescription("Get all three-star selections for a player (1st, 2nd, or 3rd star of the game)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			stars, err := queries.GetPlayerThreeStarSelections(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stars)
		},
	)
}

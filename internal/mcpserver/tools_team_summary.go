package mcpserver

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	teamSummaryToolName    = "get_team_season_summary"
	teamSummaryGameTypeArg = "game_type"
	// teamSkatersSection and teamGoaliesSection title the two CSV sections
	// of get_team_season_summary.
	teamSkatersSection = "skaters"
	teamGoaliesSection = "goalies"
	// perGameDecimals rounds goals per game and GAA (3.05, 2.71).
	perGameDecimals = 2
	// savePctgDecimals rounds the save percentage (0.915).
	savePctgDecimals = 3
	// decimalBase scales a value before rounding it to some decimals.
	decimalBase = 10
)

// teamSummaryGameTypes are the game types get_team_season_summary
// accepts; the first is the default. Regular season and playoffs are
// never added together.
var teamSummaryGameTypes = []sqlcdb.GameType{
	sqlcdb.GameTypeRegularSeason,
	sqlcdb.GameTypePlayoffs,
}

// noDateCutoff makes GetTeamGoalsPerGame count every completed game of
// the season.
var noDateCutoff = pgtype.Date{InfinityModifier: pgtype.Infinity, Valid: true}

type teamSummaryQueries interface {
	GetTeamGoalsPerGame(context.Context, sqlcdb.GetTeamGoalsPerGameParams) (sqlcdb.GetTeamGoalsPerGameRow, error)
	GetTeamSkaterSeasonTotals(context.Context, sqlcdb.GetTeamSkaterSeasonTotalsParams) ([]sqlcdb.GetTeamSkaterSeasonTotalsRow, error)
	GetTeamGoalieSeasonTotals(context.Context, sqlcdb.GetTeamGoalieSeasonTotalsParams) ([]sqlcdb.GetTeamGoalieSeasonTotalsRow, error)
}

// registerTeamSummaryTools adds get_team_season_summary.
func registerTeamSummaryTools(srv *server.MCPServer, queries teamSummaryQueries) {
	srv.AddTools(teamSummaryTool(queries))
}

// teamSummaryRequest is one validated get_team_season_summary call.
type teamSummaryRequest struct {
	teamID   int64
	season   int32
	gameType sqlcdb.GameType
}

func teamSummaryTool(q teamSummaryQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(teamSummaryToolName,
			mcp.WithDescription("Summarize one team's season in one call: a '# team_id=... season=... game_type=... games_played=... "+
				"goals_for_per_game=... goals_against_per_game=...' header over completed games, then a '# skaters' CSV section "+
				"(each skater's games, goals, assists, points, plus-minus, shots and PIM with this team, most points first) and a "+
				"'# goalies' CSV section (games, starts, W-L-OTL, shots against, saves, goals against, TOI, save percentage, GAA, "+
				"most wins first). A traded player counts only the games with this team. Regular season and playoffs are "+
				"summarized separately, never added together. Empty sections and games_played=0 mean no imported games for "+
				"that team, season and game type."),
			mcp.WithNumber(teamIDParam.name, mcp.Required(), mcp.Description("Team ID (use find_team to resolve an abbreviation)")),
			mcp.WithNumber(seasonParam.name, mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString(teamSummaryGameTypeArg, mcp.Description("regular_season (default) or playoffs")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			summary, errResult := teamSummaryArgs(req)
			if errResult != nil {
				return errResult, nil
			}
			text, err := teamSummaryText(ctx, q, summary)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// teamSummaryArgs reads and checks every argument before any query runs.
func teamSummaryArgs(req mcp.CallToolRequest) (teamSummaryRequest, *mcp.CallToolResult) {
	var summary teamSummaryRequest
	var errResult *mcp.CallToolResult
	if summary.teamID, errResult = requireInt[int64](req, teamIDParam); errResult != nil {
		return summary, errResult
	}
	if summary.season, errResult = requireInt[int32](req, seasonParam); errResult != nil {
		return summary, errResult
	}
	summary.gameType, errResult = teamSummaryGameType(req)
	return summary, errResult
}

// teamSummaryGameType reads the optional game type; absent or empty is the
// regular season.
func teamSummaryGameType(req mcp.CallToolRequest) (sqlcdb.GameType, *mcp.CallToolResult) {
	raw, errResult := optionalString(req, teamSummaryGameTypeArg)
	if errResult != nil {
		return "", errResult
	}
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return teamSummaryGameTypes[0], nil
	}
	gameType := sqlcdb.GameType(name)
	if !slices.Contains(teamSummaryGameTypes, gameType) {
		accepted := make([]string, len(teamSummaryGameTypes))
		for i, t := range teamSummaryGameTypes {
			accepted[i] = string(t)
		}
		return "", mcp.NewToolResultError(fmt.Sprintf("unknown %s %s; accepted: %s",
			teamSummaryGameTypeArg, formatRaw(raw), strings.Join(accepted, ", ")))
	}
	return gameType, nil
}

// teamSummaryText runs the three queries and renders the header and the
// skater and goalie sections.
func teamSummaryText(ctx context.Context, q teamSummaryQueries, r teamSummaryRequest) (string, error) {
	goals, err := q.GetTeamGoalsPerGame(ctx, sqlcdb.GetTeamGoalsPerGameParams{
		Season:     r.season,
		GameDate:   noDateCutoff,
		HomeTeamID: r.teamID,
		GameType:   r.gameType,
	})
	if err != nil {
		return "", fmt.Errorf("goals per game: %w", err)
	}
	skaters, err := q.GetTeamSkaterSeasonTotals(ctx, sqlcdb.GetTeamSkaterSeasonTotalsParams{
		TeamID: r.teamID, Season: r.season, GameType: r.gameType,
	})
	if err != nil {
		return "", fmt.Errorf("skater totals: %w", err)
	}
	goalieRows, err := q.GetTeamGoalieSeasonTotals(ctx, sqlcdb.GetTeamGoalieSeasonTotalsParams{
		TeamID: r.teamID, Season: r.season, GameType: r.gameType,
	})
	if err != nil {
		return "", fmt.Errorf("goalie totals: %w", err)
	}
	goalies, err := teamGoalieRows(goalieRows)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(teamSummaryHeader(r, goals).String() + "\n")
	for _, section := range []struct {
		title string
		rows  any
	}{
		{teamSkatersSection, skaters},
		{teamGoaliesSection, goalies},
	} {
		text, err := csvSection(section.title, section.rows)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

// teamSummaryHeader prints the request and the team's goals per game. The
// query reports 0 goals per game when no game counts, so the averages are
// left empty then rather than shown as a real 0.00.
func teamSummaryHeader(r teamSummaryRequest, goals sqlcdb.GetTeamGoalsPerGameRow) metadataHeader {
	var h metadataHeader
	h.add(teamIDParam.name, r.teamID)
	h.add(seasonParam.name, r.season)
	h.add(teamSummaryGameTypeArg, r.gameType)
	h.add("games_played", goals.GamesPlayed)
	goalsFor, goalsAgainst := "", ""
	if goals.GamesPlayed > 0 {
		goalsFor = formatPerGame(goals.GoalsForPerGame)
		goalsAgainst = formatPerGame(goals.GoalsAgainstPerGame)
	}
	h.add("goals_for_per_game", goalsFor)
	h.add("goals_against_per_game", goalsAgainst)
	return h
}

// formatPerGame prints an average with perGameDecimals decimals.
func formatPerGame(n pgtype.Numeric) string {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return ""
	}
	return strconv.FormatFloat(f.Float64, 'f', perGameDecimals, 64)
}

// teamGoalieRow is one get_team_season_summary goalie row: the query's
// columns, with the save percentage and GAA rounded.
type teamGoalieRow struct {
	PlayerID          int64         `json:"player_id"`
	FirstName         string        `json:"first_name"`
	LastName          string        `json:"last_name"`
	GamesPlayed       int32         `json:"games_played"`
	Starts            int32         `json:"starts"`
	Wins              int32         `json:"wins"`
	Losses            int32         `json:"losses"`
	OTL               int32         `json:"otl"`
	TotalShotsAgainst int32         `json:"total_shots_against"`
	TotalSaves        int32         `json:"total_saves"`
	TotalGoalsAgainst int32         `json:"total_goals_against"`
	TotalToiSeconds   int32         `json:"total_toi_seconds"`
	SeasonSavePctg    pgtype.Float8 `json:"season_save_pctg"`
	SeasonGaa         pgtype.Float8 `json:"season_gaa"`
}

func teamGoalieRows(rows []sqlcdb.GetTeamGoalieSeasonTotalsRow) ([]teamGoalieRow, error) {
	out := make([]teamGoalieRow, len(rows))
	for i, r := range rows {
		savePctg, err := roundedFloat(r.SeasonSavePctg, savePctgDecimals)
		if err != nil {
			return nil, fmt.Errorf("goalie %d save percentage: %w", r.PlayerID, err)
		}
		gaa, err := roundedFloat(r.SeasonGaa, perGameDecimals)
		if err != nil {
			return nil, fmt.Errorf("goalie %d GAA: %w", r.PlayerID, err)
		}
		out[i] = teamGoalieRow{
			PlayerID:          r.PlayerID,
			FirstName:         r.FirstName,
			LastName:          r.LastName,
			GamesPlayed:       r.GamesPlayed,
			Starts:            r.Starts,
			Wins:              r.Wins,
			Losses:            r.Losses,
			OTL:               r.OTL,
			TotalShotsAgainst: r.TotalShotsAgainst,
			TotalSaves:        r.TotalSaves,
			TotalGoalsAgainst: r.TotalGoalsAgainst,
			TotalToiSeconds:   r.TotalToiSeconds,
			SeasonSavePctg:    savePctg,
			SeasonGaa:         gaa,
		}
	}
	return out, nil
}

// roundedFloat rounds a float8 column sqlc types as interface{} (NULL when
// no shots or no time on ice) to decimals decimals.
func roundedFloat(v any, decimals int) (pgtype.Float8, error) {
	switch f := v.(type) {
	case nil:
		return pgtype.Float8{}, nil
	case float64:
		scale := math.Pow(decimalBase, float64(decimals))
		return pgtype.Float8{Float64: math.Round(f*scale) / scale, Valid: true}, nil
	default:
		return pgtype.Float8{}, fmt.Errorf("unexpected type %T", v)
	}
}

package mcpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const gameSummaryToolName = "get_game_summary"

// Section titles of get_game_summary, in output order.
const (
	threeStarsSection = "three_stars"
	goalsSection      = "goals"
	shootoutSection   = "shootout"
	officialsSection  = "officials"
	coachesSection    = "coaches"
	scratchesSection  = "scratches"
)

// gameSummaryQueries are the existing per-game reads behind
// get_game_summary; *sqlcdb.Queries implements them.
type gameSummaryQueries interface {
	GetGame(ctx context.Context, id int64) (sqlcdb.GetGameRow, error)
	GetGameThreeStars(ctx context.Context, gameID int64) ([]sqlcdb.GetGameThreeStarsRow, error)
	GetGoalHighlights(ctx context.Context, gameID int64) ([]sqlcdb.GetGoalHighlightsRow, error)
	GetShootoutAttempts(ctx context.Context, gameID int64) ([]sqlcdb.GetShootoutAttemptsRow, error)
	GetGameOfficials(ctx context.Context, gameID int64) ([]sqlcdb.GameOfficial, error)
	GetGameCoaches(ctx context.Context, gameID int64) ([]sqlcdb.GetGameCoachesRow, error)
	GetGameScratches(ctx context.Context, gameID int64) ([]sqlcdb.GetGameScratchesRow, error)
}

// gameSummary is everything get_game_summary reports for one game.
type gameSummary struct {
	game       sqlcdb.GetGameRow
	threeStars []sqlcdb.GetGameThreeStarsRow
	goals      []sqlcdb.GetGoalHighlightsRow
	shootout   []sqlcdb.GetShootoutAttemptsRow
	officials  []sqlcdb.GameOfficial
	coaches    []sqlcdb.GetGameCoachesRow
	scratches  []sqlcdb.GetGameScratchesRow
}

// The section rows leave out game_id, which the header carries once.

type summaryStarRow struct {
	Star      int16                     `json:"star"`
	PlayerID  int64                     `json:"player_id"`
	FirstName string                    `json:"first_name"`
	LastName  string                    `json:"last_name"`
	Position  sqlcdb.NullPlayerPosition `json:"position"`
}

type summaryGoalRow struct {
	Period       int16       `json:"period"`
	TimeInPeriod string      `json:"time_in_period"`
	PlayerID     int64       `json:"player_id"`
	FirstName    string      `json:"first_name"`
	LastName     string      `json:"last_name"`
	GoalsToDate  pgtype.Int2 `json:"goals_to_date"`
	EventID      int64       `json:"event_id"`
	ClipURL      pgtype.Text `json:"clip_url"`
}

type summaryShootoutRow struct {
	Sequence   int16                 `json:"sequence"`
	TeamAbbrev string                `json:"team_abbrev"`
	PlayerID   int64                 `json:"player_id"`
	FirstName  string                `json:"first_name"`
	LastName   string                `json:"last_name"`
	ShotType   string                `json:"shot_type"`
	Result     sqlcdb.ShootoutResult `json:"result"`
	GameWinner bool                  `json:"game_winner"`
}

type summaryOfficialRow struct {
	Role     sqlcdb.OfficialRole `json:"role"`
	Sequence int16               `json:"sequence"`
	Name     string              `json:"name"`
}

type summaryCoachRow struct {
	TeamAbbrev string `json:"team_abbrev"`
	HeadCoach  string `json:"head_coach"`
}

type summaryScratchRow struct {
	TeamAbbrev string                    `json:"team_abbrev"`
	PlayerID   int64                     `json:"player_id"`
	FirstName  string                    `json:"first_name"`
	LastName   string                    `json:"last_name"`
	Position   sqlcdb.NullPlayerPosition `json:"position"`
}

func gameSummaryTool(q gameSummaryQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(gameSummaryToolName,
			mcp.WithDescription("Get the story of one NHL game in one call: a header (date, teams, final score, "+
				"how it ended), then CSV sections for the three stars, goals (scorer, period, time, season goal count, "+
				"highlight clip URL), shootout attempts, officials, head coaches and scratched players. "+
				"Rows name teams by abbreviation; a section reads \"no results\" when nothing was imported for it."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID (use get_games_by_date to find IDs)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			summary, err := loadGameSummary(ctx, q, gameID)
			if errors.Is(err, pgx.ErrNoRows) {
				return gameNotFoundResult(gameID), nil
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := summary.text()
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// gameNotFoundResult is the error for a game GetGame cannot read: its
// inner joins also drop a game whose club has no season_teams row.
func gameNotFoundResult(gameID int64) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(
		"game_id %d not found (unknown game, or one of its teams has no season_teams row for its season)", gameID))
}

// loadGameSummary reads the game row, then each section's rows. An unknown
// game returns pgx.ErrNoRows before any section is read.
func loadGameSummary(ctx context.Context, q gameSummaryQueries, gameID int64) (gameSummary, error) {
	game, err := q.GetGame(ctx, gameID)
	if err != nil {
		return gameSummary{}, err
	}
	s := gameSummary{game: game}
	if s.threeStars, err = q.GetGameThreeStars(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load three stars of game %d: %w", gameID, err)
	}
	if s.goals, err = q.GetGoalHighlights(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load goals of game %d: %w", gameID, err)
	}
	if s.shootout, err = q.GetShootoutAttempts(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load shootout of game %d: %w", gameID, err)
	}
	if s.officials, err = q.GetGameOfficials(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load officials of game %d: %w", gameID, err)
	}
	if s.coaches, err = q.GetGameCoaches(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load coaches of game %d: %w", gameID, err)
	}
	if s.scratches, err = q.GetGameScratches(ctx, gameID); err != nil {
		return gameSummary{}, fmt.Errorf("load scratches of game %d: %w", gameID, err)
	}
	return s, nil
}

// text renders the header line and every section, each ending in a newline.
func (s gameSummary) text() (string, error) {
	sections := []struct {
		title string
		rows  any
	}{
		{threeStarsSection, s.starRows()},
		{goalsSection, s.goalRows()},
		{shootoutSection, s.shootoutRows()},
		{officialsSection, s.officialRows()},
		{coachesSection, s.coachRows()},
		{scratchesSection, s.scratchRows()},
	}
	text := s.header().String() + "\n"
	for _, section := range sections {
		csv, err := csvSection(section.title, section.rows)
		if err != nil {
			return "", err
		}
		text += csv
	}
	return text, nil
}

// header identifies the game; period_type tells how it ended (REG, OT, SO).
func (s gameSummary) header() metadataHeader {
	g := s.game
	var h metadataHeader
	h.add("game_id", g.ID)
	h.add("season", g.Season)
	h.add("game_type", g.GameType)
	h.add("game_date", dateText(g.GameDate))
	h.add("game_state", g.GameState)
	h.add("period_type", g.PeriodType)
	h.add("away", g.AwayTeamAbbrev)
	h.add("away_team_id", g.AwayTeamID)
	h.add("away_score", g.AwayTeamScore)
	h.add("home", g.HomeTeamAbbrev)
	h.add("home_team_id", g.HomeTeamID)
	h.add("home_score", g.HomeTeamScore)
	return h
}

// dateText prints a date as YYYY-MM-DD, or "" when it is NULL.
func dateText(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

// teamAbbrev names one of the game's two clubs; another team ID, which the
// imports never write, prints as the ID.
func (s gameSummary) teamAbbrev(teamID int64) string {
	switch teamID {
	case s.game.HomeTeamID:
		return s.game.HomeTeamAbbrev
	case s.game.AwayTeamID:
		return s.game.AwayTeamAbbrev
	}
	return fmt.Sprint(teamID)
}

func (s gameSummary) starRows() []summaryStarRow {
	rows := make([]summaryStarRow, 0, len(s.threeStars))
	for _, r := range s.threeStars {
		rows = append(rows, summaryStarRow{
			Star: r.Star, PlayerID: r.PlayerID, FirstName: r.FirstName, LastName: r.LastName, Position: r.Position,
		})
	}
	return rows
}

func (s gameSummary) goalRows() []summaryGoalRow {
	rows := make([]summaryGoalRow, 0, len(s.goals))
	for _, r := range s.goals {
		rows = append(rows, summaryGoalRow{
			Period: r.Period, TimeInPeriod: r.TimeInPeriod, PlayerID: r.PlayerID, FirstName: r.FirstName,
			LastName: r.LastName, GoalsToDate: r.GoalsToDate, EventID: r.EventID, ClipURL: r.HighlightClipUrl,
		})
	}
	return rows
}

func (s gameSummary) shootoutRows() []summaryShootoutRow {
	rows := make([]summaryShootoutRow, 0, len(s.shootout))
	for _, r := range s.shootout {
		rows = append(rows, summaryShootoutRow{
			Sequence: r.Sequence, TeamAbbrev: s.teamAbbrev(r.TeamID), PlayerID: r.PlayerID, FirstName: r.FirstName,
			LastName: r.LastName, ShotType: r.ShotType, Result: r.Result, GameWinner: r.GameWinner,
		})
	}
	return rows
}

func (s gameSummary) officialRows() []summaryOfficialRow {
	rows := make([]summaryOfficialRow, 0, len(s.officials))
	for _, r := range s.officials {
		rows = append(rows, summaryOfficialRow{Role: r.Role, Sequence: r.Sequence, Name: r.Name})
	}
	return rows
}

// coachRows lists the away coach first, as the header lists the away team
// first; GetGameCoaches has no ORDER BY.
func (s gameSummary) coachRows() []summaryCoachRow {
	coaches := slices.Clone(s.coaches)
	slices.SortStableFunc(coaches, func(a, b sqlcdb.GetGameCoachesRow) int {
		return cmp.Compare(s.awayFirst(a.TeamID), s.awayFirst(b.TeamID))
	})
	rows := make([]summaryCoachRow, 0, len(coaches))
	for _, r := range coaches {
		rows = append(rows, summaryCoachRow{TeamAbbrev: r.TeamAbbrev, HeadCoach: r.HeadCoach})
	}
	return rows
}

// awayFirst is a sort key putting the away team before the home team.
func (s gameSummary) awayFirst(teamID int64) int {
	if teamID == s.game.AwayTeamID {
		return 0
	}
	return 1
}

func (s gameSummary) scratchRows() []summaryScratchRow {
	rows := make([]summaryScratchRow, 0, len(s.scratches))
	for _, r := range s.scratches {
		rows = append(rows, summaryScratchRow{
			TeamAbbrev: s.teamAbbrev(r.TeamID), PlayerID: r.PlayerID, FirstName: r.FirstName,
			LastName: r.LastName, Position: r.Position,
		})
	}
	return rows
}

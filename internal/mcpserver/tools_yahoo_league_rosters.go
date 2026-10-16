package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	leagueRostersToolName = "get_yahoo_league_rosters"
	// maxLeagueTeams is the team count of Yahoo's largest league.
	maxLeagueTeams = 20
	// typicalTeamRosterRows bounds one team's roster rows on a date (active
	// slots, bench, IR and NA) in any common league setup.
	typicalTeamRosterRows = 30
	// defaultLeagueRostersLimit covers a full league without a limit argument.
	defaultLeagueRostersLimit = maxLeagueTeams * typicalTeamRosterRows
	// maxLeagueRostersLimit caps limit to bound the result size; the
	// header's roster_rows count shows the cut.
	maxLeagueRostersLimit = 1000
	// Section titles of get_yahoo_league_rosters.
	leagueRosterTeamsSection = "teams"
	leagueRostersSection     = "rosters"
)

// leagueRostersLimitParam is the limit of get_yahoo_league_rosters.
var leagueRostersLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxLeagueRostersLimit}

// leagueRosterTeamRow names a team once, so roster rows carry only its ID.
type leagueRosterTeamRow struct {
	TeamID      int32  `json:"team_id"`
	Name        string `json:"name"`
	RosterCount int    `json:"roster_count"`
}

// leagueRosterRow is one rostered player of one team on the requested date.
type leagueRosterRow struct {
	TeamID           int32       `json:"team_id"`
	SelectedPosition string      `json:"selected_position"`
	YahooPlayerID    int32       `json:"yahoo_player_id"`
	PlayerKey        string      `json:"player_key"`
	Name             string      `json:"name"`
	NHLPlayerID      pgtype.Int8 `json:"nhl_player_id"`
	Team             string      `json:"team"`
	Positions        string      `json:"positions"`
	Status           string      `json:"status"`
	InjuryNote       string      `json:"injury_note"`
}

func yahooLeagueRostersTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(leagueRostersToolName,
			mcp.WithDescription("Get every team's roster in a Yahoo fantasy league on a date in one call (one get_yahoo_roster "+
				"call per team otherwise). A header line gives the league, the date, the number of roster rows on that date "+
				"and how many are returned; a teams section names each team with its roster size; a rosters section lists "+
				"one row per rostered player as CSV ordered by team, selected position and Yahoo player ID. name is the "+
				"matched NHL player's, else Yahoo's from the league's imported player pool; nhl_player_id is empty when no "+
				"NHL player is matched. team is the player's NHL team abbreviation as Yahoo lists it, status the Yahoo "+
				"status code (DTD, O, IR, ...; empty when Yahoo lists none). A date with no imported rosters (before the "+
				"season, or not imported yet) has roster_rows=0."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Roster date in YYYY-MM-DD format")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max roster rows (default %d, max %d)",
				defaultLeagueRostersLimit, maxLeagueRostersLimit))),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			date, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			limit, errResult := boundedLimitOrDefault(req, leagueRostersLimitParam, defaultLeagueRostersLimit)
			if errResult != nil {
				return errResult, nil
			}
			rosters, err := loadLeagueRosters(ctx, q, leagueID, date)
			if errors.Is(err, errLeagueNotImported) {
				return unknownLeagueResult(leagueID), nil
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := rosters.text(limit)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		}),
	}
}

// leagueRosters is what get_yahoo_league_rosters reports for one league
// and date.
type leagueRosters struct {
	league sqlcdb.YahooLeague
	date   pgtype.Date
	teams  []sqlcdb.YahooTeam
	rows   []sqlcdb.GetYahooLeagueRostersWithPlayersRow
}

// loadLeagueRosters reads the league row, its teams and every roster row
// of the date.
func loadLeagueRosters(ctx context.Context, q yahooQueries, leagueID int32, date pgtype.Date) (leagueRosters, error) {
	league, err := loadLeague(ctx, q, leagueID)
	if err != nil {
		return leagueRosters{}, err
	}
	teams, err := q.GetYahooTeamsByLeague(ctx, leagueID)
	if err != nil {
		return leagueRosters{}, fmt.Errorf("load teams of league %d: %w", leagueID, err)
	}
	rows, err := q.GetYahooLeagueRostersWithPlayers(ctx, sqlcdb.GetYahooLeagueRostersWithPlayersParams{
		LeagueID: leagueID,
		Date:     date,
	})
	if err != nil {
		return leagueRosters{}, fmt.Errorf("load rosters of league %d on %s: %w", leagueID, date.Time.Format(time.DateOnly), err)
	}
	return leagueRosters{league: league, date: date, teams: teams, rows: rows}, nil
}

// text renders the header, the teams and the first limit roster rows.
func (r leagueRosters) text(limit int32) (string, error) {
	n := min(len(r.rows), int(limit))
	rows := make([]leagueRosterRow, 0, n)
	for _, row := range r.rows[:n] {
		rows = append(rows, leagueRosterRowOf(row))
	}
	teams, err := csvSection(leagueRosterTeamsSection, r.teamRows())
	if err != nil {
		return "", err
	}
	rosters, err := csvSection(leagueRostersSection, rows)
	if err != nil {
		return "", err
	}
	return r.header(len(rows)).String() + "\n" + teams + rosters, nil
}

// teamRows lists every team of the league with its roster size on the
// date, counted over all rows, not just the returned ones.
func (r leagueRosters) teamRows() []leagueRosterTeamRow {
	counts := make(map[int32]int, len(r.teams))
	for _, row := range r.rows {
		counts[row.TeamID]++
	}
	teams := make([]leagueRosterTeamRow, 0, len(r.teams))
	for _, team := range r.teams {
		teams = append(teams, leagueRosterTeamRow{TeamID: team.ID, Name: team.Name, RosterCount: counts[team.ID]})
	}
	return teams
}

// header identifies the league and date and shows whether limit cut the
// roster rows.
func (r leagueRosters) header(returned int) metadataHeader {
	var h metadataHeader
	h.add("league_id", r.league.ID)
	h.add("league_key", r.league.LeagueKey)
	h.add("season", r.league.Season)
	h.add("date", r.date.Time.Format(time.DateOnly))
	h.add("teams", len(r.teams))
	h.add("roster_rows", len(r.rows))
	h.add("returned", returned)
	return h
}

func leagueRosterRowOf(row sqlcdb.GetYahooLeagueRostersWithPlayersRow) leagueRosterRow {
	return leagueRosterRow{
		TeamID: row.TeamID, SelectedPosition: row.SelectedPosition, YahooPlayerID: row.YahooPlayerID,
		PlayerKey: row.PlayerKey, Name: row.Name, NHLPlayerID: row.NhlPlayerID, Team: row.EditorialTeamAbbr.String,
		Positions: row.DisplayPosition.String, Status: row.PlayerStatus.String, InjuryNote: row.InjuryNote.String,
	}
}

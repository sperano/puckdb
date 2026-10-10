package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// defaultLeaguePlayersLimit is the number of pool players returned
	// without a limit argument.
	defaultLeaguePlayersLimit = 100
	// maxLeaguePlayersLimit caps limit to bound the result size. A whole
	// pool can be larger; the header's matching count shows the cut.
	maxLeaguePlayersLimit = 1000
	// Arguments of get_yahoo_league_players besides league_id and limit.
	leaguePositionsArg     = "positions"
	leagueStatusArg        = "status"
	leagueUnmatchedOnlyArg = "unmatched_only"
	// filterListSeparator separates the values of a list argument ("C,LW").
	filterListSeparator = ","
	// anyStatusFilter matches every player Yahoo lists a status code for.
	anyStatusFilter = "ANY"
	// eligiblePositionsSeparator joins a player's eligible positions in one
	// CSV cell.
	eligiblePositionsSeparator = ","
	// leaguePlayersSection is the title of the player rows.
	leaguePlayersSection = "players"
)

// leaguePlayersLimitParam is the limit of get_yahoo_league_players.
var leaguePlayersLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxLeaguePlayersLimit}

// leaguePlayerPositions are the positions the positions filter accepts, the
// same set the draft ranking views filter on.
var leaguePlayerPositions = []string{
	draft.PositionCenter, draft.PositionLeftWing, draft.PositionRightWing, draft.PositionDefense, draft.PositionGoalie,
}

// leaguePlayerRow is one pool player. The league, season and fetch time are
// the same for every row and go in the header instead.
type leaguePlayerRow struct {
	YahooPlayerID     int32       `json:"yahoo_player_id"`
	PlayerKey         string      `json:"player_key"`
	Name              string      `json:"name"`
	Team              string      `json:"team"`
	PrimaryPosition   string      `json:"primary_position"`
	EligiblePositions string      `json:"eligible_positions"`
	PositionType      string      `json:"position_type"`
	Status            string      `json:"status"`
	StatusFull        string      `json:"status_full"`
	InjuryNote        string      `json:"injury_note"`
	OnDisabledList    bool        `json:"on_disabled_list"`
	NHLPlayerID       pgtype.Int8 `json:"nhl_player_id"`
}

func yahooLeaguePlayersTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_league_players",
			mcp.WithDescription("Get a Yahoo fantasy league's draftable player pool as last imported from Yahoo: each player's "+
				"eligible positions, Yahoo status code (DTD, O, IR, IR-LT, NA, SUSP, ...; empty when Yahoo lists none, which "+
				"does not confirm the player is healthy), injury note, and nhl_player_id, the matched NHL player (empty when "+
				"no NHL player is matched, e.g. a rookie without NHL history). A header line gives the pool size, how many "+
				"players are unmatched, when Yahoo was fetched, how many players match the filters and how many are "+
				"returned; rows follow as CSV ordered by Yahoo player ID. A league whose pool was never imported has pool_players=0, "+
				"as can a temporary stand-in league (see get_yahoo_league_settings), whose draft rankings use NHL rosters instead."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
			mcp.WithString(leaguePositionsArg, mcp.Description(fmt.Sprintf(
				"Comma-separated eligible positions, any of which must match (%s), e.g. \"C,LW\"",
				strings.Join(leaguePlayerPositions, ", ")))),
			mcp.WithString(leagueStatusArg, mcp.Description(
				"Comma-separated Yahoo status codes, any of which must match (e.g. \"IR,IR-LT\"), or \"any\" for every player with a status")),
			mcp.WithBoolean(leagueUnmatchedOnlyArg, mcp.Description("Only players not matched to an NHL player (default false)")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max rows (default %d, max %d)",
				defaultLeaguePlayersLimit, maxLeaguePlayersLimit))),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			filter, errResult := readLeaguePlayersFilter(req)
			if errResult != nil {
				return errResult, nil
			}
			league, pool, err := loadLeaguePool(ctx, q, leagueID)
			if errors.Is(err, errLeagueNotImported) {
				return unknownLeagueResult(leagueID), nil
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := selectLeaguePlayers(league, pool, filter).text()
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		}),
	}
}

// loadLeaguePool reads the league row, whose key the pool is stored under,
// and the league's pool with each player's NHL match.
func loadLeaguePool(ctx context.Context, q yahooQueries, leagueID int32) (sqlcdb.YahooLeague, []sqlcdb.ListYahooLeaguePlayersWithNHLRow, error) {
	league, err := q.GetYahooLeague(ctx, leagueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcdb.YahooLeague{}, nil, errLeagueNotImported
	}
	if err != nil {
		return sqlcdb.YahooLeague{}, nil, fmt.Errorf("load league %d: %w", leagueID, err)
	}
	pool, err := q.ListYahooLeaguePlayersWithNHL(ctx, league.LeagueKey)
	if err != nil {
		return sqlcdb.YahooLeague{}, nil, fmt.Errorf("load player pool of league %s: %w", league.LeagueKey, err)
	}
	return league, pool, nil
}

// leaguePlayersFilter selects pool players. Each list matches when any of
// its values does; the filters combine with AND.
type leaguePlayersFilter struct {
	positions     []string // upper-cased, from leaguePlayerPositions
	statuses      []string // upper-cased Yahoo status codes
	anyStatus     bool
	unmatchedOnly bool
	limit         int32
}

// readLeaguePlayersFilter reads and validates the filter arguments.
func readLeaguePlayersFilter(req mcp.CallToolRequest) (leaguePlayersFilter, *mcp.CallToolResult) {
	var f leaguePlayersFilter
	var errResult *mcp.CallToolResult
	if f.positions, errResult = readPositionsFilter(req); errResult != nil {
		return f, errResult
	}
	// Status codes are not validated like positions: Yahoo's set is open,
	// and a code no pool player has (nobody suspended) is a real answer.
	status, errResult := optionalString(req, leagueStatusArg)
	if errResult != nil {
		return f, errResult
	}
	f.statuses = splitFilterList(status)
	f.anyStatus = slices.Contains(f.statuses, anyStatusFilter)
	if f.unmatchedOnly, errResult = optionalBool(req, leagueUnmatchedOnlyArg); errResult != nil {
		return f, errResult
	}
	f.limit, errResult = boundedLimitOrDefault(req, leaguePlayersLimitParam, defaultLeaguePlayersLimit)
	return f, errResult
}

// readPositionsFilter reads the positions argument, refusing a position the
// pool's eligibility never holds so a typo is not answered with no rows.
func readPositionsFilter(req mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	text, errResult := optionalString(req, leaguePositionsArg)
	if errResult != nil {
		return nil, errResult
	}
	positions := splitFilterList(text)
	for _, position := range positions {
		if !slices.Contains(leaguePlayerPositions, position) {
			return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %q: want any of %s",
				leaguePositionsArg, position, strings.Join(leaguePlayerPositions, ", ")))
		}
	}
	return positions, nil
}

// splitFilterList splits a comma-separated argument into trimmed,
// upper-cased, distinct values.
func splitFilterList(text string) []string {
	var values []string
	for _, field := range strings.Split(text, filterListSeparator) {
		value := strings.ToUpper(strings.TrimSpace(field))
		if value != "" && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}

func (f leaguePlayersFilter) matches(p sqlcdb.ListYahooLeaguePlayersWithNHLRow) bool {
	if f.unmatchedOnly && p.NhlPlayerID.Valid {
		return false
	}
	if len(f.positions) > 0 && !slices.ContainsFunc(p.EligiblePositions, func(position string) bool {
		return slices.Contains(f.positions, strings.ToUpper(position))
	}) {
		return false
	}
	return f.matchesStatus(p.Status)
}

func (f leaguePlayersFilter) matchesStatus(status string) bool {
	switch {
	case len(f.statuses) == 0:
		return true
	case f.anyStatus && status != "":
		return true
	default:
		return slices.Contains(f.statuses, strings.ToUpper(status))
	}
}

// leaguePlayers is what get_yahoo_league_players reports for one league.
type leaguePlayers struct {
	league    sqlcdb.YahooLeague
	poolSize  int
	unmatched int
	fetchedAt time.Time // latest Yahoo fetch; zero for an empty pool
	matching  int
	rows      []leaguePlayerRow
}

// selectLeaguePlayers counts the whole pool and keeps the first filter.limit
// players that match the filter.
func selectLeaguePlayers(league sqlcdb.YahooLeague, pool []sqlcdb.ListYahooLeaguePlayersWithNHLRow, filter leaguePlayersFilter) leaguePlayers {
	rows := make([]leaguePlayerRow, 0, min(len(pool), int(filter.limit)))
	result := leaguePlayers{league: league, poolSize: len(pool), rows: rows}
	for _, p := range pool {
		if !p.NhlPlayerID.Valid {
			result.unmatched++
		}
		if p.FetchedAt.Time.After(result.fetchedAt) {
			result.fetchedAt = p.FetchedAt.Time
		}
		if !filter.matches(p) {
			continue
		}
		result.matching++
		if len(result.rows) < int(filter.limit) {
			result.rows = append(result.rows, leaguePlayerRowOf(p))
		}
	}
	return result
}

func leaguePlayerRowOf(p sqlcdb.ListYahooLeaguePlayersWithNHLRow) leaguePlayerRow {
	return leaguePlayerRow{
		YahooPlayerID: p.PlayerID, PlayerKey: p.PlayerKey, Name: p.FullName, Team: p.EditorialTeamAbbr,
		PrimaryPosition: p.PrimaryPosition, EligiblePositions: strings.Join(p.EligiblePositions, eligiblePositionsSeparator),
		PositionType: p.PositionType, Status: p.Status, StatusFull: p.StatusFull, InjuryNote: p.InjuryNote,
		OnDisabledList: p.OnDisabledList, NHLPlayerID: p.NhlPlayerID,
	}
}

// text renders the header line and the player rows.
func (r leaguePlayers) text() (string, error) {
	rows, err := csvSection(leaguePlayersSection, r.rows)
	if err != nil {
		return "", err
	}
	return r.header().String() + "\n" + rows, nil
}

// header identifies the league and summarizes the pool and the selection.
func (r leaguePlayers) header() metadataHeader {
	var h metadataHeader
	h.add("league_id", r.league.ID)
	h.add("league_key", r.league.LeagueKey)
	h.add("season", r.league.Season)
	h.add("pool_players", r.poolSize)
	h.add("unmatched_players", r.unmatched)
	if !r.fetchedAt.IsZero() {
		h.add("fetched_at", r.fetchedAt.UTC().Format(time.RFC3339))
	}
	h.add("matching", r.matching)
	h.add("returned", len(r.rows))
	return h
}

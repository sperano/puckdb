package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	teamValuesToolName = "get_yahoo_team_player_values"
	teamValuesFromArg  = "from"
	teamValuesToArg    = "to"
	activeOnlyArg      = "active_only"
)

// inactiveRosterSlots are the slots whose days active_only leaves out and
// active_days does not count.
var inactiveRosterSlots = []string{draft.SlotBench, draft.SlotInjuredReserve, draft.SlotInjuredPlus, draft.SlotNotActive}

// errNoTeamValueRosters means the league has no imported roster.
var errNoTeamValueRosters = errors.New("no roster of this league has been imported")

// draftRankingReader is the draft ranking read path the team values tool
// takes its projection from; *draftrank.Service satisfies it.
type draftRankingReader interface {
	Read(context.Context, draftrank.LeagueRef, uuid.UUID) (draftrank.Read, error)
}

func yahooTeamPlayerValuesTool(q yahooQueries, guard leagueGuard, rankings draftRankingReader) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(teamValuesToolName,
			mcp.WithDescription("Rank one Yahoo fantasy team's players by what they produced in the league's own scoring "+
				"categories, to answer who is the best or weakest player on a team. Prints a '# key=value' header (league, team, "+
				"the from/to dates used, requested_to when to was moved back to the latest imported roster, active_only, the "+
				"number of compared players, the draft projection used or projection=none, unsupported categories), then CSV "+
				"sections: '# categories' (each category's columns, direction, and for a ratio category its numerator and "+
				"denominator), '# skaters' and '# goalies' (one row per matched player the team rostered in the window: "+
				"on_roster false for a player who left the team before to; days on the team, days in an active slot, games "+
				"played on those days, then per category the actual total over those days, its z-score against every "+
				"player of the same position type rostered in the league on to over the same window (positive helps the "+
				"team, a lower-is-better category is flipped, a ratio category is scored by volume), and with a draft "+
				"projection the projected pace for the games played; value is the sum of the z-scores and rank orders the "+
				"players still on the roster by it, best first; a player on the team for fewer days of the window has smaller "+
				"totals, so read days with value), and '# unmatched' (players with no NHL match, so no stats). "+
				"To find replacements, use get_unrostered_skaters or get_unrostered_goalies."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
			mcp.WithNumber(yahooTeamIDParam.name, mcp.Required(), mcp.Description("Yahoo team ID")),
			mcp.WithString(teamValuesFromArg, mcp.Description("First day, YYYY-MM-DD (default: the league's season start)")),
			mcp.WithString(teamValuesToArg, mcp.Description("Last day and the roster to rank, YYYY-MM-DD (default and "+
				"latest: the league's latest imported roster). "+rosterFreshnessNote)),
			mcp.WithBoolean(activeOnlyArg, mcp.Description("Count only days in an active slot, not BN, IR, IR+ or NA (default false)")),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			args, errResult := teamValuesArgsOf(req)
			if errResult != nil {
				return errResult, nil
			}
			values, err := loadTeamValues(ctx, q, rankings, leagueID, args)
			switch {
			case errors.Is(err, errLeagueNotImported):
				return unknownLeagueResult(leagueID), nil
			case err != nil:
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := values.text()
			if err != nil {
				return nil, err
			}
			if len(text) > maxTeamValuesOutputBytes {
				return mcp.NewToolResultError(fmt.Sprintf("team values result is %d bytes; maximum is %d: shorten the window with %s",
					len(text), maxTeamValuesOutputBytes, teamValuesFromArg)), nil
			}
			return mcp.NewToolResultText(text), nil
		}),
	}
}

// teamValuesArgs are the arguments besides league_id.
type teamValuesArgs struct {
	teamID     int32
	from, to   pgtype.Date
	activeOnly bool
}

func teamValuesArgsOf(req mcp.CallToolRequest) (teamValuesArgs, *mcp.CallToolResult) {
	var args teamValuesArgs
	var errResult *mcp.CallToolResult
	if args.teamID, errResult = requireInt[int32](req, yahooTeamIDParam); errResult != nil {
		return args, errResult
	}
	if args.from, errResult = optionalDate(req, teamValuesFromArg); errResult != nil {
		return args, errResult
	}
	if args.to, errResult = optionalDate(req, teamValuesToArg); errResult != nil {
		return args, errResult
	}
	args.activeOnly, errResult = optionalBool(req, activeOnlyArg)
	return args, errResult
}

// teamValues is what get_yahoo_team_player_values reports.
type teamValues struct {
	league      sqlcdb.YahooLeague
	team        sqlcdb.YahooTeam
	args        teamValuesArgs
	requestedTo pgtype.Date
	latest      pgtype.Date
	categories  []valueCategory
	unsupported []string
	rows        []sqlcdb.ListYahooLeaguePlayerWindowTotalsRow
	projection  *draftrank.ProjectionInfo
	projected   map[int64]statTotals
}

func loadTeamValues(ctx context.Context, q yahooQueries, rankings draftRankingReader, leagueID int32, args teamValuesArgs) (teamValues, error) {
	league, err := loadLeague(ctx, q, leagueID)
	if err != nil {
		return teamValues{}, err
	}
	team, err := q.GetYahooTeam(ctx, sqlcdb.GetYahooTeamParams{LeagueID: leagueID, ID: args.teamID})
	if errors.Is(err, pgx.ErrNoRows) {
		return teamValues{}, fmt.Errorf("team %d not found in league %d (use get_yahoo_teams_by_league)", args.teamID, leagueID)
	}
	if err != nil {
		return teamValues{}, fmt.Errorf("load team %d of league %d: %w", args.teamID, leagueID, err)
	}
	v := teamValues{league: league, team: team, args: args}
	if err := v.resolveWindow(ctx, q); err != nil {
		return teamValues{}, err
	}
	categories, err := q.GetYahooLeagueStatCategories(ctx, leagueID)
	if err != nil {
		return teamValues{}, fmt.Errorf("load stat categories of league %d: %w", leagueID, err)
	}
	v.categories, v.unsupported = valueCategories(categories)
	v.rows, err = q.ListYahooLeaguePlayerWindowTotals(ctx, sqlcdb.ListYahooLeaguePlayerWindowTotalsParams{
		LeagueKey: league.LeagueKey, InactiveSlots: inactiveRosterSlots, LeagueID: leagueID,
		FromDate: v.args.from, ToDate: v.args.to, ActiveOnly: args.activeOnly,
		MinimumShutoutToiSeconds: projection.DefaultGoalieShutoutMinTOI,
	})
	if err != nil {
		return teamValues{}, fmt.Errorf("load player totals of league %d: %w", leagueID, err)
	}
	if err := v.loadProjection(ctx, q, rankings); err != nil {
		return teamValues{}, err
	}
	return v, nil
}

// resolveWindow fills the default dates and moves a to after the latest
// imported roster back to it, keeping the requested one for the header.
func (v *teamValues) resolveWindow(ctx context.Context, q yahooQueries) error {
	latest, err := q.GetLatestYahooLeagueRosterDate(ctx, v.league.ID)
	if err != nil {
		return fmt.Errorf("load latest roster date of league %d: %w", v.league.ID, err)
	}
	if !latest.Valid {
		return fmt.Errorf("league %d: %w", v.league.ID, errNoTeamValueRosters)
	}
	v.latest = latest
	if !v.args.to.Valid || v.args.to.Time.After(latest.Time) {
		v.requestedTo, v.args.to = v.args.to, latest
	}
	if !v.args.from.Valid {
		v.args.from = v.league.StartDate
	}
	if !v.args.from.Valid {
		return fmt.Errorf("league %d has no season start date: pass %s", v.league.ID, teamValuesFromArg)
	}
	if v.args.from.Time.After(v.args.to.Time) {
		return fmt.Errorf("%s %s is after %s %s", teamValuesFromArg, formatDate(v.args.from),
			teamValuesToArg, formatDate(v.args.to))
	}
	return nil
}

// loadProjection reads the projection behind the league's served draft
// ranking snapshot for the team's matched players; a league without a
// ranking snapshot (or whose rules were never imported) has none.
func (v *teamValues) loadProjection(ctx context.Context, q yahooQueries, rankings draftRankingReader) error {
	read, err := rankings.Read(ctx, draftrank.LeagueRef{Season: int(v.league.Season), LeagueID: int(v.league.ID)}, uuid.Nil)
	if errors.Is(err, draftrank.ErrUnknownLeague) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load draft ranking of league %d: %w", v.league.ID, err)
	}
	if read.Snapshot == nil || read.Snapshot.Projection.SnapshotID == uuid.Nil {
		return nil
	}
	info := read.Snapshot.Projection
	v.projection = &info
	var ids []int64
	for _, row := range v.rows {
		if row.TeamID == v.team.ID && row.NhlPlayerID.Valid {
			ids = append(ids, row.NhlPlayerID.Int64)
		}
	}
	rows, err := q.ListProjectionValuesByNHLPlayers(ctx, sqlcdb.ListProjectionValuesByNHLPlayersParams{
		SnapshotID: pgtype.UUID{Bytes: info.SnapshotID, Valid: true}, NhlPlayerIds: ids,
	})
	if err != nil {
		return fmt.Errorf("load projection %s: %w", info.SnapshotID, err)
	}
	v.projected = projectedTotals(rows)
	return nil
}

// projectedTotals keeps each player's first projection key (the rows are
// ordered by key), so a player projected twice is not summed.
func projectedTotals(rows []sqlcdb.ListProjectionValuesByNHLPlayersRow) map[int64]statTotals {
	keys := make(map[int64]string)
	out := make(map[int64]statTotals)
	for _, row := range rows {
		key, seen := keys[row.NhlPlayerID]
		if !seen {
			keys[row.NhlPlayerID], key = row.PlayerKey, row.PlayerKey
			out[row.NhlPlayerID] = statTotals{}
		}
		if key == row.PlayerKey {
			out[row.NhlPlayerID][projection.Stat(row.Stat)] = row.Mean
		}
	}
	return out
}

// teamPlayers splits the team's lines into matched players by position
// type and unmatched players.
func (v teamValues) teamPlayers() (matched map[string][]windowPlayer, unmatched []windowPlayer) {
	matched = make(map[string][]windowPlayer, len(valuePositionTypes))
	for _, row := range v.rows {
		if row.TeamID != v.team.ID {
			continue
		}
		player := windowPlayerOf(row)
		if !player.nhlPlayerID.Valid {
			unmatched = append(unmatched, player)
			continue
		}
		matched[player.positionType] = append(matched[player.positionType], player)
	}
	return matched, unmatched
}

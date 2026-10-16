package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// transactionsToolName is the tool listing a league's transactions.
	transactionsToolName = "get_yahoo_transactions"
	// defaultTransactionsLimit is the number of transactions returned
	// without a limit argument.
	defaultTransactionsLimit = 50
	// maxTransactionsLimit caps limit to bound the result size; a trade
	// adds one row per player, so the rows can outnumber the transactions.
	maxTransactionsLimit = 500
	// Arguments of get_yahoo_transactions besides league_id and limit.
	transactionTypeArg  = "type"
	transactionSinceArg = "since"
	// teamMoveSide is the source or destination type of a move from or to
	// a fantasy team, as opposed to freeagents or waivers.
	teamMoveSide = "team"
)

// transactionsLimitParam is the limit of get_yahoo_transactions.
var transactionsLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxTransactionsLimit}

// transactionMoveRow is one player moved by a transaction. Rows of the same
// transaction share its time, key, type and status.
type transactionMoveRow struct {
	Time            string      `json:"time"`
	TransactionKey  string      `json:"transaction_key"`
	TransactionType string      `json:"transaction_type"`
	Status          pgtype.Text `json:"status"`
	MoveType        string      `json:"move_type"`
	YahooPlayerID   int32       `json:"yahoo_player_id"`
	NHLPlayerID     pgtype.Int8 `json:"nhl_player_id"`
	PlayerName      string      `json:"player_name"`
	From            string      `json:"from"`
	FromTeamID      pgtype.Int4 `json:"from_team_id"`
	To              string      `json:"to"`
	ToTeamID        pgtype.Int4 `json:"to_team_id"`
}

func yahooTransactionsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(transactionsToolName,
			mcp.WithDescription("List a Yahoo fantasy league's transactions, newest first, as CSV with one row per player moved: "+
				"time (UTC), transaction_key (shared by the rows of one transaction), transaction_type (add, drop, add/drop, "+
				"trade, commish, ...), status, move_type (add, drop or trade), the player (yahoo_player_id, nhl_player_id when "+
				"matched, name), and where the player went from and to: a fantasy team name with its team_id, or freeagents/waivers. "+
				"limit counts transactions, not rows, so a trade or an add/drop is never cut in half. "+
				"Transactions without a recorded time come last and are dropped when since is given."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
			mcp.WithString(transactionTypeArg, mcp.Description(
				"Only transactions of this type or players moved this way, e.g. \"trade\", \"add/drop\", or \"add\" "+
					"(which also returns the add of every add/drop)")),
			mcp.WithString(transactionSinceArg, mcp.Description(
				"Only transactions at or after this time: YYYY-MM-DD (midnight UTC) or an RFC 3339 timestamp")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max transactions (default %d, max %d)",
				defaultTransactionsLimit, maxTransactionsLimit))),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			params, errResult := readTransactionsParams(req, leagueID)
			if errResult != nil {
				return errResult, nil
			}
			if _, err := q.GetYahooLeague(ctx, leagueID); errors.Is(err, pgx.ErrNoRows) {
				return unknownLeagueResult(leagueID), nil
			} else if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			moves, err := q.ListYahooTransactionMoves(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(transactionMoveRows(moves))
		}),
	}
}

// readTransactionsParams reads and validates the filter arguments.
func readTransactionsParams(req mcp.CallToolRequest, leagueID int32) (sqlcdb.ListYahooTransactionMovesParams, *mcp.CallToolResult) {
	params := sqlcdb.ListYahooTransactionMovesParams{LeagueID: leagueID}
	moveType, errResult := optionalString(req, transactionTypeArg)
	if errResult != nil {
		return params, errResult
	}
	// Yahoo writes every type in lower case; the set is open, so it is not
	// validated.
	if moveType = strings.ToLower(strings.TrimSpace(moveType)); moveType != "" {
		params.MoveType = pgtype.Text{String: moveType, Valid: true}
	}
	if params.SinceTs, errResult = optionalSince(req); errResult != nil {
		return params, errResult
	}
	params.RowLimit, errResult = boundedLimitOrDefault(req, transactionsLimitParam, defaultTransactionsLimit)
	return params, errResult
}

// optionalSince reads the since argument as Unix seconds; absent or empty
// is NULL. A date means midnight UTC, the zone of the time column.
func optionalSince(req mcp.CallToolRequest) (pgtype.Int8, *mcp.CallToolResult) {
	raw, present := argument(req, transactionSinceArg)
	if !present || raw == "" {
		return pgtype.Int8{}, nil
	}
	if text, isString := raw.(string); isString {
		for _, layout := range []string{time.DateOnly, time.RFC3339} {
			if at, err := time.Parse(layout, text); err == nil {
				return pgtype.Int8{Int64: at.Unix(), Valid: true}, nil
			}
		}
	}
	return pgtype.Int8{}, mcp.NewToolResultError(fmt.Sprintf(
		"invalid %s %s: want YYYY-MM-DD or an RFC 3339 timestamp", transactionSinceArg, formatRaw(raw)))
}

func transactionMoveRows(moves []sqlcdb.ListYahooTransactionMovesRow) []transactionMoveRow {
	rows := make([]transactionMoveRow, len(moves))
	for i, m := range moves {
		rows[i] = transactionMoveRow{
			Time:            transactionTime(m.Timestamp),
			TransactionKey:  m.TransactionKey,
			TransactionType: m.TransactionType,
			Status:          m.Status,
			MoveType:        m.MoveType,
			YahooPlayerID:   m.YahooPlayerID,
			NHLPlayerID:     m.NhlPlayerID,
			PlayerName:      m.PlayerName,
			From:            moveSide(m.SourceType, m.SourceTeamName, m.SourceTeamKey),
			FromTeamID:      m.SourceTeamID,
			To:              moveSide(m.DestinationType, m.DestinationTeamName, m.DestinationTeamKey),
			ToTeamID:        m.DestinationTeamID,
		}
	}
	return rows
}

// transactionTime formats Yahoo's Unix timestamp as RFC 3339 UTC; empty
// when Yahoo gave none.
func transactionTime(ts pgtype.Int8) string {
	if !ts.Valid {
		return ""
	}
	return time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339)
}

// moveSide names one end of a move: the fantasy team's name, its key when
// the team was not imported, or Yahoo's side type (freeagents, waivers).
func moveSide(sideType, teamName, teamKey string) string {
	switch {
	case teamName != "":
		return teamName
	case sideType == teamMoveSide && teamKey != "":
		return teamKey
	default:
		return sideType
	}
}

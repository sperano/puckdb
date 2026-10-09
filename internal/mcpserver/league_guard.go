package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// leagueKeyListSeparator separates league keys in --mcp-yahoo-leagues.
	leagueKeyListSeparator = ","
	// leagueKeyMarker separates the game key and league ID of a Yahoo
	// league key ("465.l.1001").
	leagueKeyMarker = ".l."
	// leagueIDArg is the argument every league-scoped Yahoo tool takes.
	leagueIDArg = "league_id"
)

// leagueLookup finds a league by its numeric ID; *sqlcdb.Queries satisfies it.
type leagueLookup interface {
	GetYahooLeague(ctx context.Context, id int32) (sqlcdb.YahooLeague, error)
}

// leagueGuard decides which Yahoo leagues the server serves. With an empty
// allowlist it serves every league; otherwise only the listed league keys.
// It checks each call against the database rather than resolving keys once
// at startup, so leagues imported later are judged by their own key.
type leagueGuard struct {
	allowed map[string]bool
	leagues leagueLookup
}

func newLeagueGuard(leagues leagueLookup, leagueKeys []string) leagueGuard {
	allowed := make(map[string]bool, len(leagueKeys))
	for _, key := range leagueKeys {
		allowed[key] = true
	}
	return leagueGuard{allowed: allowed, leagues: leagues}
}

// restricted reports whether the guard serves only listed leagues.
func (g leagueGuard) restricted() bool {
	return len(g.allowed) > 0
}

// allow reports whether the server serves leagueID. An unrestricted guard
// allows every ID without a query. A restricted guard treats a missing
// league like one outside the allowlist.
func (g leagueGuard) allow(ctx context.Context, leagueID int32) (bool, error) {
	if !g.restricted() {
		return true, nil
	}
	league, err := g.leagues.GetYahooLeague(ctx, leagueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return g.allowed[league.LeagueKey], nil
}

// filter drops the leagues the guard does not serve.
func (g leagueGuard) filter(leagues []sqlcdb.YahooLeague) []sqlcdb.YahooLeague {
	if !g.restricted() {
		return leagues
	}
	served := make([]sqlcdb.YahooLeague, 0, len(leagues))
	for _, league := range leagues {
		if g.allowed[league.LeagueKey] {
			served = append(served, league)
		}
	}
	return served
}

// leagueHandler handles a tool call for a league the guard already allowed.
type leagueHandler func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error)

// scoped wraps a league-scoped handler: it reads league_id, refuses a
// league the guard does not serve, and only then runs h. A refused league
// gets the same answer as a missing one, so a client cannot learn which
// other leagues exist.
func (g leagueGuard) scoped(h leagueHandler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		leagueID, errResult := requireInt[int32](req, leagueIDParam)
		if errResult != nil {
			return errResult, nil
		}
		ok, err := g.allow(ctx, leagueID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if !ok {
			return unknownLeagueResult(leagueID), nil
		}
		return h(ctx, req, leagueID)
	}
}

// unknownLeagueResult is the one answer for a league the server does not
// serve, whether it is missing or outside the allowlist.
func unknownLeagueResult(leagueID int32) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("unknown %s %d", leagueIDArg, leagueID))
}

// ParseLeagueKeys reads a comma-separated list of Yahoo league keys such as
// "465.l.1001,465.l.2002". Keys, not bare league IDs: a Yahoo league ID is
// unique only within one season, so an ID could later match another
// season's league. An empty list is valid and serves every league.
func ParseLeagueKeys(list string) ([]string, error) {
	var keys []string
	seen := make(map[string]bool)
	for _, field := range strings.Split(list, leagueKeyListSeparator) {
		key := strings.TrimSpace(field)
		if key == "" || seen[key] {
			continue
		}
		if err := validateLeagueKey(key); err != nil {
			return nil, err
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys, nil
}

// validateLeagueKey checks the "<game key>.l.<league ID>" shape.
func validateLeagueKey(key string) error {
	gameKey, id, found := strings.Cut(key, leagueKeyMarker)
	if !found || gameKey == "" {
		return fmt.Errorf("invalid league key %q: want <game key>%s<league ID>, e.g. 465.l.1001", key, leagueKeyMarker)
	}
	if n, err := strconv.Atoi(id); err != nil || n <= 0 {
		return fmt.Errorf("invalid league key %q: league ID %q is not a positive number", key, id)
	}
	return nil
}

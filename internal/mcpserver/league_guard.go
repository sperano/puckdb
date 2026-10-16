package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draftrank"
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
	// leagueArg is the key-or-ID argument used by draft tools.
	leagueArg = "league"
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

// allowKey reports whether the guard serves leagueKey. Unlike allow, it does
// not need a database lookup because the allowlist itself contains keys.
func (g leagueGuard) allowKey(leagueKey string) bool {
	return !g.restricted() || g.allowed[leagueKey]
}

// leagueKeys returns the allowlisted league keys in sorted order; empty for
// an unrestricted guard.
func (g leagueGuard) leagueKeys() []string {
	return slices.Sorted(maps.Keys(g.allowed))
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

// leagueKeyResolver resolves a full key or a season-scoped numeric ID to the
// complete identity needed by key-based authorization.
type leagueKeyResolver interface {
	ResolveLeague(ctx context.Context, ref draftrank.LeagueRef) (draftrank.LeagueRef, error)
}

type leagueKeyHandler func(context.Context, mcp.CallToolRequest, draftrank.LeagueRef) (*mcp.CallToolResult, error)

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

// scopedKey wraps a draft tool that takes season and league. Full league keys
// are checked before resolution; numeric IDs are resolved with their season
// and then checked by key. Missing and refused leagues share one response.
func (g leagueGuard) scopedKey(resolver leagueKeyResolver, h leagueKeyHandler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		season, errResult := requiredSeasonYear(req)
		if errResult != nil {
			return errResult, nil
		}
		value, errResult := optionalString(req, leagueArg)
		if errResult != nil {
			return errResult, nil
		}
		if strings.TrimSpace(value) == "" {
			return mcp.NewToolResultError(leagueArg + " is required"), nil
		}
		ref, errResult := g.resolveKey(ctx, resolver, value, season)
		if errResult != nil {
			return errResult, nil
		}
		return h(ctx, req, ref)
	}
}

// resolveKey resolves and authorizes one draft league reference. Callers
// with a plural league argument use this for every requested league.
func (g leagueGuard) resolveKey(ctx context.Context, resolver leagueKeyResolver, value string, season int) (draftrank.LeagueRef, *mcp.CallToolResult) {
	ref, err := draftrank.ParseLeague(value, season)
	if err != nil {
		return draftrank.LeagueRef{}, mcp.NewToolResultError(err.Error())
	}
	if ref.LeagueKey != "" && !g.allowKey(ref.LeagueKey) {
		return draftrank.LeagueRef{}, unknownLeagueRefResult(value)
	}
	ref, err = resolver.ResolveLeague(ctx, ref)
	if errors.Is(err, draftrank.ErrUnknownLeague) {
		return draftrank.LeagueRef{}, unknownLeagueRefResult(value)
	}
	if err != nil {
		return draftrank.LeagueRef{}, mcp.NewToolResultError(err.Error())
	}
	if !g.allowKey(ref.LeagueKey) {
		return draftrank.LeagueRef{}, unknownLeagueRefResult(value)
	}
	return ref, nil
}

// unknownLeagueResult is the one answer for a league the server does not
// serve, whether it is missing or outside the allowlist.
func unknownLeagueResult(leagueID int32) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("unknown %s %d", leagueIDArg, leagueID))
}

func unknownLeagueRefResult(value string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("unknown %s %s", leagueArg, formatRaw(value)))
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

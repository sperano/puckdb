package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// leagueLister lists every imported Yahoo league; *sqlcdb.Queries satisfies it.
type leagueLister interface {
	GetAllYahooLeagues(ctx context.Context) ([]sqlcdb.YahooLeague, error)
}

// ErrLeagueKeysNotImported marks a CheckLeagueKeys failure caused by the
// configuration (a key matching no imported league), as opposed to a failed
// lookup, so the caller can blame the flag only for the former.
var ErrLeagueKeysNotImported = errors.New("configured yahoo league keys not imported")

// CheckLeagueKeys fails when a configured league key matches no imported
// league. A key that matches nothing makes the guard serve no league, so
// every Yahoo tool would refuse every league ("unknown league_id N", or
// `unknown league "<key>"` for the draft tools): a typo or a stale game key
// (it changes every season) must stop the server instead. The yahoo
// toolset alone uses the keys, so nothing is checked without it.
func CheckLeagueKeys(ctx context.Context, leagues leagueLister, opts Options) error {
	if len(opts.YahooLeagues) == 0 || !opts.HasToolset(ToolsetYahoo) {
		return nil
	}
	imported, err := leagues.GetAllYahooLeagues(ctx)
	if err != nil {
		return fmt.Errorf("list yahoo leagues: %w", err)
	}
	known := make(map[string]bool, len(imported))
	for _, league := range imported {
		known[league.LeagueKey] = true
	}
	var missing []string
	for _, key := range opts.YahooLeagues {
		if !known[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s (check the game key; it changes every season)",
			ErrLeagueKeysNotImported, strings.Join(missing, leagueKeyListSeparator))
	}
	return nil
}

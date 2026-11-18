package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// leagueLister lists every imported Yahoo league; *sqlcdb.Queries satisfies it.
type leagueLister interface {
	GetAllYahooLeagues(ctx context.Context) ([]sqlcdb.YahooLeague, error)
}

// CheckLeagueKeys fails when a configured league key matches no imported
// league. A key that matches nothing makes the guard serve no league, so
// every Yahoo tool would answer "unknown league_id": a typo or a stale game
// key (it changes every season) must stop the server instead. The yahoo
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
		return fmt.Errorf("configured yahoo league keys not imported: %s (check the game key; it changes every season)",
			strings.Join(missing, leagueKeyListSeparator))
	}
	return nil
}

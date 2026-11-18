package player

import (
	"context"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
)

// YahooLeaguePlayerLister lists the Yahoo players the authenticated Yahoo API
// imported into yahoo_league_players, one row per Yahoo ID. The scraped
// player pages cover only a fraction of the Yahoo IDs, so these rows seed the
// Yahoo ID pool too.
type YahooLeaguePlayerLister interface {
	ListYahooLeaguePlayersForPool(ctx context.Context) ([]sqlcdb.ListYahooLeaguePlayersForPoolRow, error)
}

var _ YahooLeaguePlayerLister = (*sqlcdb.Queries)(nil)

// yahooPlayerFromLeagueRow converts a yahoo_league_players row to a pool
// entry. The name is split on its first space (store.ParseCombinedName, as
// for the HTML title): the matcher compares the normalized full names first,
// so a compound first name ("Charles Alexis Legault") matches whichever way
// the NHL side splits it. Team is the Yahoo page-style name ("Toronto"),
// derived from the API's abbreviation, because filterByTeam compares names.
// An abbreviation without a known name leaves Team empty. There is no jersey
// number, birth date or image.
func yahooPlayerFromLeagueRow(row sqlcdb.ListYahooLeaguePlayersForPoolRow) store.YahooPlayer {
	first, last := store.ParseCombinedName(row.FullName)
	return store.YahooPlayer{
		YahooID:   store.YahooPlayerID(row.PlayerID),
		FirstName: first,
		LastName:  last,
		Positions: store.ParsePositions(strings.TrimSpace(row.DisplayPosition)),
		Team:      matching.YahooTeamNameForAbbrev(row.EditorialTeamAbbr),
	}
}

// mergeLeaguePlayers returns pages followed by the league players whose
// Yahoo ID has no page: a page carries the jersey number and birth date, so
// it wins on overlap. Pages keep their order; league players follow in row
// order.
func mergeLeaguePlayers(pages []store.YahooPlayer, rows []sqlcdb.ListYahooLeaguePlayersForPoolRow) []store.YahooPlayer {
	seen := make(map[store.YahooPlayerID]struct{}, len(pages))
	for _, p := range pages {
		seen[p.YahooID] = struct{}{}
	}
	merged := make([]store.YahooPlayer, 0, len(pages)+len(rows))
	merged = append(merged, pages...)
	for _, row := range rows {
		p := yahooPlayerFromLeagueRow(row)
		if _, ok := seen[p.YahooID]; ok {
			continue
		}
		seen[p.YahooID] = struct{}{}
		merged = append(merged, p)
	}
	return merged
}

// withLeaguePlayers adds the yahoo_league_players players to pages (see
// mergeLeaguePlayers). Phase 1's save and the pool rebuild both go through
// it, so a rebuilt pool holds the entries phase 1 recorded.
func (a *Activities) withLeaguePlayers(ctx context.Context, pages []store.YahooPlayer) ([]store.YahooPlayer, error) {
	rows, err := a.LeaguePlayers.ListYahooLeaguePlayersForPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("list yahoo league players: %w", err)
	}
	return mergeLeaguePlayers(pages, rows), nil
}

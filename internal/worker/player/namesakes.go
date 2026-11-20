package player

import (
	"context"
	"fmt"
	"strings"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// namesakeSeasons returns the matcher's lookup of the NHL seasons played by
// the other players with landing's normalized name, nil (no seasons step)
// without a Namesakes query.
func (a *Activities) namesakeSeasons(ctx context.Context, landing *nhl.PlayerLanding) matching.NamesakeSeasons {
	if a.Namesakes == nil {
		return nil
	}
	return func() ([][]int, error) {
		rows, err := a.Namesakes.ListNamesakeNHLSeasons(ctx, sqlcdb.ListNamesakeNHLSeasonsParams{
			FirstNameNormalized: matching.NormalizeName(strings.TrimSpace(landing.FirstName.Default)),
			LastNameNormalized:  matching.NormalizeName(strings.TrimSpace(landing.LastName.Default)),
			ExcludePlayerID:     landing.PlayerID.Int64(),
		})
		if err != nil {
			return nil, fmt.Errorf("list NHL namesakes' seasons of player %d: %w", landing.PlayerID.Int64(), err)
		}
		return groupNamesakeSeasons(rows), nil
	}
}

// groupNamesakeSeasons groups rows, ordered by player, into one season set
// per player.
func groupNamesakeSeasons(rows []sqlcdb.ListNamesakeNHLSeasonsRow) [][]int {
	var sets [][]int
	for i, row := range rows {
		if i == 0 || row.PlayerID != rows[i-1].PlayerID {
			sets = append(sets, nil)
		}
		sets[len(sets)-1] = append(sets[len(sets)-1], int(row.StartYear))
	}
	return sets
}

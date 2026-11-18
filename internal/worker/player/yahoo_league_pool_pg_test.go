package player

// PostgreSQL-backed coverage for the ListYahooLeaguePlayersForPool query.
// Skips unless PUCKDB_TEST_PG_URL is set.

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestListYahooLeaguePlayersForPool_NewestSeasonWinsPerYahooID(t *testing.T) {
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	ctx := context.Background()
	const insert = `INSERT INTO yahoo_league_players
		(league_key, season, league_id, game_key, player_id, player_key, full_name,
		 editorial_team_abbr, display_position, eligible_positions, fetched_at)
		VALUES ($1, $2, 1, 1, $3, 'k', $4, $5, 'C', '{}', now())`
	for _, r := range []struct {
		league string
		season int
		id     int
		name   string
		team   string
	}{
		{"nhl.l.1", 2023, 6743, "Connor McDavid", "OLD"},
		{"nhl.l.2", 2025, 6743, "Connor McDavid", "EDM"},
		{"nhl.l.2", 2025, 7000, "Mitch Marner", "TOR"},
		{"nhl.l.2", 2025, 7001, "", "TOR"},
	} {
		_, err := pool.Exec(ctx, insert, r.league, r.season, r.id, r.name, r.team)
		require.NoError(t, err)
	}

	rows, err := sqlcdb.New(pool).ListYahooLeaguePlayersForPool(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 2, "one row per Yahoo ID, nameless rows left out")
	require.Equal(t, int32(6743), rows[0].PlayerID)
	require.Equal(t, "EDM", rows[0].EditorialTeamAbbr)
	require.Equal(t, int32(7000), rows[1].PlayerID)
}

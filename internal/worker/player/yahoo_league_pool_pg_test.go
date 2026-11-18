package player

// PostgreSQL-backed coverage for the ListYahooLeaguePlayersForPool query.
// Skips unless PUCKDB_TEST_PG_URL is set.

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestListYahooLeaguePlayersForPool_NewestSeasonThenNewestFetchWinsPerYahooID(t *testing.T) {
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	ctx := context.Background()
	const insert = `INSERT INTO yahoo_league_players
		(league_key, season, league_id, game_key, player_id, player_key, full_name,
		 editorial_team_abbr, display_position, eligible_positions, fetched_at)
		VALUES ($1, $2, 1, 1, $3, 'k', $4, $5, 'C', '{}', $6)`
	earlier := time.Date(2025, time.October, 1, 12, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)
	for _, r := range []struct {
		league  string
		season  int
		id      int
		name    string
		team    string
		fetched time.Time
	}{
		{"nhl.l.1", 2023, 6743, "Connor McDavid", "OLD", later},
		{"nhl.l.2", 2025, 6743, "Connor McDavid", "EDM", earlier},
		{"nhl.l.2", 2025, 7000, "Mitch Marner", "TOR", later},
		{"nhl.l.3", 2025, 7000, "Mitch Marner", "VGK", earlier},
		{"nhl.l.2", 2025, 7001, "", "TOR", later},
		{"nhl.l.4", 2026, 6743, "", "NEW", later},
	} {
		_, err := pool.Exec(ctx, insert, r.league, r.season, r.id, r.name, r.team, r.fetched)
		require.NoError(t, err)
	}

	rows, err := sqlcdb.New(pool).ListYahooLeaguePlayersForPool(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 2, "one row per Yahoo ID, nameless rows left out")
	require.Equal(t, int32(6743), rows[0].PlayerID)
	require.Equal(t, "EDM", rows[0].EditorialTeamAbbr, "the newest named season wins over a later fetch of an older one; a newer nameless row is skipped before picking")
	require.Equal(t, int32(7000), rows[1].PlayerID)
	require.Equal(t, "TOR", rows[1].EditorialTeamAbbr, "in one season, the later fetch wins")
}

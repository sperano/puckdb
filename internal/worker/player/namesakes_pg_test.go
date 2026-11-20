package player

// PostgreSQL-backed coverage for the ListNamesakeNHLSeasons and
// ListPlayerIDsWithYahooID queries. Skips
// unless PUCKDB_TEST_PG_URL is set.

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

// The query lists, per other player with the same normalized name, the
// start years of the NHL regular-season and playoff seasons with a game.
func TestListNamesakeNHLSeasons(t *testing.T) {
	pool := pgtest.Open(t, "players")
	// Packages share the database: leave no player for the next one.
	t.Cleanup(func() { pgtest.Truncate(t, pool, "players") })
	ctx := context.Background()
	for _, p := range []struct {
		id          int64
		first, last string
	}{
		{8451789, "petr", "svoboda"},
		{8467510, "petr", "svoboda"},
		{8471000, "petr", "svobodova"},
		{8472000, "jan", "svoboda"},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO players (id, first_name, last_name, first_name_normalized, last_name_normalized)
			VALUES ($1, $2, $3, $2, $3)`, p.id, p.first, p.last)
		require.NoError(t, err)
	}
	for _, st := range []struct {
		player   int64
		season   int
		gameType string
		league   string
		games    int
	}{
		{8451789, 19992000, "regular_season", "NHL", 70},
		{8451789, 20002001, "regular_season", "NHL", 40},
		{8451789, 20002001, "playoffs", "NHL", 5},
		{8451789, 20052006, "playoffs", "NHL", 3},
		{8451789, 19831984, "regular_season", "Czech", 40},
		{8451789, 19981999, "preseason", "NHL", 3},
		{8451789, 19971998, "regular_season", "NHL", 0},
		{8467510, 20002001, "regular_season", "NHL", 18},
		{8471000, 20052006, "regular_season", "NHL", 10},
		{8472000, 20052006, "regular_season", "NHL", 10},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO player_season_totals
			(player_id, season, game_type, league_abbrev, team_name, games_played)
			VALUES ($1, $2, $3, $4, 'Team', $5)`, st.player, st.season, st.gameType, st.league, st.games)
		require.NoError(t, err)
	}

	rows, err := sqlcdb.New(pool).ListNamesakeNHLSeasons(ctx, sqlcdb.ListNamesakeNHLSeasonsParams{
		FirstNameNormalized: "petr", LastNameNormalized: "svoboda", ExcludePlayerID: 8467510,
	})

	require.NoError(t, err)
	require.Equal(t, []sqlcdb.ListNamesakeNHLSeasonsRow{
		{PlayerID: 8451789, StartYear: 1999},
		{PlayerID: 8451789, StartYear: 2000},
		{PlayerID: 8451789, StartYear: 2005},
	}, rows, "a playoffs-only season is listed; the excluded player, other names, other leagues, preseason and seasons without a game are left out; a season is listed once")
}

// The query returns the asked players that have a Yahoo ID, ordered by player ID.
func TestListPlayerIDsWithYahooID(t *testing.T) {
	pool := pgtest.Open(t, "players")
	t.Cleanup(func() { pgtest.Truncate(t, pool, "players") })
	ctx := context.Background()
	for _, p := range []struct {
		id      int64
		yahooID *int64
	}{
		{3, ptr(int64(6300))},
		{1, ptr(int64(6100))},
		{2, nil},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'a', 'b')`, p.id, p.yahooID)
		require.NoError(t, err)
	}

	ids, err := sqlcdb.New(pool).ListPlayerIDsWithYahooID(ctx, []int64{3, 2, 4, 1})

	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, ids, "a player without a Yahoo ID and an unknown ID are left out")
}

func ptr[T any](v T) *T { return &v }

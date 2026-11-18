package player

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// fakeLeaguePlayers is a YahooLeaguePlayerLister returning fixed rows.
type fakeLeaguePlayers struct {
	rows []sqlcdb.ListYahooLeaguePlayersForPoolRow
	err  error
}

func (f fakeLeaguePlayers) ListYahooLeaguePlayersForPool(context.Context) ([]sqlcdb.ListYahooLeaguePlayersForPoolRow, error) {
	return f.rows, f.err
}

func leagueRow(id int32, fullName, team, position string) sqlcdb.ListYahooLeaguePlayersForPoolRow {
	return sqlcdb.ListYahooLeaguePlayersForPoolRow{
		PlayerID: id, FullName: fullName, EditorialTeamAbbr: team, DisplayPosition: position,
	}
}

func TestYahooPlayerFromLeagueRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  sqlcdb.ListYahooLeaguePlayersForPoolRow
		want store.YahooPlayer
	}{
		{"simple", leagueRow(6743, "Connor McDavid", "EDM", "C"),
			store.YahooPlayer{YahooID: 6743, FirstName: "Connor", LastName: "McDavid", Team: "Edmonton", Positions: []nhl.Position{nhl.PositionCenter}}},
		{"hyphenated first name", leagueRow(1, "Pierre-Luc Dubois", "WSH", "C,LW"),
			store.YahooPlayer{YahooID: 1, FirstName: "Pierre-Luc", LastName: "Dubois", Team: "Washington",
				Positions: []nhl.Position{nhl.PositionCenter, nhl.PositionLeftWing}}},
		{"compound first name", leagueRow(2, "Charles Alexis Legault", "UTAH", "D"),
			store.YahooPlayer{YahooID: 2, FirstName: "Charles", LastName: "Alexis Legault", Team: "Utah", Positions: []nhl.Position{nhl.PositionDefense}}},
		{"initials", leagueRow(3, "J.T. Miller", "NYR", "C"),
			store.YahooPlayer{YahooID: 3, FirstName: "J.T.", LastName: "Miller", Team: "NY Rangers", Positions: []nhl.Position{nhl.PositionCenter}}},
		{"single name", leagueRow(4, "Madonna", "LA", ""),
			store.YahooPlayer{YahooID: 4, FirstName: "Madonna", Team: "Los Angeles"}},
		{"unknown team and padded name", leagueRow(5, "  Free Agent ", "FA", "G"),
			store.YahooPlayer{YahooID: 5, FirstName: "Free", LastName: "Agent", Positions: []nhl.Position{nhl.PositionGoalie}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, yahooPlayerFromLeagueRow(tc.row))
		})
	}
}

func TestMergeLeaguePlayers_PageWinsAndDuplicatesCollapse(t *testing.T) {
	t.Parallel()

	page := store.YahooPlayer{YahooID: 6743, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"}
	rows := []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(6743, "Connor McDavid", "EDM", "C"),
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
		leagueRow(7000, "Mitch Marner", "LV", "RW"),
	}

	merged := mergeLeaguePlayers([]store.YahooPlayer{page}, rows)

	require.Len(t, merged, 2)
	assert.Equal(t, page, merged[0], "the page's jersey number is kept")
	assert.Equal(t, store.YahooPlayerID(7000), merged[1].YahooID)
	assert.Equal(t, "Toronto", merged[1].Team, "the first row (newest season) wins")
}

func newPoolActivities(t *testing.T, pages store.Storage, rows ...sqlcdb.ListYahooLeaguePlayersForPoolRow) *Activities {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &Activities{Storage: pages, RedisClient: client, LeaguePlayers: fakeLeaguePlayers{rows: rows}, Manifests: newFakeManifestStore()}
}

func landing(first, last string) *nhl.PlayerLanding {
	return &nhl.PlayerLanding{FirstName: nhl.LocalizedString{Default: first}, LastName: nhl.LocalizedString{Default: last}}
}

func TestPoolWithoutPagesMatchesLeaguePlayers(t *testing.T) {
	ctx := context.Background()
	act := newPoolActivities(t, store.NewMemStorage(),
		leagueRow(6743, "Connor McDavid", "EDM", "C"),
		leagueRow(2, "Charles Alexis Legault", "UTAH", "D"),
		leagueRow(3, "Sebastian Aho", "CAR", "C"),
		leagueRow(4, "Sebastian Aho", "NYI", "D"),
	)

	players, added, err := act.withLeaguePlayers(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 4, added)
	saved, err := saveYahooPlayersToRedisImpl(ctx, act.RedisClient, players)
	require.NoError(t, err)
	assert.Equal(t, 4, saved.PoolSize)
	pool, err := LoadYahooIDPool(ctx, act.RedisClient)
	require.NoError(t, err)

	got, err := matching.MatchYahooID(landing("Connor", "McDavid"), "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.Equal(t, store.YahooPlayerID(6743), got.YahooID)

	got, err = matching.MatchYahooID(landing("Charles Alexis", "Legault"), "UTA", time.Time{}, pool)
	require.NoError(t, err)
	assert.Equal(t, store.YahooPlayerID(2), got.YahooID, "the NHL split differs from the first-space split")
	assert.Equal(t, matching.MatchReasonFullName, got.Reason)

	got, err = matching.MatchYahooID(landing("Sebastian", "Aho"), "NYI", time.Time{}, pool)
	require.NoError(t, err)
	assert.Equal(t, store.YahooPlayerID(4), got.YahooID)
	assert.Equal(t, matching.MatchReasonTeamTiebreaker, got.Reason)
}

func TestWithLeaguePlayers_ListErrorFails(t *testing.T) {
	act := &Activities{LeaguePlayers: fakeLeaguePlayers{err: errors.New("db down")}}

	_, _, err := act.withLeaguePlayers(context.Background(), nil)

	require.ErrorContains(t, err, "db down")
}

func TestWithLeaguePlayers_CountsOnlyThePlayersWithoutAPage(t *testing.T) {
	act := &Activities{LeaguePlayers: fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(6743, "Connor McDavid", "EDM", "C"),
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
	}}}

	players, added, err := act.withLeaguePlayers(context.Background(), []store.YahooPlayer{{YahooID: 6743}})

	require.NoError(t, err)
	assert.Len(t, players, 2)
	assert.Equal(t, 1, added, "McDavid has a page")
}

func TestSaveYahooPlayersToRedis_LeagueOnlyPool(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	act := newPoolActivities(t, store.NewMemStorage(), leagueRow(7000, "Mitch Marner", "TOR", "RW"))
	env.RegisterActivity(act.SaveYahooPlayersToRedis)

	future, err := env.ExecuteActivity(act.SaveYahooPlayersToRedis, []store.YahooPlayer{})

	require.NoError(t, err)
	var result SaveYahooIDPoolResult
	require.NoError(t, future.Get(&result))
	assert.Equal(t, SaveYahooIDPoolResult{TotalPlayers: 1, AvailablePlayers: 1, PoolSize: 1, LeaguePlayers: 1}, result)
	pool, err := LoadYahooIDPool(context.Background(), act.RedisClient)
	require.NoError(t, err)
	require.Contains(t, pool, store.YahooPlayerID(7000))
	assert.Equal(t, "Marner", pool[7000].LastName)
}

func TestSaveYahooPlayersToRedis_ListErrorSavesNothing(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	act := &Activities{RedisClient: client, LeaguePlayers: fakeLeaguePlayers{err: errors.New("db down")}}
	env.RegisterActivity(act.SaveYahooPlayersToRedis)

	_, err := env.ExecuteActivity(act.SaveYahooPlayersToRedis, []store.YahooPlayer{{YahooID: 97, LastName: "McDavid"}})

	require.ErrorContains(t, err, "list yahoo league players: db down")
	assert.False(t, mr.Exists(YahooIDPoolKey), "no pool is saved without the league players")
	assert.False(t, mr.Exists(YahooIDAvailableKey))
}

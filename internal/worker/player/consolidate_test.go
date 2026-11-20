package player

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// The activity is executed by its string type name: running workflows
// recorded that name in their history, so a renamed method must fail here.
func TestConsolidateBoxscorePlayersActivity_RegisteredNameAndDedupe(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	ctx := context.Background()
	first, second := nhl.NewSeason(2023), nhl.NewSeason(2024)
	mcDavid := store.BoxscorePlayer{ID: 8478402, FirstName: "Connor", LastName: "McDavid", Position: "C"}
	draisaitl := store.BoxscorePlayer{ID: 8477934, FirstName: "Leon", LastName: "Draisaitl", Position: "C"}
	require.NoError(t, cache.SaveBoxscorePlayers(ctx, redisClient, first, []store.BoxscorePlayer{mcDavid}, 0))
	require.NoError(t, cache.SaveBoxscorePlayers(ctx, redisClient, second, []store.BoxscorePlayer{mcDavid, draisaitl}, 0))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	const cacheTTL = 17 * time.Minute
	env.RegisterActivity(&Activities{RedisClient: redisClient, BoxscorePlayerCacheTTL: cacheTTL})

	value, err := env.ExecuteActivity("ConsolidateBoxscorePlayersActivity",
		ConsolidatePlayersInput{Seasons: []nhl.Season{first, second}})
	require.NoError(t, err)

	var result ConsolidatePlayersResult
	require.NoError(t, value.Get(&result))
	require.Equal(t, ConsolidatePlayersResult{TotalPlayers: 3, UniquePlayers: 2}, result)

	all, err := cache.LoadAllBoxscorePlayers(ctx, redisClient)
	require.NoError(t, err)
	require.ElementsMatch(t, []store.BoxscorePlayer{mcDavid, draisaitl}, all)
	require.Equal(t, cacheTTL, server.TTL(cache.AllBoxscorePlayersKey))

	seasons, err := cache.LoadAllBoxscorePlayerSeasons(ctx, redisClient)
	require.NoError(t, err)
	require.Equal(t, []int{2023, 2024}, seasons)
	require.Equal(t, cacheTTL, server.TTL(cache.AllBoxscorePlayerSeasonsKey))
}

// A season whose list cannot be read contributes no players, so it is not
// one of the consolidated set's seasons.
func TestConsolidateBoxscorePlayersActivity_LeavesOutAnUnreadableSeason(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	ctx := context.Background()
	readable, corrupt := nhl.NewSeason(2024), nhl.NewSeason(2025)
	require.NoError(t, cache.SaveBoxscorePlayers(ctx, redisClient, readable, []store.BoxscorePlayer{{ID: 1}}, 0))
	require.NoError(t, server.Set(cache.BoxscorePlayersKey(corrupt), "not gob"))

	act := &Activities{RedisClient: redisClient}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(act)
	_, err := env.ExecuteActivity(act.ConsolidateBoxscorePlayersActivity,
		ConsolidatePlayersInput{Seasons: []nhl.Season{readable, corrupt}})
	require.NoError(t, err)

	seasons, err := cache.LoadAllBoxscorePlayerSeasons(ctx, redisClient)
	require.NoError(t, err)
	require.Equal(t, []int{2024}, seasons)
}

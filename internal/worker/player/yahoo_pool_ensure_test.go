package player

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// shortenedPoolTTL is a pool TTL close to expiry, which a healthy check
// must push back to ImportPlayersTTL.
const shortenedPoolTTL = time.Minute

type EnsureYahooIDPoolSuite struct {
	ActivitySuite
	mr    *miniredis.Miniredis
	store *fakeManifestStore
	act   *Activities
}

func TestEnsureYahooIDPoolSuite(t *testing.T) {
	suite.Run(t, new(EnsureYahooIDPoolSuite))
}

func (s *EnsureYahooIDPoolSuite) SetupTest() {
	s.ActivitySuite.SetupTest()
	s.mr = miniredis.RunT(s.T())
	s.store = newFakeManifestStore()
	mem := store.NewMemStorage()
	for _, id := range []store.YahooPlayerID{97, 29} {
		require.NoError(s.T(), mem.Write(context.Background(), resource.YahooPlayer{PlayerID: id}.Path(), []byte(sampleYahooPlayerHTML)))
	}
	redisClient := redis.NewClient(&redis.Options{Addr: s.mr.Addr()})
	s.T().Cleanup(func() { _ = redisClient.Close() })
	s.act = &Activities{
		Storage:       mem,
		RedisClient:   redisClient,
		Manifests:     s.store,
		LeaguePlayers: fakeLeaguePlayers{},
	}
	s.env.RegisterActivity(s.act.EnsureYahooIDPool)
}

func (s *EnsureYahooIDPoolSuite) ensure(poolSize int) EnsureYahooIDPoolResult {
	future, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: poolSize})
	s.Require().NoError(err)
	var result EnsureYahooIDPoolResult
	s.Require().NoError(future.Get(&result))
	return result
}

func (s *EnsureYahooIDPoolSuite) TestHealthyPoolOnlyGetsItsTTLRefreshed() {
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient,
		[]store.YahooPlayer{{YahooID: 97}, {YahooID: 29}})
	s.Require().NoError(err)
	s.mr.SetTTL(YahooIDPoolKey, shortenedPoolTTL)
	s.mr.SetTTL(YahooIDAvailableKey, shortenedPoolTTL)
	s.Require().NoError(RemoveFromYahooIDPool(context.Background(), s.act.RedisClient, []store.YahooPlayerID{29}))

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 2, PoolSize: 2}, s.ensure(2))

	s.Equal(ImportPlayersTTL, s.mr.TTL(YahooIDPoolKey))
	s.Equal(ImportPlayersTTL, s.mr.TTL(YahooIDAvailableKey))
	members, err := s.mr.Members(YahooIDAvailableKey)
	s.Require().NoError(err)
	s.Equal([]string{"97"}, members, "matches made during the run are kept")
}

func (s *EnsureYahooIDPoolSuite) TestLostPoolIsRebuiltWithoutTheYahooIDsAlreadyMatched() {
	s.store.matched = []store.YahooPlayerID{29}

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 0, Rebuilt: true, PoolSize: 2, Available: 1}, s.ensure(2))

	pool, err := LoadYahooIDPool(context.Background(), s.act.RedisClient)
	s.Require().NoError(err)
	s.Len(pool, 2)
	members, err := s.mr.Members(YahooIDAvailableKey)
	s.Require().NoError(err)
	s.Equal([]string{"97"}, members)
	s.Equal(ImportPlayersTTL, s.mr.TTL(YahooIDPoolKey))
}

func (s *EnsureYahooIDPoolSuite) TestPartialPoolIsCompletedWithoutDeletingIt() {
	s.mr.HSet(YahooIDPoolKey, "5", "other")

	result := s.ensure(2)

	s.True(result.Rebuilt)
	s.Equal(1, result.FoundSize)
	s.Equal(3, result.PoolSize, "the next check compares against the size after the rebuild")
	s.Equal("other", s.mr.HGet(YahooIDPoolKey, "5"), "a rebuild only adds: concurrent readers never see an empty pool")
}

func (s *EnsureYahooIDPoolSuite) TestLostAvailableSetIsRestoredFromTheHash() {
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient,
		[]store.YahooPlayer{{YahooID: 97}, {YahooID: 29}, {YahooID: 5}})
	s.Require().NoError(err)
	s.mr.Del(YahooIDAvailableKey)
	_, err = s.mr.SetAdd(VerifiedNonNHLKey, "5")
	s.Require().NoError(err)
	s.store.matched = []store.YahooPlayerID{29}

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 3, AvailableRestored: true, PoolSize: 3, Available: 1}, s.ensure(3))

	members, err := s.mr.Members(YahooIDAvailableKey)
	s.Require().NoError(err)
	s.Equal([]string{"97"}, members, "matched and verified non-NHL Yahoo IDs stay out")
	s.Equal(ImportPlayersTTL, s.mr.TTL(YahooIDAvailableKey))
}

func (s *EnsureYahooIDPoolSuite) TestEmptiedAvailableSetIsNotRebuiltFromFiles() {
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient,
		[]store.YahooPlayer{{YahooID: 97}, {YahooID: 29}})
	s.Require().NoError(err)
	s.Require().NoError(RemoveFromYahooIDPool(context.Background(), s.act.RedisClient, []store.YahooPlayerID{97, 29}))
	s.store.matched = []store.YahooPlayerID{97, 29}

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 2, PoolSize: 2}, s.ensure(2),
		"every Yahoo ID matched: the set stays empty and the pool is not parsed again")
	s.False(s.mr.Exists(YahooIDAvailableKey))
}

func (s *EnsureYahooIDPoolSuite) TestRebuildShortOfTheSavedSizeFails() {
	_, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 3})

	s.Require().Error(err)
	s.Contains(err.Error(), "rebuilt yahoo id pool holds 2 of the 3 page entries phase 1 saved")
}

func (s *EnsureYahooIDPoolSuite) TestRebuildAfterLeaguePlayersWereDeletedSucceeds() {
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
	}}

	// Phase 1 saved the 2 pages and 2 league players; a newer pool import
	// has deleted one of them since.
	future, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 4, NonPageEntries: 2})

	s.Require().NoError(err)
	var result EnsureYahooIDPoolResult
	s.Require().NoError(future.Get(&result))
	s.Equal(EnsureYahooIDPoolResult{Rebuilt: true, PoolSize: 3, NonPageEntries: 1, Available: 3}, result)
}

func (s *EnsureYahooIDPoolSuite) TestLostPagesFailTheRebuildEvenWhenLeaguePlayersGrew() {
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
		leagueRow(7001, "Auston Matthews", "TOR", "C"),
	}}

	// Phase 1 saved 3 pages; one of the files is gone, two league players
	// were added since.
	_, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 3})

	s.Require().Error(err)
	s.Contains(err.Error(), "rebuilt yahoo id pool holds 2 of the 3 page entries phase 1 saved")
}

func (s *EnsureYahooIDPoolSuite) TestRebuildIgnoresAStaleEntryAnEarlierSaveLeft() {
	// An earlier chain saved league player 7001, which a newer pool import
	// has deleted from yahoo_league_players since; the hash keeps it.
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient, []store.YahooPlayer{{YahooID: 7001}})
	s.Require().NoError(err)
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
	}}
	s.env.RegisterActivity(s.act.SaveYahooPlayersToRedis)
	future, err := s.env.ExecuteActivity(s.act.SaveYahooPlayersToRedis, []store.YahooPlayer{{YahooID: 97}, {YahooID: 29}})
	s.Require().NoError(err)
	var saved SaveYahooIDPoolResult
	s.Require().NoError(future.Get(&saved))
	s.Equal(SaveYahooIDPoolResult{TotalPlayers: 3, AvailablePlayers: 3, PoolSize: 4, NonPageEntries: 2}, saved,
		"the stale entry is counted with the league player, not as a page")

	s.mr.Del(YahooIDPoolKey)
	s.mr.Del(YahooIDAvailableKey)
	future, err = s.env.ExecuteActivity(s.act.EnsureYahooIDPool,
		EnsureYahooIDPoolInput{PoolSize: saved.PoolSize, NonPageEntries: saved.NonPageEntries})

	s.Require().NoError(err)
	var result EnsureYahooIDPoolResult
	s.Require().NoError(future.Get(&result))
	s.Equal(EnsureYahooIDPoolResult{Rebuilt: true, PoolSize: 3, NonPageEntries: 1, Available: 3}, result)
}

func (s *EnsureYahooIDPoolSuite) TestHealthyPoolKeepsTheNonPageEntryCount() {
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient,
		[]store.YahooPlayer{{YahooID: 97}, {YahooID: 7000}})
	s.Require().NoError(err)

	future, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 2, NonPageEntries: 1})

	s.Require().NoError(err)
	var result EnsureYahooIDPoolResult
	s.Require().NoError(future.Get(&result))
	s.Equal(EnsureYahooIDPoolResult{FoundSize: 2, PoolSize: 2, NonPageEntries: 1}, result)
}

func (s *EnsureYahooIDPoolSuite) TestRebuildIncludesLeaguePlayersAndKeepsThePageOnOverlap() {
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(97, "Connor McDavid", "EDM", "C"),
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
	}}
	s.store.matched = []store.YahooPlayerID{29}

	// Phase 1 saved the 2 pages plus the league player without a page.
	future, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 3, NonPageEntries: 1})
	s.Require().NoError(err)
	var result EnsureYahooIDPoolResult
	s.Require().NoError(future.Get(&result))

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 0, Rebuilt: true, PoolSize: 3, NonPageEntries: 1, Available: 2}, result)
	pool, err := LoadYahooIDPool(context.Background(), s.act.RedisClient)
	s.Require().NoError(err)
	s.Equal(97, pool[97].JerseyNumber, "the page wins over the league row")
	s.Equal("Toronto", pool[7000].Team)
}

func (s *EnsureYahooIDPoolSuite) TestRebuildFailsWhenTheLeaguePlayersCannotBeListed() {
	s.act.LeaguePlayers = fakeLeaguePlayers{err: errors.New("db down")}

	_, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, EnsureYahooIDPoolInput{PoolSize: 2})

	s.Require().Error(err)
	s.Contains(err.Error(), "db down")
}

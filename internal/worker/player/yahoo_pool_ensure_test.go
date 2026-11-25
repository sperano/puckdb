package player

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sperano/puckdb/internal/store"
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
	redisClient := redis.NewClient(&redis.Options{Addr: s.mr.Addr()})
	s.T().Cleanup(func() { _ = redisClient.Close() })
	s.act = &Activities{
		RedisClient: redisClient,
		Manifests:   s.store,
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

func (s *EnsureYahooIDPoolSuite) TestEmptiedAvailableSetStaysEmpty() {
	_, err := saveYahooPlayersToRedisImpl(context.Background(), s.act.RedisClient,
		[]store.YahooPlayer{{YahooID: 97}, {YahooID: 29}})
	s.Require().NoError(err)
	s.Require().NoError(RemoveFromYahooIDPool(context.Background(), s.act.RedisClient, []store.YahooPlayerID{97, 29}))
	s.store.matched = []store.YahooPlayerID{97, 29}

	s.Equal(EnsureYahooIDPoolResult{FoundSize: 2, PoolSize: 2}, s.ensure(2),
		"every Yahoo ID matched: the set stays empty")
	s.False(s.mr.Exists(YahooIDAvailableKey))
}

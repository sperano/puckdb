package player

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/resource"
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
	s.act = &Activities{
		Storage:     mem,
		RedisClient: redis.NewClient(&redis.Options{Addr: s.mr.Addr()}),
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

func (s *EnsureYahooIDPoolSuite) TestPartialPoolIsReplaced() {
	s.mr.HSet(YahooIDPoolKey, "5", "stale")

	result := s.ensure(2)

	s.True(result.Rebuilt)
	s.Equal(1, result.FoundSize)
	s.Equal(2, result.PoolSize)
	s.False(s.mr.Exists(YahooIDPoolKey) && s.mr.HGet(YahooIDPoolKey, "5") != "", "the stale entry is gone")
}

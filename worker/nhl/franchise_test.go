package nhl

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

func marshalFranchises(t *testing.T, franchises []nhlapi.Franchise) []byte {
	t.Helper()
	data, err := json.Marshal(nhlapi.FranchisesResponse{Data: franchises})
	require.NoError(t, err)
	return data
}

type UpsertFranchisesTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *UpsertFranchisesTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestUpsertFranchisesTestSuite(t *testing.T) {
	suite.Run(t, new(UpsertFranchisesTestSuite))
}

func (s *UpsertFranchisesTestSuite) TestUpsertFranchises_Success() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
		{ID: 2, FullName: "Toronto Maple Leafs", TeamCommonName: "Maple Leafs", TeamPlaceName: "Toronto"},
		{ID: 3, FullName: "Boston Bruins", TeamCommonName: "Bruins", TeamPlaceName: "Boston"},
	}
	franchisesJSON := marshalFranchises(s.T(), franchises)

	require.NoError(s.T(), mem.Write(resource.Franchises{}.Path(), franchisesJSON))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	upserter.On("UpsertFranchise", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(nil).Times(3)

	activities := &FranchiseActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
		Upserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertFranchises)
	future, err := s.env.ExecuteActivity(activities.UpsertFranchises)

	require.NoError(s.T(), err)

	var result UpsertFranchisesResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 3, result.FranchisesUpserted)
	upserter.AssertExpectations(s.T())
}

func (s *UpsertFranchisesTestSuite) TestUpsertFranchises_EmptyInput() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockFranchiseUpserter{}

	franchisesJSON := marshalFranchises(s.T(), []nhlapi.Franchise{})
	require.NoError(s.T(), mem.Write(resource.Franchises{}.Path(), franchisesJSON))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	activities := &FranchiseActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
		Upserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertFranchises)
	future, err := s.env.ExecuteActivity(activities.UpsertFranchises)

	require.NoError(s.T(), err)

	var result UpsertFranchisesResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.FranchisesUpserted)
	upserter.AssertNotCalled(s.T(), "UpsertFranchise")
}

func (s *UpsertFranchisesTestSuite) TestUpsertFranchises_UpsertError() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
	}
	franchisesJSON := marshalFranchises(s.T(), franchises)
	require.NoError(s.T(), mem.Write(resource.Franchises{}.Path(), franchisesJSON))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	upserter.On("UpsertFranchise", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(errors.New("database error"))

	activities := &FranchiseActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
		Upserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertFranchises)
	_, err := s.env.ExecuteActivity(activities.UpsertFranchises)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "upsert franchise")
	assert.Contains(s.T(), err.Error(), "Montreal Canadiens")
	upserter.AssertExpectations(s.T())
}

func (s *UpsertFranchisesTestSuite) TestUpsertFranchises_PartialFailure() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
		{ID: 2, FullName: "Toronto Maple Leafs", TeamCommonName: "Maple Leafs", TeamPlaceName: "Toronto"},
	}
	franchisesJSON := marshalFranchises(s.T(), franchises)
	require.NoError(s.T(), mem.Write(resource.Franchises{}.Path(), franchisesJSON))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	upserter.On("UpsertFranchise", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(nil).Once()
	upserter.On("UpsertFranchise", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(errors.New("database error")).Once()

	activities := &FranchiseActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
		Upserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertFranchises)
	_, err := s.env.ExecuteActivity(activities.UpsertFranchises)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "Toronto Maple Leafs")
	upserter.AssertExpectations(s.T())
}

// --- FetchFranchises tests ---

type FetchFranchisesTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchFranchisesTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchFranchisesTestSuite(t *testing.T) {
	suite.Run(t, new(FetchFranchisesTestSuite))
}

func (s *FetchFranchisesTestSuite) TestFetchFranchises_CacheHit() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
	}
	require.NoError(s.T(), mem.Write(resource.Franchises{}.Path(), marshalFranchises(s.T(), franchises)))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	activities := &FranchiseActivities{
		Storage:   mem,
		GobCache:  cache.NewGobCache(redisClient),
		NHLClient: client,
	}
	s.env.RegisterActivity(activities.FetchFranchises)
	future, err := s.env.ExecuteActivity(activities.FetchFranchises)

	require.NoError(s.T(), err)

	var result FetchFranchisesResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), core.OriginFileSystem, result.Origin)
	client.AssertNotCalled(s.T(), "Franchises")
}

func (s *FetchFranchisesTestSuite) TestFetchFranchises_CacheMiss() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
		{ID: 3, FullName: "Boston Bruins"},
	}
	client.On("Franchises", mock.Anything).Return(franchises, nil)

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	activities := &FranchiseActivities{
		Storage:   mem,
		GobCache:  cache.NewGobCache(redisClient),
		NHLClient: client,
	}
	s.env.RegisterActivity(activities.FetchFranchises)
	future, err := s.env.ExecuteActivity(activities.FetchFranchises)

	require.NoError(s.T(), err)

	var result FetchFranchisesResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), core.OriginRemoteNHLAPI, result.Origin)
	client.AssertExpectations(s.T())

	assert.True(s.T(), mem.Exists(resource.Franchises{}.Path()))
}

func (s *FetchFranchisesTestSuite) TestFetchFranchises_APIError() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	client.On("Franchises", mock.Anything).Return(nil, errors.New("API unavailable"))

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)

	activities := &FranchiseActivities{
		Storage:   mem,
		GobCache:  cache.NewGobCache(redisClient),
		NHLClient: client,
	}
	s.env.RegisterActivity(activities.FetchFranchises)
	_, err := s.env.ExecuteActivity(activities.FetchFranchises)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "API unavailable")
	client.AssertExpectations(s.T())
}

func (s *FetchFranchisesTestSuite) TestFetchFranchises_CacheCorrupt() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	mem.SetFile(resource.Franchises{}.Path(), []byte("not valid json"))

	franchises := []nhlapi.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
	}
	client.On("Franchises", mock.Anything).Return(franchises, nil)

	mockRedis.ExpectGet(core.RedisKey(resource.Franchises{})).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(resource.Franchises{}), "x", cache.GobCacheTTL).SetVal("OK")

	activities := &FranchiseActivities{
		Storage:   mem,
		GobCache:  cache.NewGobCache(redisClient),
		NHLClient: client,
	}
	s.env.RegisterActivity(activities.FetchFranchises)
	future, err := s.env.ExecuteActivity(activities.FetchFranchises)

	require.NoError(s.T(), err)

	var result FetchFranchisesResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), core.OriginRemoteNHLAPI, result.Origin)
	client.AssertExpectations(s.T())
}

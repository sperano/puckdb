package player

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

func newTestActivities(storage store.Storage) *Activities {
	return &Activities{
		Storage:   storage,
		NHLClient: &MockNHLClient{},
	}
}

func TestDownloadPlayerGameLogsBatch_EmptyInput(t *testing.T) {
	act := newTestActivities(store.NewMemStorage())

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{},
		StartSeason: 2024,
	}

	result, err := act.DownloadPlayerGameLogsBatch(context.Background(), input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Empty(t, result.Errors)
}

// PlayerGameLogTestSuite uses TestActivityEnvironment to provide a proper
// Temporal activity context, which is required by RecordHeartbeat calls.
type PlayerGameLogTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *PlayerGameLogTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestPlayerGameLogTestSuite(t *testing.T) {
	suite.Run(t, new(PlayerGameLogTestSuite))
}

func (s *PlayerGameLogTestSuite) TestCacheHit() {
	mem := store.NewMemStorage()
	act := newTestActivities(mem)

	playerID := nhl.PlayerID(8478402)
	season := nhl.NewSeason(2024)
	gameTypeID := nhl.GameTypeRegularSeason.Int()
	mem.Write(context.Background(), resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: gameTypeID}.Path(), []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{8478402},
		StartSeason: 2024,
		GameTypes:   []int{nhl.GameTypeRegularSeason.Int()},
	}

	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)

	require.NoError(s.T(), err)
	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.Downloaded)
	assert.Equal(s.T(), 1, result.CacheHits)
	assert.Empty(s.T(), result.Errors)
}

func (s *PlayerGameLogTestSuite) TestMultipleGameTypes() {
	mem := store.NewMemStorage()
	act := newTestActivities(mem)

	playerID := nhl.PlayerID(8478402)
	season := nhl.NewSeason(2024)

	mem.Write(context.Background(), resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: nhl.GameTypeRegularSeason.Int()}.Path(), []byte(`{"gameLog":[]}`))
	mem.Write(context.Background(), resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: nhl.GameTypePlayoffs.Int()}.Path(), []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{8478402},
		StartSeason: 2024,
		GameTypes:   []int{nhl.GameTypeRegularSeason.Int(), nhl.GameTypePlayoffs.Int()},
	}

	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)

	require.NoError(s.T(), err)
	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.Downloaded)
	assert.Equal(s.T(), 2, result.CacheHits)
	assert.Empty(s.T(), result.Errors)
}

func (s *PlayerGameLogTestSuite) TestInvalidGameType() {
	act := newTestActivities(store.NewMemStorage())

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{8478402},
		StartSeason: 2024,
		GameTypes:   []int{99},
	}

	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)

	require.NoError(s.T(), err)
	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.Downloaded)
	assert.Equal(s.T(), 0, result.CacheHits)
	assert.Len(s.T(), result.Errors, 1)
	assert.Contains(s.T(), result.Errors[0], "invalid game type")
}

func (s *PlayerGameLogTestSuite) TestDefaultsToRegularSeason() {
	mem := store.NewMemStorage()
	act := newTestActivities(mem)

	playerID := nhl.PlayerID(8478402)
	season := nhl.NewSeason(2024)
	mem.Write(context.Background(), resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: nhl.GameTypeRegularSeason.Int()}.Path(), []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{8478402},
		StartSeason: 2024,
	}

	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)

	require.NoError(s.T(), err)
	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.Downloaded)
	assert.Equal(s.T(), 1, result.CacheHits)
}

func (s *PlayerGameLogTestSuite) TestContextCancellation() {
	act := newTestActivities(store.NewMemStorage())

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{8478402, 8479318},
		StartSeason: 2024,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := act.DownloadPlayerGameLogsBatch(ctx, input)

	assert.Error(s.T(), err)
	assert.Equal(s.T(), context.Canceled, err)
	assert.NotNil(s.T(), result)
}

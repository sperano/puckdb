package player

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ////////////////////////////////////////////////////////////////////////////
// Shared helpers
// ////////////////////////////////////////////////////////////////////////////

// gobEncodeBoxscorePlayers gob-encodes a slice of BoxscorePlayer for Redis mock setup.
func gobEncodeBoxscorePlayers(t *testing.T, players []store.BoxscorePlayer) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))
	return buf.Bytes()
}

// failWriteStorage wraps MemStorage but returns an error from every Write call.
// Used to exercise error paths in ensurePlayerLandingCached.
type failWriteStorage struct {
	inner *store.MemStorage
}

func (f *failWriteStorage) Read(path string) ([]byte, error)       { return f.inner.Read(path) }
func (f *failWriteStorage) Write(_ string, _ []byte) error         { return errors.New("disk full") }
func (f *failWriteStorage) Exists(path string) bool                { return f.inner.Exists(path) }
func (f *failWriteStorage) Delete(path string) error               { return f.inner.Delete(path) }
func (f *failWriteStorage) List(dir, ext string) ([]string, error) { return f.inner.List(dir, ext) }
func (f *failWriteStorage) Stat(path string) (os.FileInfo, error)  { return f.inner.Stat(path) }

// newFailingAwardBatch returns a batch result whose Exec call reports an error.
func newFailingAwardBatch() *sqlcdb.UpsertPlayerAwardBatchBatchResults {
	return sqlcdb.NewUpsertPlayerAwardBatchBatchResults(&mockBatchResults{execErr: errors.New("db constraint"), count: 1}, 1)
}

// newFailingSeasonTotalBatch returns a batch result whose Exec call reports an error.
func newFailingSeasonTotalBatch() *sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults {
	return sqlcdb.NewUpsertPlayerSeasonTotalBatchBatchResults(&mockBatchResults{execErr: errors.New("db constraint"), count: 1}, 1)
}

// ////////////////////////////////////////////////////////////////////////////
// ActivitySuite — Temporal test harness for activities that call GetLogger/RecordHeartbeat
// ////////////////////////////////////////////////////////////////////////////

type ActivitySuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ActivitySuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

// ////////////////////////////////////////////////////////////////////////////
// FetchPlayerLandingsBatch
// ////////////////////////////////////////////////////////////////////////////

type FetchPlayerLandingsBatchSuite struct {
	ActivitySuite
}

func TestFetchPlayerLandingsBatchSuite(t *testing.T) {
	suite.Run(t, new(FetchPlayerLandingsBatchSuite))
}

func (s *FetchPlayerLandingsBatchSuite) TestEmptyPlayers() {
	act := newTestActivities(store.NewMemStorage())
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, []store.BoxscorePlayer{})
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 0, stats.Downloaded)
	assert.Equal(s.T(), 0, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Missing)
}

func (s *FetchPlayerLandingsBatchSuite) TestCacheHit() {
	mem := store.NewMemStorage()
	playerID := nhl.PlayerID(8476453)

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
	}
	data, err := json.Marshal(landing)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), data))

	act := newTestActivities(mem)
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{{ID: int64(playerID), FirstName: "Connor", LastName: "McDavid"}}
	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 0, stats.Downloaded)
	assert.Equal(s.T(), 1, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Missing)
}

func (s *FetchPlayerLandingsBatchSuite) TestDownload() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	playerID := nhl.PlayerID(8476453)

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
	}
	// Use mock.Anything for context: the Temporal test env provides its own context.
	client.On("PlayerLanding", mock.Anything, playerID).Return(landing, nil)

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{{ID: int64(playerID), FirstName: "Connor", LastName: "McDavid"}}
	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 1, stats.Downloaded)
	assert.Equal(s.T(), 0, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Missing)
	client.AssertExpectations(s.T())
}

func (s *FetchPlayerLandingsBatchSuite) TestMissingPlayer() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	playerID := nhl.PlayerID(9999999)

	client.On("PlayerLanding", mock.Anything, playerID).Return(nil, nhl.ErrNotFound)

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{{ID: int64(playerID), FirstName: "Ghost", LastName: "Player", Position: "C"}}
	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 0, stats.Downloaded)
	assert.Equal(s.T(), 0, stats.CacheHits)
	assert.Equal(s.T(), 1, stats.Missing)
	assert.True(s.T(), mem.Exists(resource.MissingPlayerLanding{PlayerID: playerID}.Path()))
	client.AssertExpectations(s.T())
}

func (s *FetchPlayerLandingsBatchSuite) TestAlreadyMarkedMissing() {
	mem := store.NewMemStorage()
	playerID := nhl.PlayerID(9999999)

	missingData, _ := json.Marshal(store.MissingPlayerLandingData{FirstName: "Ghost", LastName: "Player"})
	require.NoError(s.T(), mem.Write(resource.MissingPlayerLanding{PlayerID: playerID}.Path(), missingData))

	act := newTestActivities(mem)
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 1, stats.Missing)
}

func (s *FetchPlayerLandingsBatchSuite) TestAPIError() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	playerID := nhl.PlayerID(8476453)

	client.On("PlayerLanding", mock.Anything, playerID).Return(nil, errors.New("connection refused"))

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	_, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "connection refused")
	client.AssertExpectations(s.T())
}

func (s *FetchPlayerLandingsBatchSuite) TestMultiplePlayers_AllStatuses() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	cachedID := nhl.PlayerID(8476453)
	downloadID := nhl.PlayerID(8479318)
	missingID := nhl.PlayerID(9999999)

	// Pre-populate the cached player's landing file.
	cachedLanding := &nhl.PlayerLanding{PlayerID: cachedID, FirstName: nhl.LocalizedString{Default: "Connor"}, LastName: nhl.LocalizedString{Default: "McDavid"}}
	data, _ := json.Marshal(cachedLanding)
	require.NoError(s.T(), mem.Write(resource.PlayerLanding{PlayerID: cachedID}.Path(), data))

	// The downloadable player needs a network fetch.
	// Use mock.Anything for context: the Temporal test env provides its own activity context.
	downloadLanding := &nhl.PlayerLanding{PlayerID: downloadID, FirstName: nhl.LocalizedString{Default: "Mitch"}, LastName: nhl.LocalizedString{Default: "Marner"}}
	client.On("PlayerLanding", mock.Anything, downloadID).Return(downloadLanding, nil)

	// The missing player returns 404.
	client.On("PlayerLanding", mock.Anything, missingID).Return(nil, nhl.ErrNotFound)

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.FetchPlayerLandingsBatch)

	players := []store.BoxscorePlayer{
		{ID: int64(cachedID)},
		{ID: int64(downloadID)},
		{ID: int64(missingID), Position: "C"},
	}
	future, err := s.env.ExecuteActivity(act.FetchPlayerLandingsBatch, players)
	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), future.Get(&stats))
	assert.Equal(s.T(), 1, stats.Downloaded)
	assert.Equal(s.T(), 1, stats.CacheHits)
	assert.Equal(s.T(), 1, stats.Missing)
	client.AssertExpectations(s.T())
}

// ////////////////////////////////////////////////////////////////////////////
// LoadSeasonBoxscorePlayers
// ////////////////////////////////////////////////////////////////////////////

func TestLoadSeasonBoxscorePlayers_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	season := nhl.NewSeason(2024)
	players := []store.BoxscorePlayer{
		{ID: 8476453, FirstName: "Connor", LastName: "McDavid"},
		{ID: 8479318, FirstName: "Mitch", LastName: "Marner"},
	}
	encoded := gobEncodeBoxscorePlayers(t, players)
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season)).SetVal(string(encoded))

	a := &Activities{RedisClient: redisClient}
	result, err := a.LoadSeasonBoxscorePlayers(ctx, 2024)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(8476453), result[0].ID)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadSeasonBoxscorePlayers_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	season := nhl.NewSeason(2024)
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season)).SetErr(errors.New("redis down"))

	a := &Activities{RedisClient: redisClient}
	result, err := a.LoadSeasonBoxscorePlayers(ctx, 2024)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load boxscore players for 20242025")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// LoadAllBoxscorePlayers
// ////////////////////////////////////////////////////////////////////////////

func TestLoadAllBoxscorePlayers_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	players := []store.BoxscorePlayer{{ID: 8476453, FirstName: "Connor", LastName: "McDavid"}}
	encoded := gobEncodeBoxscorePlayers(t, players)
	mockRedis.ExpectGet(cache.AllBoxscorePlayersKey).SetVal(string(encoded))

	a := &Activities{RedisClient: redisClient}
	result, err := a.LoadAllBoxscorePlayers(ctx)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(8476453), result[0].ID)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadAllBoxscorePlayers_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectGet(cache.AllBoxscorePlayersKey).SetErr(errors.New("cache miss"))

	a := &Activities{RedisClient: redisClient}
	result, err := a.LoadAllBoxscorePlayers(ctx)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// CountPlayersForAllSeasons
// ////////////////////////////////////////////////////////////////////////////

func TestCountPlayersForAllSeasons_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	season2023 := nhl.NewSeason(2023)
	season2024 := nhl.NewSeason(2024)

	players2023 := []store.BoxscorePlayer{{ID: 1}, {ID: 2}, {ID: 3}}
	players2024 := []store.BoxscorePlayer{{ID: 4}, {ID: 5}}

	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season2023)).SetVal(string(gobEncodeBoxscorePlayers(t, players2023)))
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season2024)).SetVal(string(gobEncodeBoxscorePlayers(t, players2024)))

	a := &Activities{RedisClient: redisClient}
	counts, err := a.CountPlayersForAllSeasons(ctx, []int{2023, 2024})

	require.NoError(t, err)
	assert.Equal(t, 3, counts[2023])
	assert.Equal(t, 2, counts[2024])
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCountPlayersForAllSeasons_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, _ := redismock.NewClientMock()

	a := &Activities{RedisClient: redisClient}
	counts, err := a.CountPlayersForAllSeasons(ctx, []int{})

	require.NoError(t, err)
	assert.Empty(t, counts)
}

func TestCountPlayersForAllSeasons_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	season2023 := nhl.NewSeason(2023)
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season2023)).SetErr(errors.New("redis timeout"))

	a := &Activities{RedisClient: redisClient}
	counts, err := a.CountPlayersForAllSeasons(ctx, []int{2023})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load boxscore players for 20232024")
	assert.Nil(t, counts)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// ensurePlayerLandingCached — Write failure path
// ////////////////////////////////////////////////////////////////////////////

func TestEnsurePlayerLandingCached_WriteFailsAfterDownload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &MockNHLClient{}
	playerID := nhl.PlayerID(8476453)

	storage := &failWriteStorage{inner: store.NewMemStorage()}

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	a := &Activities{Storage: storage, NHLClient: client}
	p := store.BoxscorePlayer{ID: int64(playerID), FirstName: "Connor", LastName: "McDavid"}

	_, err := a.ensurePlayerLandingCached(ctx, playerID, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write player")
	client.AssertExpectations(t)
}

// ////////////////////////////////////////////////////////////////////////////
// downloadPlayerGameLogToCache — remaining uncovered paths via DownloadPlayerGameLogsBatch
// ////////////////////////////////////////////////////////////////////////////

type DownloadGameLogSuite struct {
	ActivitySuite
}

func TestDownloadGameLogSuite(t *testing.T) {
	suite.Run(t, new(DownloadGameLogSuite))
}

// TestCurrentSeasonRefresh exercises the path where a current-season file
// already exists but RefreshCurrent=true forces a re-download.
func (s *DownloadGameLogSuite) TestCurrentSeasonRefresh() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	// Determine the current season's start year.
	now := time.Now()
	startYear := now.Year()
	if now.Month() < time.October {
		startYear = now.Year() - 1
	}
	playerID := nhl.PlayerID(8476453)
	season := nhl.NewSeason(startYear)

	// Pre-write an existing file to trigger the "file exists" branch.
	existingPath := resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: nhl.GameTypeRegularSeason.Int()}.Path()
	require.NoError(s.T(), mem.Write(existingPath, []byte(`{"gameLog":[]}`)))

	// The client must be called because RefreshCurrent=true overrides cache.
	// Use mock.Anything for context: Temporal test env provides its own activity context.
	// GameType must be set to a valid value; the zero value causes a JSON marshal error.
	gameLog := &nhl.PlayerGameLog{
		Season:   season,
		GameType: nhl.GameTypeRegularSeason,
		GameLog:  []nhl.GameLog{},
	}
	client.On("PlayerGameLog", mock.Anything, playerID, season, nhl.GameTypeRegularSeason).Return(gameLog, nil)

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:      []int64{int64(playerID)},
		StartSeason:    startYear,
		GameTypes:      []int{nhl.GameTypeRegularSeason.Int()},
		RefreshCurrent: true,
	}
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)
	require.NoError(s.T(), err)

	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 1, result.Downloaded)
	assert.Equal(s.T(), 0, result.Skipped)
	assert.Empty(s.T(), result.Errors)
	client.AssertExpectations(s.T())
}

// TestDownloadAPIError exercises the error path when the NHL client fails.
func (s *DownloadGameLogSuite) TestDownloadAPIError() {
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	// Use a clearly historical season so no skip logic applies.
	const historicalStartYear = 2020
	season := nhl.NewSeason(historicalStartYear)

	client.On("PlayerGameLog", mock.Anything, playerID, season, nhl.GameTypeRegularSeason).
		Return(nil, errors.New("API timeout"))

	act := &Activities{Storage: mem, NHLClient: client}
	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{int64(playerID)},
		StartSeason: historicalStartYear,
		GameTypes:   []int{nhl.GameTypeRegularSeason.Int()},
	}
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)
	require.NoError(s.T(), err)

	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.Downloaded)
	require.Len(s.T(), result.Errors, 1)
	assert.Contains(s.T(), result.Errors[0], "API timeout")
	client.AssertExpectations(s.T())
}

// ////////////////////////////////////////////////////////////////////////////
// ListYahooPlayerFiles, ParseYahooPlayerBatch, SaveYahooPlayersToRedis — activity wrappers
// ////////////////////////////////////////////////////////////////////////////

type YahooActivitySuite struct {
	ActivitySuite
}

func TestYahooActivitySuite(t *testing.T) {
	suite.Run(t, new(YahooActivitySuite))
}

func (s *YahooActivitySuite) TestListYahooPlayerFiles_Success() {
	mem := store.NewMemStorage()
	require.NoError(s.T(), mem.Write(resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))
	require.NoError(s.T(), mem.Write(resource.YahooPlayer{PlayerID: 29}.Path(), []byte(sampleYahooPlayerHTML)))

	act := &Activities{Storage: mem}
	s.env.RegisterActivity(act.ListYahooPlayerFiles)

	future, err := s.env.ExecuteActivity(act.ListYahooPlayerFiles)
	require.NoError(s.T(), err)

	var ids []store.YahooPlayerID
	require.NoError(s.T(), future.Get(&ids))
	assert.Len(s.T(), ids, 2)
}

func (s *YahooActivitySuite) TestListYahooPlayerFiles_Empty() {
	act := &Activities{Storage: store.NewMemStorage()}
	s.env.RegisterActivity(act.ListYahooPlayerFiles)

	future, err := s.env.ExecuteActivity(act.ListYahooPlayerFiles)
	require.NoError(s.T(), err)

	var ids []store.YahooPlayerID
	require.NoError(s.T(), future.Get(&ids))
	assert.Empty(s.T(), ids)
}

func (s *YahooActivitySuite) TestParseYahooPlayerBatch_Success() {
	mem := store.NewMemStorage()
	require.NoError(s.T(), mem.Write(resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))

	act := &Activities{Storage: mem}
	s.env.RegisterActivity(act.ParseYahooPlayerBatch)

	playerIDs := []store.YahooPlayerID{97}
	future, err := s.env.ExecuteActivity(act.ParseYahooPlayerBatch, playerIDs)
	require.NoError(s.T(), err)

	var players []store.YahooPlayer
	require.NoError(s.T(), future.Get(&players))
	require.Len(s.T(), players, 1)
	assert.Equal(s.T(), store.YahooPlayerID(97), players[0].YahooID)
}

func (s *YahooActivitySuite) TestParseYahooPlayerBatch_PartialErrors() {
	mem := store.NewMemStorage()
	// Player 97 parses; player 1 doesn't exist (read error is logged, not returned).
	require.NoError(s.T(), mem.Write(resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))

	act := &Activities{Storage: mem}
	s.env.RegisterActivity(act.ParseYahooPlayerBatch)

	playerIDs := []store.YahooPlayerID{1, 97}
	future, err := s.env.ExecuteActivity(act.ParseYahooPlayerBatch, playerIDs)
	require.NoError(s.T(), err)

	var players []store.YahooPlayer
	require.NoError(s.T(), future.Get(&players))
	assert.Len(s.T(), players, 1)
}

func (s *YahooActivitySuite) TestSaveYahooPlayersToRedis_Success() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	players := []store.YahooPlayer{
		{YahooID: 97, FirstName: "Connor", LastName: "McDavid"},
	}

	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})
	localAnyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(YahooIDAvailableKey, "x").SetVal(1)
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetVal(true)

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.SaveYahooPlayersToRedis)

	future, err := s.env.ExecuteActivity(act.SaveYahooPlayersToRedis, players)
	require.NoError(s.T(), err)

	var result SaveYahooIDPoolResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 1, result.TotalPlayers)
	assert.Equal(s.T(), 1, result.AvailablePlayers)
	assert.Equal(s.T(), 0, result.SkippedNonNHL)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *YahooActivitySuite) TestSaveYahooPlayersToRedis_Empty() {
	redisClient, _ := redismock.NewClientMock()

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.SaveYahooPlayersToRedis)

	future, err := s.env.ExecuteActivity(act.SaveYahooPlayersToRedis, []store.YahooPlayer{})
	require.NoError(s.T(), err)

	var result SaveYahooIDPoolResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.TotalPlayers)
}

// ////////////////////////////////////////////////////////////////////////////
// LoadUnmatchedYahooPlayers — activity wrapper
// ////////////////////////////////////////////////////////////////////////////

type LoadUnmatchedSuite struct {
	ActivitySuite
}

func TestLoadUnmatchedSuite(t *testing.T) {
	suite.Run(t, new(LoadUnmatchedSuite))
}

func (s *LoadUnmatchedSuite) TestEmpty() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{})

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.LoadUnmatchedYahooPlayers)

	future, err := s.env.ExecuteActivity(act.LoadUnmatchedYahooPlayers)
	require.NoError(s.T(), err)

	var players []UnmatchedYahooPlayer
	require.NoError(s.T(), future.Get(&players))
	assert.Empty(s.T(), players)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *LoadUnmatchedSuite) TestSuccess() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	player := &store.YahooPlayer{
		YahooID:      42,
		FirstName:    "Test",
		LastName:     "Player",
		Team:         "TOR",
		JerseyNumber: 10,
	}
	var buf bytes.Buffer
	require.NoError(s.T(), gob.NewEncoder(&buf).Encode(player))

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"42"})
	mockRedis.ExpectHGet(YahooIDPoolKey, "42").SetVal(buf.String())

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.LoadUnmatchedYahooPlayers)

	future, err := s.env.ExecuteActivity(act.LoadUnmatchedYahooPlayers)
	require.NoError(s.T(), err)

	var players []UnmatchedYahooPlayer
	require.NoError(s.T(), future.Get(&players))
	require.Len(s.T(), players, 1)
	assert.Equal(s.T(), store.YahooPlayerID(42), players[0].YahooID)
	assert.Equal(s.T(), "Test", players[0].FirstName)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *LoadUnmatchedSuite) TestRedisError() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("connection refused"))

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.LoadUnmatchedYahooPlayers)

	_, err := s.env.ExecuteActivity(act.LoadUnmatchedYahooPlayers)
	require.Error(s.T(), err)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// VerifyUnmatchedBatch — activity wrapper
// ////////////////////////////////////////////////////////////////////////////

type VerifyUnmatchedBatchSuite struct {
	ActivitySuite
}

func TestVerifyUnmatchedBatchSuite(t *testing.T) {
	suite.Run(t, new(VerifyUnmatchedBatchSuite))
}

func (s *VerifyUnmatchedBatchSuite) TestEmptyPlayers() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	act := &Activities{
		Storage:     store.NewMemStorage(),
		NHLClient:   &MockNHLClient{},
		RedisClient: redisClient,
	}
	s.env.RegisterActivity(act.VerifyUnmatchedBatch)

	future, err := s.env.ExecuteActivity(act.VerifyUnmatchedBatch, []UnmatchedYahooPlayer{})
	require.NoError(s.T(), err)

	var result VerifyUnmatchedResult
	require.NoError(s.T(), future.Get(&result))
	assert.Empty(s.T(), result.VerifiedNonNHL)
	assert.Empty(s.T(), result.TrulyUnmatched)
	assert.Empty(s.T(), result.NotFoundInNHL)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *VerifyUnmatchedBatchSuite) TestPlayerNotFoundInNHL() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	client := &MockNHLClient{}
	limit := maxSearchResults
	// Use mock.Anything for context: Temporal test env provides its own activity context.
	client.On("SearchPlayer", mock.Anything, "John Doe", &limit).
		Return([]nhl.PlayerSearchResult{}, nil)

	act := &Activities{
		Storage:     store.NewMemStorage(),
		NHLClient:   client,
		RedisClient: redisClient,
	}
	s.env.RegisterActivity(act.VerifyUnmatchedBatch)

	players := []UnmatchedYahooPlayer{{YahooID: 1, FirstName: "John", LastName: "Doe"}}
	future, err := s.env.ExecuteActivity(act.VerifyUnmatchedBatch, players)
	require.NoError(s.T(), err)

	var result VerifyUnmatchedResult
	require.NoError(s.T(), future.Get(&result))
	assert.Empty(s.T(), result.VerifiedNonNHL)
	assert.Empty(s.T(), result.TrulyUnmatched)
	assert.Len(s.T(), result.NotFoundInNHL, 1)
	client.AssertExpectations(s.T())
}

// ////////////////////////////////////////////////////////////////////////////
// redis_cache.go — remaining uncovered function paths
// ////////////////////////////////////////////////////////////////////////////

func TestLoadAvailableYahooIDs_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"1", "2", "42"})

	result, err := LoadAvailableYahooIDs(ctx, redisClient)

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Contains(t, result, store.YahooPlayerID(1))
	assert.Contains(t, result, store.YahooPlayerID(42))
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadAvailableYahooIDs_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{})

	result, err := LoadAvailableYahooIDs(ctx, redisClient)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadAvailableYahooIDs_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("timeout"))

	result, err := LoadAvailableYahooIDs(ctx, redisClient)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load available yahoo ids from redis")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadAvailableYahooIDs_SkipsInvalidIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	// "abc" is not a valid integer and should be skipped.
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"1", "abc", "42"})

	result, err := LoadAvailableYahooIDs(ctx, redisClient)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, store.YahooPlayerID(1))
	assert.Contains(t, result, store.YahooPlayerID(42))
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestRemoveFromYahooIDPool_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	// No Redis calls expected when IDs slice is empty.
	err := RemoveFromYahooIDPool(ctx, redisClient, []store.YahooPlayerID{})

	require.NoError(t, err)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestRemoveFromYahooIDPool_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSRem(YahooIDAvailableKey, 1).SetErr(errors.New("redis error"))

	err := RemoveFromYahooIDPool(ctx, redisClient, []store.YahooPlayerID{1})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove from yahoo id pool")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetUnmatchedYahooIDs_SkipsInvalidIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"10", "not-a-number", "20"})

	result, err := GetUnmatchedYahooIDs(ctx, redisClient)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetUnmatchedYahooIDs_Error(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("redis unavailable"))

	result, err := GetUnmatchedYahooIDs(ctx, redisClient)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get unmatched yahoo ids")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestSaveVerifiedNonNHLIDs_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	err := SaveVerifiedNonNHLIDs(ctx, redisClient, []store.YahooPlayerID{})

	require.NoError(t, err)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestSaveVerifiedNonNHLIDs_PipelineError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	localAnyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(VerifiedNonNHLKey, "x").SetErr(errors.New("pipe failed"))

	err := SaveVerifiedNonNHLIDs(ctx, redisClient, []store.YahooPlayerID{1})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "save verified non-nhl ids")
}

func TestLoadVerifiedNonNHLIDs_SkipsInvalidIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{"100", "bad", "200"})

	result, err := LoadVerifiedNonNHLIDs(ctx, redisClient)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, store.YahooPlayerID(100))
	assert.Contains(t, result, store.YahooPlayerID(200))
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetYahooPlayerByID_DecodeError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	yahooID := store.YahooPlayerID(42)
	mockRedis.ExpectHGet(YahooIDPoolKey, "42").SetVal("not-valid-gob-data")

	result, err := GetYahooPlayerByID(ctx, redisClient, yahooID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode yahoo player 42")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetYahooPlayerByID_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	yahooID := store.YahooPlayerID(42)
	mockRedis.ExpectHGet(YahooIDPoolKey, "42").SetErr(errors.New("redis: nil"))

	result, err := GetYahooPlayerByID(ctx, redisClient, yahooID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get yahoo player 42")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadYahooIDPool_SkipsInvalidIDKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	var buf bytes.Buffer
	player := &store.YahooPlayer{YahooID: 42, FirstName: "Connor", LastName: "McDavid"}
	require.NoError(t, gob.NewEncoder(&buf).Encode(player))

	// "abc" is not a valid integer key and must be skipped.
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{
		"abc": buf.String(),
		"42":  buf.String(),
	})

	result, err := LoadYahooIDPool(ctx, redisClient)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Contains(t, result, store.YahooPlayerID(42))
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadYahooIDPool_SkipsCorruptGobData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	// Key "1" has data that cannot be gob-decoded; it should be skipped.
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{
		"1": "not-gob-data",
	})

	result, err := LoadYahooIDPool(ctx, redisClient)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// upsertPlayerCareerData — error paths
// ////////////////////////////////////////////////////////////////////////////

func TestUpsertPlayerCareerData_AwardBatchError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	careerUpserter := &MockPlayerCareerUpserter{}

	playerID := nhl.PlayerID(8476453)
	landing := &nhl.PlayerLanding{
		PlayerID: playerID,
		Awards: []nhl.Award{
			{
				Trophy:  nhl.LocalizedString{Default: "Hart Trophy"},
				Seasons: []nhl.AwardSeason{{SeasonID: nhl.NewSeason(2023)}},
			},
		},
	}

	careerUpserter.On(
		"UpsertPlayerAwardBatch",
		ctx,
		mock.AnythingOfType("[]sqlcdb.UpsertPlayerAwardBatchParams"),
	).Return(newFailingAwardBatch())

	a := &Activities{CareerQueries: careerUpserter}
	_, _, err := a.upsertPlayerCareerData(ctx, landing)

	require.Error(t, err)
	careerUpserter.AssertExpectations(t)
}

func TestUpsertPlayerCareerData_SeasonTotalBatchError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	careerUpserter := &MockPlayerCareerUpserter{}

	playerID := nhl.PlayerID(8476453)
	goals := 50
	landing := &nhl.PlayerLanding{
		PlayerID: playerID,
		// No awards — only season totals, so award batch is not invoked.
		SeasonTotals: []nhl.SeasonTotal{
			{
				Season:       nhl.NewSeason(2023),
				GameType:     nhl.GameTypeRegularSeason,
				LeagueAbbrev: "NHL",
				TeamName:     nhl.LocalizedString{Default: "Edmonton Oilers"},
				GamesPlayed:  82,
				Goals:        &goals,
			},
		},
	}

	careerUpserter.On(
		"UpsertPlayerSeasonTotalBatch",
		ctx,
		mock.AnythingOfType("[]sqlcdb.UpsertPlayerSeasonTotalBatchParams"),
	).Return(newFailingSeasonTotalBatch())

	a := &Activities{CareerQueries: careerUpserter}
	_, _, err := a.upsertPlayerCareerData(ctx, landing)

	require.Error(t, err)
	careerUpserter.AssertExpectations(t)
}

// ////////////////////////////////////////////////////////////////////////////
// intPtrToInt4 — nil path
// ////////////////////////////////////////////////////////////////////////////

func TestIntPtrToInt4_Nil(t *testing.T) {
	t.Parallel()

	result := intPtrToInt4(nil)
	assert.False(t, result.Valid)
	assert.Equal(t, int32(0), result.Int32)
}

func TestIntPtrToInt4_NonNil(t *testing.T) {
	t.Parallel()

	v := 42
	result := intPtrToInt4(&v)
	assert.True(t, result.Valid)
	assert.Equal(t, int32(42), result.Int32)
}

// ////////////////////////////////////////////////////////////////////////////
// RemoveFromYahooIDPool — success path
// ////////////////////////////////////////////////////////////////////////////

func TestRemoveFromYahooIDPool_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	// SRem expects the integer value of the YahooPlayerID.
	mockRedis.ExpectSRem(YahooIDAvailableKey, 42, 97).SetVal(2)

	err := RemoveFromYahooIDPool(ctx, redisClient, []store.YahooPlayerID{42, 97})

	require.NoError(t, err)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// SaveVerifiedNonNHLIDs — success path
// ////////////////////////////////////////////////////////////////////////////

func TestSaveVerifiedNonNHLIDs_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	localAnyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(VerifiedNonNHLKey, "x", "x").SetVal(2)
	mockRedis.ExpectExpire(VerifiedNonNHLKey, VerifiedNonNHLTTL).SetVal(true)

	err := SaveVerifiedNonNHLIDs(ctx, redisClient, []store.YahooPlayerID{10, 20})

	require.NoError(t, err)
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// CountPlayersForSeason (package-level function)
// ////////////////////////////////////////////////////////////////////////////

func TestCountPlayersForSeason_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	season := nhl.NewSeason(2024)
	players := []store.BoxscorePlayer{{ID: 1}, {ID: 2}, {ID: 3}}
	encoded := gobEncodeBoxscorePlayers(t, players)
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season)).SetVal(string(encoded))

	count, err := CountPlayersForSeason(ctx, redisClient, season)

	require.NoError(t, err)
	assert.Equal(t, 3, count)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCountPlayersForSeason_RedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	redisClient, mockRedis := redismock.NewClientMock()

	season := nhl.NewSeason(2024)
	mockRedis.ExpectGet(cache.BoxscorePlayersKey(season)).SetErr(errors.New("redis error"))

	count, err := CountPlayersForSeason(ctx, redisClient, season)

	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "load boxscore players for 20242025")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// CleanupYahooIDPoolData (activity wrapper via Temporal)
// ////////////////////////////////////////////////////////////////////////////

type CleanupSuite struct {
	ActivitySuite
}

func TestCleanupSuite(t *testing.T) {
	suite.Run(t, new(CleanupSuite))
}

func (s *CleanupSuite) TestSuccess() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetVal(2)

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.CleanupYahooIDPoolData)

	// CleanupYahooIDPoolData returns only an error — ExecuteActivity itself
	// surfaces the activity error here if one occurred.
	_, err := s.env.ExecuteActivity(act.CleanupYahooIDPoolData)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *CleanupSuite) TestRedisError() {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetErr(errors.New("redis down"))

	act := &Activities{RedisClient: redisClient}
	s.env.RegisterActivity(act.CleanupYahooIDPoolData)

	_, err := s.env.ExecuteActivity(act.CleanupYahooIDPoolData)
	require.Error(s.T(), err)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

// ////////////////////////////////////////////////////////////////////////////
// downloadPlayerGameLogToCache — storage write error path
// ////////////////////////////////////////////////////////////////////////////

type DownloadGameLogStorageErrorSuite struct {
	ActivitySuite
}

func TestDownloadGameLogStorageErrorSuite(t *testing.T) {
	suite.Run(t, new(DownloadGameLogStorageErrorSuite))
}

// TestStorageWriteError exercises the path where the storage write fails after
// a successful NHL API download.
func (s *DownloadGameLogStorageErrorSuite) TestStorageWriteError() {
	storage := &failWriteStorage{inner: store.NewMemStorage()}
	client := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	const historicalStartYear = 2020
	season := nhl.NewSeason(historicalStartYear)

	gameLog := &nhl.PlayerGameLog{
		Season:   season,
		GameType: nhl.GameTypeRegularSeason,
		GameLog:  []nhl.GameLog{},
	}
	client.On("PlayerGameLog", mock.Anything, playerID, season, nhl.GameTypeRegularSeason).Return(gameLog, nil)

	act := &Activities{Storage: storage, NHLClient: client}
	s.env.RegisterActivity(act.DownloadPlayerGameLogsBatch)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs:   []int64{int64(playerID)},
		StartSeason: historicalStartYear,
		GameTypes:   []int{nhl.GameTypeRegularSeason.Int()},
	}
	future, err := s.env.ExecuteActivity(act.DownloadPlayerGameLogsBatch, input)
	require.NoError(s.T(), err)

	var result DownloadPlayerGameLogsResult
	require.NoError(s.T(), future.Get(&result))
	// The error is collected, not returned at the activity level.
	require.Len(s.T(), result.Errors, 1)
	assert.Contains(s.T(), result.Errors[0], "save:")
	client.AssertExpectations(s.T())
}

// ////////////////////////////////////////////////////////////////////////////
// isCurrentSeason — October branch
// ////////////////////////////////////////////////////////////////////////////

func TestIsCurrentSeason_OctoberStartYear(t *testing.T) {
	t.Parallel()

	// The current date is 2026-04-05 (from MEMORY.md: currentDate=2026-04-05).
	// In April, the current season start year is 2025 (2025-2026 season).
	// Season starting in 2025 should be current, 2024 should not.
	assert.True(t, isCurrentSeason(2025))
	assert.False(t, isCurrentSeason(2024))
	assert.False(t, isCurrentSeason(2026))
}

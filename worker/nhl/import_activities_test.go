package nhl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
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

// =============================================================================
// mockBatchResultsImport is a zero-error pgx.BatchResults for import tests.
// (The import_rosters_test.go already defines mockBatchResults in the same
// package, so we reuse it here — no redeclaration needed.)
// =============================================================================

// zeroBatchResults always succeeds — used when we need a no-op batch result.
type zeroBatchResults struct{}

func (zeroBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (zeroBatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (zeroBatchResults) QueryRow() pgx.Row        { return nil }
func (zeroBatchResults) Close() error              { return nil }

// errBatchResults always returns an error from Exec.
type errBatchResults struct{ err error }

func (e errBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), e.err
}
func (e errBatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (e errBatchResults) QueryRow() pgx.Row        { return nil }
func (e errBatchResults) Close() error              { return nil }

// =============================================================================
// MockQueries implements the full Queries composite interface for ImportActivities.
// =============================================================================

type MockQueries struct {
	mock.Mock
}

func (m *MockQueries) UpsertGame(ctx context.Context, arg sqlcdb.UpsertGameParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertGameSkaterStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameSkaterStatsBatchParams) *sqlcdb.UpsertGameSkaterStatsBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertGameSkaterStatsBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockQueries) UpsertGameGoalieStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameGoalieStatsBatchParams) *sqlcdb.UpsertGameGoalieStatsBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertGameGoalieStatsBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockQueries) UpsertGameBroadcastBatch(ctx context.Context, arg []sqlcdb.UpsertGameBroadcastBatchParams) *sqlcdb.UpsertGameBroadcastBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertGameBroadcastBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockQueries) UpdateSkaterGameLogStats(ctx context.Context, arg sqlcdb.UpdateSkaterGameLogStatsParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertGameThreeStar(ctx context.Context, arg sqlcdb.UpsertGameThreeStarParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertGoalHighlight(ctx context.Context, arg sqlcdb.UpsertGoalHighlightParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertShootoutAttempt(ctx context.Context, arg sqlcdb.UpsertShootoutAttemptParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) GetTeamIDByAbbrev(ctx context.Context, arg sqlcdb.GetTeamIDByAbbrevParams) (int64, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockQueries) UpsertPlayEventBatch(ctx context.Context, arg []sqlcdb.UpsertPlayEventBatchParams) *sqlcdb.UpsertPlayEventBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertPlayEventBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockQueries) UpsertShiftBatch(ctx context.Context, arg []sqlcdb.UpsertShiftBatchParams) *sqlcdb.UpsertShiftBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertShiftBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockQueries) UpsertGameOfficial(ctx context.Context, arg sqlcdb.UpsertGameOfficialParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertGameCoach(ctx context.Context, arg sqlcdb.UpsertGameCoachParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertGameScratch(ctx context.Context, arg sqlcdb.UpsertGameScratchParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) GetGameTeamIDs(ctx context.Context, id int64) (sqlcdb.GetGameTeamIDsRow, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(sqlcdb.GetGameTeamIDsRow), args.Error(1)
}

func (m *MockQueries) UpsertStandingsSnapshotBatch(ctx context.Context, arg []sqlcdb.UpsertStandingsSnapshotBatchParams) *sqlcdb.UpsertStandingsSnapshotBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertStandingsSnapshotBatchBatchResults(zeroBatchResults{}, len(arg))
}

// =============================================================================
// MockClubStatsUpserter implements ClubStatsUpserter.
// =============================================================================

type MockClubStatsUpserter struct {
	mock.Mock
}

func (m *MockClubStatsUpserter) GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error) {
	args := m.Called(ctx, seasonID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]sqlcdb.GetSeasonTeamAbbrevsRow), args.Error(1)
}

func (m *MockClubStatsUpserter) UpsertClubSkaterStatsBatch(ctx context.Context, arg []sqlcdb.UpsertClubSkaterStatsBatchParams) *sqlcdb.UpsertClubSkaterStatsBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertClubSkaterStatsBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockClubStatsUpserter) UpsertClubGoalieStatsBatch(ctx context.Context, arg []sqlcdb.UpsertClubGoalieStatsBatchParams) *sqlcdb.UpsertClubGoalieStatsBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertClubGoalieStatsBatchBatchResults(zeroBatchResults{}, len(arg))
}

// =============================================================================
// MockSeasonRosterUpserter implements SeasonRosterUpserter.
// =============================================================================

type MockSeasonRosterUpserter struct {
	mock.Mock
}

func (m *MockSeasonRosterUpserter) GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error) {
	args := m.Called(ctx, seasonID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]sqlcdb.GetSeasonTeamAbbrevsRow), args.Error(1)
}

func (m *MockSeasonRosterUpserter) EnsurePlayerExistsBatch(ctx context.Context, arg []sqlcdb.EnsurePlayerExistsBatchParams) *sqlcdb.EnsurePlayerExistsBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewEnsurePlayerExistsBatchBatchResults(zeroBatchResults{}, len(arg))
}

func (m *MockSeasonRosterUpserter) UpsertSeasonRosterBatch(ctx context.Context, arg []sqlcdb.UpsertSeasonRosterBatchParams) *sqlcdb.UpsertSeasonRosterBatchBatchResults {
	m.Called(ctx, arg)
	return sqlcdb.NewUpsertSeasonRosterBatchBatchResults(zeroBatchResults{}, len(arg))
}

// =============================================================================
// Test helpers
// =============================================================================

// newImportTestGobCache builds a permissive gob cache for import tests.
func newImportTestGobCache() *cache.GobCache {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 40 {
		mockRedis.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}
	return cache.NewGobCache(redisClient)
}

// seedRegularSeasonSchedule writes a valid daily schedule with one final regular-season game.
func seedRegularSeasonSchedule(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameState: nhlapi.GameStateOff, GameType: nhlapi.GameTypeRegularSeason},
		},
	}
	data, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.DailySchedule{Date: day}.Path(), data))
}

// seedEmptySchedule writes a valid daily schedule with no games.
func seedEmptySchedule(t *testing.T, mem *store.MemStorage, day time.Time) {
	t.Helper()
	data, err := json.Marshal(&nhlapi.DailySchedule{Games: []nhlapi.ScheduleGame{}})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.DailySchedule{Date: day}.Path(), data))
}

// seedEmptyBoxscore writes a JSON-parseable boxscore with no players.
func seedEmptyBoxscore(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	b := createTestBoxscore(gameID)
	data, err := json.Marshal(b)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.Boxscore{Date: day, GameID: gameID}.Path(), data))
}

func seedEmptyPlayByPlay(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	data, err := json.Marshal(&nhlapi.PlayByPlay{
		Season:            nhlapi.NewSeason(2024),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		PeriodDescriptor:  nhlapi.PeriodDescriptor{PeriodType: nhlapi.PeriodTypeRegulation},
	})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayByPlay{Date: day, GameID: gameID}.Path(), data))
}

func seedEmptyShiftChart(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	data, err := json.Marshal(&nhlapi.ShiftChart{})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.ShiftChart{Date: day, GameID: gameID}.Path(), data))
}

func seedEmptyGameStory(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	// Summary must be non-nil because processGameStory dereferences it unconditionally.
	// GameType, GameState, GameScheduleState, and Season must all be valid because nhlapi
	// uses custom MarshalJSON/UnmarshalJSON that rejects zero/empty values.
	data, err := json.Marshal(&nhlapi.GameStory{
		Season:            nhlapi.NewSeason(2023),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		Summary:           &nhlapi.GameSummary{},
	})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.GameStory{Date: day, GameID: gameID}.Path(), data))
}

func seedEmptySeasonSeries(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	data, err := json.Marshal(&nhlapi.SeasonSeriesMatchup{})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.SeasonSeries{Date: day, GameID: gameID}.Path(), data))
}

func seedEmptyStandings(t *testing.T, mem *store.MemStorage, day time.Time) {
	t.Helper()
	data, err := json.Marshal([]nhlapi.Standing{})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.DailyStandings{Date: day}.Path(), data))
}

// =============================================================================
// ImportGameStoryForDate tests
// =============================================================================

type ImportGameStorySuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportGameStorySuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportGameStorySuite(t *testing.T) {
	suite.Run(t, new(ImportGameStorySuite))
}

func (s *ImportGameStorySuite) newActivities(mem *store.MemStorage, q Queries) *ImportActivities {
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
}

func (s *ImportGameStorySuite) TestNoScheduleFile_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err)

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
	q.AssertNotCalled(s.T(), "UpsertGameThreeStar")
}

func (s *ImportGameStorySuite) TestEmptySchedule_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	seedEmptySchedule(s.T(), mem, s.day)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err)

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
}

func (s *ImportGameStorySuite) TestGameWithNoStoryFile_SkipsGame() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	// No game story file written.

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err)

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.GamesProcessed)
	s.Equal(0, result.ThreeStarsImported)
}

func (s *ImportGameStorySuite) TestGameWithThreeStars_UpsertsCalled() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	star1 := nhlapi.ThreeStar{Star: 1, PlayerID: nhlapi.PlayerID(8478402)}
	star2 := nhlapi.ThreeStar{Star: 2, PlayerID: nhlapi.PlayerID(8477934)}
	stars := []nhlapi.ThreeStar{star1, star2}
	story := &nhlapi.GameStory{
		Season:            nhlapi.NewSeason(2024),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		Summary: &nhlapi.GameSummary{
			ThreeStars: &stars,
		},
	}
	data, err := json.Marshal(story)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.GameStory{Date: s.day, GameID: gameID}.Path(), data))

	q.On("UpsertGameThreeStar", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameThreeStarParams")).Return(nil).Times(2)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err)

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.GamesProcessed)
	s.Empty(result.Errors, "unexpected errors: %v", result.Errors)
	s.Equal(2, result.ThreeStarsImported)
	q.AssertExpectations(s.T())
}

func (s *ImportGameStorySuite) TestGameStoryWithGoalHighlights_UpsertsCalled() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	story := &nhlapi.GameStory{
		Season:            nhlapi.NewSeason(2024),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		Summary: &nhlapi.GameSummary{
			Scoring: []nhlapi.PeriodScoring{
				{
					PeriodDescriptor: nhlapi.PeriodDescriptor{Number: 1, PeriodType: nhlapi.PeriodTypeRegulation},
					Goals: []nhlapi.GoalSummary{
						{EventID: 42, PlayerID: nhlapi.PlayerID(8478402), TimeInPeriod: "05:00"},
						{EventID: 43, PlayerID: nhlapi.PlayerID(8477934), TimeInPeriod: "10:00"},
					},
				},
			},
		},
	}
	data, err := json.Marshal(story)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.GameStory{Date: s.day, GameID: gameID}.Path(), data))

	q.On("UpsertGoalHighlight", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGoalHighlightParams")).Return(nil).Times(2)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err)

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(2, result.HighlightsImported)
	q.AssertExpectations(s.T())
}

func (s *ImportGameStorySuite) TestThreeStarUpsertError_RecordedInErrors() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	stars := []nhlapi.ThreeStar{{Star: 1, PlayerID: nhlapi.PlayerID(1)}}
	story := &nhlapi.GameStory{
		Season:            nhlapi.NewSeason(2024),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		Summary:           &nhlapi.GameSummary{ThreeStars: &stars},
	}
	data, err := json.Marshal(story)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.GameStory{Date: s.day, GameID: gameID}.Path(), data))

	q.On("UpsertGameThreeStar", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameThreeStarParams")).Return(errors.New("db error"))

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportGameStoryForDate)

	input := ImportGameStoryForDateInput{Season: 20232024, Date: s.day}
	future, err := s.env.ExecuteActivity(act.ImportGameStoryForDate, input)
	s.Require().NoError(err) // processGameStory errors are accumulated, not returned

	var result *ImportGameStoryForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.ThreeStarsImported)
	s.NotEmpty(result.Errors)
}

// =============================================================================
// ImportPlayByPlayForDate tests
// =============================================================================

type ImportPlayByPlaySuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportPlayByPlaySuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportPlayByPlaySuite(t *testing.T) {
	suite.Run(t, new(ImportPlayByPlaySuite))
}

func (s *ImportPlayByPlaySuite) newActivities(mem *store.MemStorage, q Queries) *ImportActivities {
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
}

func (s *ImportPlayByPlaySuite) TestNoScheduleFile_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportPlayByPlayForDate)

	input := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportPlayByPlayForDate, input)
	s.Require().NoError(err)

	var result ImportPlayByPlayForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
	s.Equal(0, result.EventsImported)
}

func (s *ImportPlayByPlaySuite) TestNonFinalGame_IsSkipped() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	// PRE state game — shouldSkipGame returns true
	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameState: nhlapi.GameStatePreGame, GameType: nhlapi.GameTypeRegularSeason},
		},
	}
	data, err := json.Marshal(schedule)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.DailySchedule{Date: s.day}.Path(), data))

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportPlayByPlayForDate)

	input := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportPlayByPlayForDate, input)
	s.Require().NoError(err)

	var result ImportPlayByPlayForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
}

func (s *ImportPlayByPlaySuite) TestMissingPBPFile_ReturnsError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	// No PBP file written.

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportPlayByPlayForDate)

	input := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	_, err := s.env.ExecuteActivity(act.ImportPlayByPlayForDate, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "play-by-play file missing")
}

func (s *ImportPlayByPlaySuite) TestEmptyPlays_SkipsBatch() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	seedEmptyPlayByPlay(s.T(), mem, s.day, gameID)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportPlayByPlayForDate)

	input := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportPlayByPlayForDate, input)
	s.Require().NoError(err)

	var result ImportPlayByPlayForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
	s.Equal(0, result.EventsImported)
	q.AssertNotCalled(s.T(), "UpsertPlayEventBatch")
}

func (s *ImportPlayByPlaySuite) TestWithPlays_CallsBatch() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	pbp := &nhlapi.PlayByPlay{
		Season:            nhlapi.NewSeason(2024),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		PeriodDescriptor:  nhlapi.PeriodDescriptor{PeriodType: nhlapi.PeriodTypeRegulation},
		Plays: []nhlapi.PlayEvent{
			{EventID: 1, TypeCode: 505, TypeDescKey: "shot-on-goal", PeriodDescriptor: nhlapi.PeriodDescriptor{PeriodType: nhlapi.PeriodTypeRegulation}},
			{EventID: 2, TypeCode: 516, TypeDescKey: "goal", PeriodDescriptor: nhlapi.PeriodDescriptor{PeriodType: nhlapi.PeriodTypeRegulation}},
		},
	}
	data, err := json.Marshal(pbp)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.PlayByPlay{Date: s.day, GameID: gameID}.Path(), data))

	q.On("UpsertPlayEventBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertPlayEventBatchParams) bool {
		return len(params) == 2
	})).Return(nil)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportPlayByPlayForDate)

	input := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportPlayByPlayForDate, input)
	s.Require().NoError(err)

	var result ImportPlayByPlayForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.GamesProcessed)
	s.Equal(2, result.EventsImported)
	q.AssertExpectations(s.T())
}

// =============================================================================
// ImportShiftChartForDate tests
// =============================================================================

type ImportShiftChartSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportShiftChartSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportShiftChartSuite(t *testing.T) {
	suite.Run(t, new(ImportShiftChartSuite))
}

func (s *ImportShiftChartSuite) newActivities(mem *store.MemStorage, q Queries) *ImportActivities {
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
}

func (s *ImportShiftChartSuite) TestNoScheduleFile_ReturnsEmpty() {
	mem := store.NewMemStorage()
	act := s.newActivities(mem, &MockQueries{})
	s.env.RegisterActivity(act.ImportShiftChartForDate)

	input := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportShiftChartForDate, input)
	s.Require().NoError(err)

	var result ImportShiftChartForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
}

func (s *ImportShiftChartSuite) TestMissingShiftFile_ReturnsError() {
	mem := store.NewMemStorage()
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	act := s.newActivities(mem, &MockQueries{})
	s.env.RegisterActivity(act.ImportShiftChartForDate)

	input := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	_, err := s.env.ExecuteActivity(act.ImportShiftChartForDate, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "shift chart file missing")
}

func (s *ImportShiftChartSuite) TestEmptyShifts_SkipsBatch() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	seedEmptyShiftChart(s.T(), mem, s.day, gameID)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportShiftChartForDate)

	input := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportShiftChartForDate, input)
	s.Require().NoError(err)

	var result ImportShiftChartForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.GamesProcessed)
	s.Equal(0, result.ShiftsImported)
	q.AssertNotCalled(s.T(), "UpsertShiftBatch")
}

func (s *ImportShiftChartSuite) TestWithShifts_CallsBatch() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)

	sc := &nhlapi.ShiftChart{
		Data: []nhlapi.ShiftEntry{
			{ID: 1, GameID: gameID, PlayerID: nhlapi.PlayerID(8478402), TeamID: nhlapi.TeamID(22)},
			{ID: 2, GameID: gameID, PlayerID: nhlapi.PlayerID(8477934), TeamID: nhlapi.TeamID(22)},
		},
	}
	data, err := json.Marshal(sc)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.ShiftChart{Date: s.day, GameID: gameID}.Path(), data))

	q.On("UpsertShiftBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertShiftBatchParams) bool {
		return len(params) == 2
	})).Return(nil)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportShiftChartForDate)

	input := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	future, err := s.env.ExecuteActivity(act.ImportShiftChartForDate, input)
	s.Require().NoError(err)

	var result ImportShiftChartForDateResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.GamesProcessed)
	s.Equal(2, result.ShiftsImported)
	q.AssertExpectations(s.T())
}

// =============================================================================
// ImportStandingsForDate tests
// =============================================================================

type ImportStandingsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportStandingsSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportStandingsSuite(t *testing.T) {
	suite.Run(t, new(ImportStandingsSuite))
}

func (s *ImportStandingsSuite) newActivities(mem *store.MemStorage, q Queries) *ImportActivities {
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
}

func (s *ImportStandingsSuite) TestNoStandingsFile_Noop() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportStandingsForDate)

	input := shared.DateSeasonInput{Date: s.day, Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportStandingsForDate, input)
	s.Require().NoError(err)
	q.AssertNotCalled(s.T(), "UpsertStandingsSnapshotBatch")
}

func (s *ImportStandingsSuite) TestEmptyStandings_Noop() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	seedEmptyStandings(s.T(), mem, s.day)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportStandingsForDate)

	input := shared.DateSeasonInput{Date: s.day, Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportStandingsForDate, input)
	s.Require().NoError(err)
	q.AssertNotCalled(s.T(), "UpsertStandingsSnapshotBatch")
}

func (s *ImportStandingsSuite) TestWithStandings_CallsBatch() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	confName := "Eastern"
	confAbbrev := "E"
	standings := []nhlapi.Standing{
		{
			TeamAbbrev:       nhlapi.LocalizedString{Default: "MTL"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
			Wins:             30, Losses: 20, OTLosses: 5, Points: 65,
		},
		{
			TeamAbbrev:     nhlapi.LocalizedString{Default: "TOR"},
			DivisionName:   "Atlantic",
			DivisionAbbrev: "A",
			Wins:           28, Losses: 22, OTLosses: 5, Points: 61,
		},
	}
	data, err := json.Marshal(standings)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.DailyStandings{Date: s.day}.Path(), data))

	q.On("UpsertStandingsSnapshotBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertStandingsSnapshotBatchParams) bool {
		return len(params) == 2
	})).Return(nil)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportStandingsForDate)

	input := shared.DateSeasonInput{Date: s.day, Season: 2023}
	_, err = s.env.ExecuteActivity(act.ImportStandingsForDate, input)
	s.Require().NoError(err)
	q.AssertExpectations(s.T())
}

// =============================================================================
// ImportPlayerGameLogsBatch tests
// =============================================================================

type ImportPlayerGameLogsBatchSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportPlayerGameLogsBatchSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportPlayerGameLogsBatchSuite(t *testing.T) {
	suite.Run(t, new(ImportPlayerGameLogsBatchSuite))
}

func (s *ImportPlayerGameLogsBatchSuite) TestEmptyPlayerIDs_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	act := &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
	s.env.RegisterActivity(act.ImportPlayerGameLogsBatch)

	input := ImportPlayerGameLogsBatchInput{
		Season:    nhlapi.NewSeason(2023),
		PlayerIDs: []int64{},
	}
	future, err := s.env.ExecuteActivity(act.ImportPlayerGameLogsBatch, input)
	s.Require().NoError(err)

	var result *ImportPlayerGameLogsBatchResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.PlayersProcessed)
	s.Equal(0, result.GamesUpdated)
}

func (s *ImportPlayerGameLogsBatchSuite) TestPlayerWithNoFile_IsSkipped() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	act := &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
	s.env.RegisterActivity(act.ImportPlayerGameLogsBatch)

	input := ImportPlayerGameLogsBatchInput{
		Season:    nhlapi.NewSeason(2023),
		PlayerIDs: []int64{8478402, 8477934},
	}
	future, err := s.env.ExecuteActivity(act.ImportPlayerGameLogsBatch, input)
	s.Require().NoError(err)

	var result *ImportPlayerGameLogsBatchResult
	s.Require().NoError(future.Get(&result))
	s.Equal(0, result.PlayersProcessed)
}

func (s *ImportPlayerGameLogsBatchSuite) TestPlayerWithZeroStatEntries_ProcessedWithNoUpdates() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	season := nhlapi.NewSeason(2023)
	playerID := int64(8478402)

	gameLog := &nhlapi.PlayerGameLog{
		Season:   season,
		GameType: nhlapi.GameTypeRegularSeason,
		GameLog: []nhlapi.GameLog{
			{GameID: nhlapi.GameID(2024020001), HomeRoadFlag: nhlapi.HomeRoadHome, PowerPlayPoints: 0},
		},
	}
	data, err := json.Marshal(gameLog)
	s.Require().NoError(err)
	res := resource.PlayerGameLog{
		PlayerID: nhlapi.NewPlayerID(playerID),
		Season:   season,
		GameType: regularSeasonGameType,
	}
	s.Require().NoError(mem.Write(res.Path(), data))

	act := &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
	s.env.RegisterActivity(act.ImportPlayerGameLogsBatch)

	input := ImportPlayerGameLogsBatchInput{
		Season:    season,
		PlayerIDs: []int64{playerID},
	}
	future, err := s.env.ExecuteActivity(act.ImportPlayerGameLogsBatch, input)
	s.Require().NoError(err)

	var result *ImportPlayerGameLogsBatchResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.PlayersProcessed)
	s.Equal(0, result.GamesUpdated)
}

func (s *ImportPlayerGameLogsBatchSuite) TestPlayerWithPPP_UpdatesCalled() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	season := nhlapi.NewSeason(2023)
	playerID := int64(8478402)
	gwg := 1

	gameLog := &nhlapi.PlayerGameLog{
		Season:   season,
		GameType: nhlapi.GameTypeRegularSeason,
		GameLog: []nhlapi.GameLog{
			{GameID: nhlapi.GameID(2024020001), HomeRoadFlag: nhlapi.HomeRoadHome, PowerPlayPoints: 2, GameWinningGoals: &gwg},
		},
	}
	data, err := json.Marshal(gameLog)
	s.Require().NoError(err)
	res := resource.PlayerGameLog{
		PlayerID: nhlapi.NewPlayerID(playerID),
		Season:   season,
		GameType: regularSeasonGameType,
	}
	s.Require().NoError(mem.Write(res.Path(), data))

	q.On("UpdateSkaterGameLogStats", mock.Anything, mock.AnythingOfType("sqlcdb.UpdateSkaterGameLogStatsParams")).Return(nil)

	act := &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
	s.env.RegisterActivity(act.ImportPlayerGameLogsBatch)

	input := ImportPlayerGameLogsBatchInput{
		Season:    season,
		PlayerIDs: []int64{playerID},
	}
	future, err := s.env.ExecuteActivity(act.ImportPlayerGameLogsBatch, input)
	s.Require().NoError(err)

	var result *ImportPlayerGameLogsBatchResult
	s.Require().NoError(future.Get(&result))
	s.Equal(1, result.PlayersProcessed)
	s.Equal(1, result.GamesUpdated)
	q.AssertExpectations(s.T())
}

// =============================================================================
// CollectSeasonPlayerIDs tests
// =============================================================================

type CollectSeasonPlayerIDsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *CollectSeasonPlayerIDsSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestCollectSeasonPlayerIDsSuite(t *testing.T) {
	suite.Run(t, new(CollectSeasonPlayerIDsSuite))
}

func (s *CollectSeasonPlayerIDsSuite) TestEmptyDateRange_ReturnsEmpty() {
	mem := store.NewMemStorage()
	act := &ImportActivities{Storage: mem, GobCache: newImportTestGobCache()}
	s.env.RegisterActivity(act.CollectSeasonPlayerIDs)

	// End before start — no iteration.
	season := nhlapi.SeasonInfo{
		ID:             nhlapi.NewSeason(2023),
		StandingsStart: nhlapi.DateFromTime(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)),
		StandingsEnd:   nhlapi.DateFromTime(time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)),
	}
	future, err := s.env.ExecuteActivity(act.CollectSeasonPlayerIDs, season)
	s.Require().NoError(err)

	var ids []int64
	s.Require().NoError(future.Get(&ids))
	s.Empty(ids)
}

func (s *CollectSeasonPlayerIDsSuite) TestSingleDayWithPlayers_CollectsIDs() {
	mem := store.NewMemStorage()
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhlapi.GameID(2024020001)

	seedBoxscoreDay(s.T(), mem, day, gameID, []nhlapi.SkaterStats{
		{PlayerID: nhlapi.PlayerID(8478402), Name: nhlapi.LocalizedString{Default: "Player One"}, Position: "C"},
		{PlayerID: nhlapi.PlayerID(8477934), Name: nhlapi.LocalizedString{Default: "Player Two"}, Position: "LW"},
	})

	act := &ImportActivities{Storage: mem, GobCache: newImportTestGobCache()}
	s.env.RegisterActivity(act.CollectSeasonPlayerIDs)

	season := nhlapi.SeasonInfo{
		ID:             nhlapi.NewSeason(2023),
		StandingsStart: nhlapi.DateFromTime(day),
		StandingsEnd:   nhlapi.DateFromTime(day),
	}
	future, err := s.env.ExecuteActivity(act.CollectSeasonPlayerIDs, season)
	s.Require().NoError(err)

	var ids []int64
	s.Require().NoError(future.Get(&ids))
	s.Len(ids, 2)
}

// =============================================================================
// importClubStats (SeasonsActivities) tests
// =============================================================================

type ImportClubStatsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportClubStatsSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportClubStatsSuite(t *testing.T) {
	suite.Run(t, new(ImportClubStatsSuite))
}

func (s *ImportClubStatsSuite) TestGetTeamsError_ReturnsError() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return(nil, errors.New("db error"))

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), ClubStatsQueries: q}
	s.env.RegisterActivity(act.ImportClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportClubStats, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "get season teams")
}

func (s *ImportClubStatsSuite) TestNoTeams_LogsWarning_ReturnsNil() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{}, nil)

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), ClubStatsQueries: q}
	s.env.RegisterActivity(act.ImportClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportClubStats, input)
	s.Require().NoError(err)
	q.AssertExpectations(s.T())
}

func (s *ImportClubStatsSuite) TestTeamWithNoCacheFile_Skipped() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), ClubStatsQueries: q}
	s.env.RegisterActivity(act.ImportClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportClubStats, input)
	s.Require().NoError(err)
	// Batch methods should not have been called.
	q.AssertNotCalled(s.T(), "UpsertClubSkaterStatsBatch")
	q.AssertNotCalled(s.T(), "UpsertClubGoalieStatsBatch")
}

func (s *ImportClubStatsSuite) TestWithSkaterStats_CallsBatch() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	for _, gameType := range gameTypesToFetch {
		clubStats := &nhlapi.ClubStats{
			Season:   nhlapi.NewSeason(2023),
			GameType: gameType,
			Skaters: []nhlapi.ClubSkaterStats{
				{PlayerID: nhlapi.PlayerID(8478402), GamesPlayed: 60, Goals: 30},
			},
			Goalies: []nhlapi.ClubGoalieStats{},
		}
		res := resource.ClubStatsResource{Season: 2023, TeamAbbrev: "EDM", GameType: gameType.Int()}
		data, err := json.Marshal(clubStats)
		s.Require().NoError(err)
		s.Require().NoError(mem.Write(res.Path(), data))
	}

	q.On("UpsertClubSkaterStatsBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertClubSkaterStatsBatchParams) bool {
		return len(params) == 1
	})).Return(nil).Times(len(gameTypesToFetch))

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), ClubStatsQueries: q}
	s.env.RegisterActivity(act.ImportClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportClubStats, input)
	s.Require().NoError(err)
	q.AssertExpectations(s.T())
}

// =============================================================================
// ImportSeasonRosters (SeasonsActivities) tests
// =============================================================================

type ImportSeasonRostersSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportSeasonRostersSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportSeasonRostersSuite(t *testing.T) {
	suite.Run(t, new(ImportSeasonRostersSuite))
}

func (s *ImportSeasonRostersSuite) TestGetTeamsError_ReturnsError() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return(nil, errors.New("db error"))

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), RosterQueries: q}
	s.env.RegisterActivity(act.ImportSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportSeasonRosters, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "get season teams")
}

func (s *ImportSeasonRostersSuite) TestNoTeams_ReturnsNil() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{}, nil)

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), RosterQueries: q}
	s.env.RegisterActivity(act.ImportSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportSeasonRosters, input)
	s.Require().NoError(err)
}

func (s *ImportSeasonRostersSuite) TestTeamWithNoCacheFile_Skipped() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), RosterQueries: q}
	s.env.RegisterActivity(act.ImportSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.ImportSeasonRosters, input)
	s.Require().NoError(err)
	q.AssertNotCalled(s.T(), "EnsurePlayerExistsBatch")
	q.AssertNotCalled(s.T(), "UpsertSeasonRosterBatch")
}

func (s *ImportSeasonRostersSuite) TestWithRosterData_CallsBatch() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	players := testRosterPlayers()
	roster := &nhlapi.Roster{Forwards: players[:1], Defensemen: players[1:]}
	data, err := json.Marshal(roster)
	s.Require().NoError(err)
	res := resource.SeasonRoster{Season: 2023, TeamAbbrev: "EDM"}
	s.Require().NoError(mem.Write(res.Path(), data))

	q.On("EnsurePlayerExistsBatch", mock.Anything, mock.Anything).Return(nil)
	q.On("UpsertSeasonRosterBatch", mock.Anything, mock.Anything).Return(nil)

	act := &SeasonsActivities{Storage: mem, GobCache: newImportTestGobCache(), RosterQueries: q}
	s.env.RegisterActivity(act.ImportSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err = s.env.ExecuteActivity(act.ImportSeasonRosters, input)
	s.Require().NoError(err)
	q.AssertExpectations(s.T())
}

// =============================================================================
// FetchSeasonRosters tests
// =============================================================================

type FetchSeasonRostersSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchSeasonRostersSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchSeasonRostersSuite(t *testing.T) {
	suite.Run(t, new(FetchSeasonRostersSuite))
}

func (s *FetchSeasonRostersSuite) TestGetTeamsError_ReturnsError() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return(nil, errors.New("db error"))

	act := &SeasonsActivities{
		Storage:       mem,
		GobCache:      newImportTestGobCache(),
		RosterQueries: q,
		NHLClient:     &MockNHLClient{},
	}
	s.env.RegisterActivity(act.FetchSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchSeasonRosters, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "get season teams")
}

func (s *FetchSeasonRostersSuite) TestNoTeams_ReturnsNil() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{}, nil)

	act := &SeasonsActivities{
		Storage:       mem,
		GobCache:      newImportTestGobCache(),
		RosterQueries: q,
		NHLClient:     &MockNHLClient{},
	}
	s.env.RegisterActivity(act.FetchSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchSeasonRosters, input)
	s.Require().NoError(err)
}

func (s *FetchSeasonRostersSuite) TestCacheHit_DoesNotCallAPI() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	season := nhlapi.NewSeason(2023)
	res := resource.SeasonRoster{Season: 2023, TeamAbbrev: "EDM"}
	roster := &nhlapi.Roster{}
	data, err := json.Marshal(roster)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(res.Path(), data))

	act := &SeasonsActivities{
		Storage:       mem,
		GobCache:      newImportTestGobCache(),
		RosterQueries: q,
		NHLClient:     client,
	}
	s.env.RegisterActivity(act.FetchSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err = s.env.ExecuteActivity(act.FetchSeasonRosters, input)
	s.Require().NoError(err)
	client.AssertNotCalled(s.T(), "RosterSeason", mock.Anything, mock.Anything, season)
}

func (s *FetchSeasonRostersSuite) TestCacheMiss_CallsAPI() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	season := nhlapi.NewSeason(2023)
	roster := &nhlapi.Roster{Forwards: testRosterPlayers()[:1]}
	client.On("RosterSeason", mock.Anything, "EDM", season).Return(roster, nil)

	act := &SeasonsActivities{
		Storage:       mem,
		GobCache:      newImportTestGobCache(),
		RosterQueries: q,
		NHLClient:     client,
	}
	s.env.RegisterActivity(act.FetchSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchSeasonRosters, input)
	s.Require().NoError(err)
	client.AssertExpectations(s.T())
	assert.True(s.T(), mem.Exists(resource.SeasonRoster{Season: 2023, TeamAbbrev: "EDM"}.Path()))
}

func (s *FetchSeasonRostersSuite) TestAPINotFound_IsSkipped() {
	mem := store.NewMemStorage()
	q := &MockSeasonRosterUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	season := nhlapi.NewSeason(2023)
	client.On("RosterSeason", mock.Anything, "EDM", season).Return(nil, nhlapi.ErrNotFound)

	act := &SeasonsActivities{
		Storage:       mem,
		GobCache:      newImportTestGobCache(),
		RosterQueries: q,
		NHLClient:     client,
	}
	s.env.RegisterActivity(act.FetchSeasonRosters)

	input := FetchSeasonRostersInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchSeasonRosters, input)
	s.Require().NoError(err)
	client.AssertExpectations(s.T())
}

// =============================================================================
// FetchClubStats tests
// =============================================================================

type FetchClubStatsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchClubStatsSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchClubStatsSuite(t *testing.T) {
	suite.Run(t, new(FetchClubStatsSuite))
}

func (s *FetchClubStatsSuite) TestGetTeamsError_ReturnsError() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return(nil, errors.New("db error"))

	act := &SeasonsActivities{
		Storage:          mem,
		GobCache:         newImportTestGobCache(),
		ClubStatsQueries: q,
		NHLClient:        &MockNHLClient{},
	}
	s.env.RegisterActivity(act.FetchClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchClubStats, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "get season teams")
}

func (s *FetchClubStatsSuite) TestNoTeams_ReturnsNil() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{}, nil)

	act := &SeasonsActivities{
		Storage:          mem,
		GobCache:         newImportTestGobCache(),
		ClubStatsQueries: q,
		NHLClient:        &MockNHLClient{},
	}
	s.env.RegisterActivity(act.FetchClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchClubStats, input)
	s.Require().NoError(err)
}

func (s *FetchClubStatsSuite) TestCacheHit_DoesNotCallAPI() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	for _, gameType := range gameTypesToFetch {
		res := resource.ClubStatsResource{Season: 2023, TeamAbbrev: "EDM", GameType: gameType.Int()}
		stats := &nhlapi.ClubStats{Season: nhlapi.NewSeason(2023), GameType: gameType}
		data, err := json.Marshal(stats)
		s.Require().NoError(err)
		s.Require().NoError(mem.Write(res.Path(), data))
	}

	act := &SeasonsActivities{
		Storage:          mem,
		GobCache:         newImportTestGobCache(),
		ClubStatsQueries: q,
		NHLClient:        client,
	}
	s.env.RegisterActivity(act.FetchClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchClubStats, input)
	s.Require().NoError(err)
	client.AssertNotCalled(s.T(), "ClubStats")
}

func (s *FetchClubStatsSuite) TestCacheMiss_CallsAPI() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	season := nhlapi.NewSeason(2023)
	for _, gameType := range gameTypesToFetch {
		stats := &nhlapi.ClubStats{Season: season, GameType: gameType}
		client.On("ClubStats", mock.Anything, "EDM", season, gameType).Return(stats, nil)
	}

	act := &SeasonsActivities{
		Storage:          mem,
		GobCache:         newImportTestGobCache(),
		ClubStatsQueries: q,
		NHLClient:        client,
	}
	s.env.RegisterActivity(act.FetchClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchClubStats, input)
	s.Require().NoError(err)
	client.AssertExpectations(s.T())
}

func (s *FetchClubStatsSuite) TestAPINotFound_IsSkipped() {
	mem := store.NewMemStorage()
	q := &MockClubStatsUpserter{}
	client := &MockNHLClient{}

	q.On("GetSeasonTeamAbbrevs", mock.Anything, mock.AnythingOfType("int32")).
		Return([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: 22, Abbrev: "EDM"}}, nil)

	season := nhlapi.NewSeason(2023)
	for _, gameType := range gameTypesToFetch {
		client.On("ClubStats", mock.Anything, "EDM", season, gameType).Return(nil, nhlapi.ErrNotFound)
	}

	act := &SeasonsActivities{
		Storage:          mem,
		GobCache:         newImportTestGobCache(),
		ClubStatsQueries: q,
		NHLClient:        client,
	}
	s.env.RegisterActivity(act.FetchClubStats)

	input := FetchClubStatsInput{Season: 2023}
	_, err := s.env.ExecuteActivity(act.FetchClubStats, input)
	s.Require().NoError(err)
	client.AssertExpectations(s.T())
}

// =============================================================================
// ImportDay tests
// =============================================================================

type ImportDaySuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportDaySuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportDaySuite(t *testing.T) {
	suite.Run(t, new(ImportDaySuite))
}

func (s *ImportDaySuite) newActivities(mem *store.MemStorage, q Queries) *ImportActivities {
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
	}
}

// seedAllImportData seeds all cache files required by ImportDay with a single
// regular-season final game that has no players, goals, or officials.
// Season series is intentionally omitted so processSeasonSeries returns early
// (no file → no GetGameTeamIDs call needed).
func (s *ImportDaySuite) seedAllImportData(mem *store.MemStorage, gameID nhlapi.GameID) {
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	seedEmptyBoxscore(s.T(), mem, s.day, gameID)
	seedEmptyPlayByPlay(s.T(), mem, s.day, gameID)
	seedEmptyShiftChart(s.T(), mem, s.day, gameID)
	seedEmptyGameStory(s.T(), mem, s.day, gameID)
	// SeasonSeries intentionally omitted — processSeasonSeries is a no-op without it.
	seedEmptyStandings(s.T(), mem, s.day)
}

func (s *ImportDaySuite) TestNoFiles_ReturnsEmptyCounts() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	// All sub-methods are no-ops when no schedule file exists.

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportDay)

	input := ImportDayInput{
		Date:     s.day,
		Season:   2023,
		SeasonID: 20232024,
		TeamIDs:  nil,
	}
	future, err := s.env.ExecuteActivity(act.ImportDay, input)
	s.Require().NoError(err)

	var counts core.OriginCounts
	s.Require().NoError(future.Get(&counts))
	s.NotNil(counts)
}

func (s *ImportDaySuite) TestWithCachedGame_ProcessesAllStages() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	s.seedAllImportData(mem, gameID)

	// The empty boxscore has no players so no skater/goalie batch calls happen.
	// UpsertGame will be called once for the game.
	// Empty standings file → UpsertStandingsSnapshotBatch is not called.
	// Season series file omitted → GetGameTeamIDs is not called.
	q.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).Return(nil)

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportDay)

	input := ImportDayInput{
		Date:     s.day,
		Season:   2023,
		SeasonID: 20232024,
		TeamIDs:  nil,
	}
	future, err := s.env.ExecuteActivity(act.ImportDay, input)
	s.Require().NoError(err)

	var counts core.OriginCounts
	s.Require().NoError(future.Get(&counts))
	q.AssertExpectations(s.T())
}

func (s *ImportDaySuite) TestBoxscoreImportError_PropagatesError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	gameID := nhlapi.GameID(2024020001)
	seedRegularSeasonSchedule(s.T(), mem, s.day, gameID)
	seedEmptyBoxscore(s.T(), mem, s.day, gameID)

	q.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).
		Return(errors.New("db failure"))

	act := s.newActivities(mem, q)
	s.env.RegisterActivity(act.ImportDay)

	input := ImportDayInput{
		Date:     s.day,
		Season:   2023,
		SeasonID: 20232024,
	}
	_, err := s.env.ExecuteActivity(act.ImportDay, input)
	s.Require().Error(err)
	s.Contains(err.Error(), "import boxscores")
}

// =============================================================================
// ExtractAndSaveBoxscorePlayers tests
// =============================================================================

func TestExtractAndSaveBoxscorePlayers_EmptySeason(t *testing.T) {
	ts := new(testsuite.WorkflowTestSuite)
	env := ts.NewTestActivityEnvironment()

	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	// SaveBoxscorePlayers will set a key — allow any set call.
	mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(".*", "x", 0).SetVal("OK")

	act := &BoxscoreActivities{
		Storage:     mem,
		GobCache:    gobCache,
		RedisClient: redisClient,
	}
	env.RegisterActivity(act.ExtractAndSaveBoxscorePlayers)

	// Season with no days (end before start).
	input := ExtractAndSaveInput{
		Season: testSeason(
			time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		),
	}
	future, err := env.ExecuteActivity(act.ExtractAndSaveBoxscorePlayers, input)
	require.NoError(t, err)

	var counts core.OriginCounts
	require.NoError(t, future.Get(&counts))
	// No reads happened, counts should be nil or empty.
	_ = counts
}

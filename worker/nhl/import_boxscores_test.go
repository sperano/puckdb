package nhl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
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

// MockBoxscoreUpserter implements BoxscoreUpserter for testing.
type MockBoxscoreUpserter struct {
	mock.Mock
}

func NewMockBoxscoreUpserter() *MockBoxscoreUpserter {
	return &MockBoxscoreUpserter{}
}

func (m *MockBoxscoreUpserter) UpsertGame(ctx context.Context, arg sqlcdb.UpsertGameParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockBoxscoreUpserter) UpsertGameSkaterStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameSkaterStatsBatchParams) *sqlcdb.UpsertGameSkaterStatsBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertGameSkaterStatsBatchBatchResults)
}

func (m *MockBoxscoreUpserter) UpsertGameGoalieStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameGoalieStatsBatchParams) *sqlcdb.UpsertGameGoalieStatsBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertGameGoalieStatsBatchBatchResults)
}

func (m *MockBoxscoreUpserter) UpsertGameBroadcastBatch(ctx context.Context, arg []sqlcdb.UpsertGameBroadcastBatchParams) *sqlcdb.UpsertGameBroadcastBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertGameBroadcastBatchBatchResults)
}

// =============================================================================
// Test parseTOI
// =============================================================================

func TestParseTOI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
		{
			name:     "valid time 20:30",
			input:    "20:30",
			expected: 20*60 + 30,
		},
		{
			name:     "valid time 0:00",
			input:    "0:00",
			expected: 0,
		},
		{
			name:     "valid time 59:59",
			input:    "59:59",
			expected: 59*60 + 59,
		},
		{
			name:     "invalid format - no colon",
			input:    "2030",
			expected: 0,
		},
		{
			name:     "invalid format - too many colons",
			input:    "20:30:00",
			expected: 0,
		},
		{
			name:     "invalid minutes",
			input:    "abc:30",
			expected: 0,
		},
		{
			name:     "invalid seconds",
			input:    "20:xyz",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTOI(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Test boxscoreToGameParams
// =============================================================================

func TestBoxscoreToGameParams(t *testing.T) {
	t.Parallel()

	t.Run("valid boxscore with all fields", func(t *testing.T) {
		boxscore := &nhlapi.Boxscore{
			ID:                nhlapi.GameID(2024020001),
			GameType:          2,
			GameDate:          "2024-01-15",
			StartTimeUTC:      "2024-01-15T19:00:00Z",
			Venue:             nhlapi.LocalizedString{Default: "Madison Square Garden"},
			VenueLocation:     nhlapi.LocalizedString{Default: "New York, NY"},
			EasternUTCOffset:  "-05:00",
			VenueUTCOffset:    "-05:00",
			GameState:         nhlapi.GameState("OFF"),
			GameScheduleState: nhlapi.GameScheduleState("OK"),
			PeriodDescriptor: nhlapi.PeriodDescriptor{
				Number:               3,
				PeriodType:           "REG",
				MaxRegulationPeriods: 3,
			},
			Clock: nhlapi.GameClock{
				TimeRemaining:    "00:00",
				SecondsRemaining: 0,
				Running:          false,
				InIntermission:   false,
			},
			HomeTeam: nhlapi.BoxscoreTeam{
				ID:    nhlapi.TeamID(3),
				Score: 4,
				SOG:   35,
			},
			AwayTeam: nhlapi.BoxscoreTeam{
				ID:    nhlapi.TeamID(10),
				Score: 2,
				SOG:   28,
			},
			LimitedScoring: false,
		}

		params := boxscoreToGameParams(boxscore, 2023)

		assert.Equal(t, int64(2024020001), params.ID)
		assert.Equal(t, int32(2023), params.Season)
		assert.Equal(t, int16(2), params.GameType)
		assert.True(t, params.GameDate.Valid)
		assert.Equal(t, "2024-01-15", params.GameDate.Time.Format("2006-01-02"))
		assert.True(t, params.StartTimeUTC.Valid)
		assert.Equal(t, "Madison Square Garden", params.Venue)
		assert.Equal(t, "New York, NY", params.VenueLocation)
		assert.Equal(t, "-05:00", params.EasternUTCOffset)
		assert.Equal(t, "OFF", params.GameState)
		assert.Equal(t, "OK", params.GameScheduleState)
		assert.Equal(t, int16(3), params.PeriodNumber)
		assert.Equal(t, "REG", params.PeriodType)
		assert.Equal(t, int16(3), params.MaxRegulationPeriods)
		assert.Equal(t, "00:00", params.ClockTimeRemaining)
		assert.Equal(t, int32(0), params.ClockSecondsRemaining)
		assert.False(t, params.ClockRunning)
		assert.False(t, params.ClockInIntermission)
		assert.Equal(t, int64(3), params.HomeTeamID)
		assert.Equal(t, int32(4), params.HomeTeamScore)
		assert.Equal(t, int32(35), params.HomeTeamSog)
		assert.Equal(t, int64(10), params.AwayTeamID)
		assert.Equal(t, int32(2), params.AwayTeamScore)
		assert.Equal(t, int32(28), params.AwayTeamSog)
		assert.False(t, params.LimitedScoring)
	})

	t.Run("invalid date format", func(t *testing.T) {
		boxscore := &nhlapi.Boxscore{
			ID:       nhlapi.GameID(2024020001),
			GameDate: "invalid-date",
		}

		params := boxscoreToGameParams(boxscore, 2023)

		assert.False(t, params.GameDate.Valid)
	})

	t.Run("invalid start time format", func(t *testing.T) {
		boxscore := &nhlapi.Boxscore{
			ID:           nhlapi.GameID(2024020001),
			GameDate:     "2024-01-15",
			StartTimeUTC: "invalid-time",
		}

		params := boxscoreToGameParams(boxscore, 2023)

		assert.True(t, params.GameDate.Valid)
		assert.False(t, params.StartTimeUTC.Valid)
	})
}

// =============================================================================
// Test skaterToBatchParams
// =============================================================================

func TestSkaterToBatchParams(t *testing.T) {
	t.Parallel()

	t.Run("with faceoff percentage", func(t *testing.T) {
		skater := &nhlapi.SkaterStats{
			PlayerID:           nhlapi.PlayerID(8476453),
			SweaterNumber:      97,
			Position:           "C",
			Goals:              2,
			Assists:            1,
			Points:             3,
			PlusMinus:          2,
			SOG:                5,
			TOI:                "22:30",
			Shifts:             25,
			FaceoffWinningPctg: 0.65,
			Hits:               3,
			BlockedShots:       1,
			PIM:                2,
			Giveaways:          1,
			Takeaways:          2,
			PowerPlayGoals:     1,
		}

		params := skaterToBatchParams(nhlapi.GameID(2024020001), 22, true, skater)

		assert.Equal(t, int64(2024020001), params.GameID)
		assert.Equal(t, int64(8476453), params.PlayerID)
		assert.Equal(t, int64(22), params.TeamID)
		assert.True(t, params.IsHome)
		assert.Equal(t, int16(97), params.SweaterNumber)
		assert.Equal(t, "C", params.Position)
		assert.Equal(t, int16(2), params.Goals)
		assert.Equal(t, int16(1), params.Assists)
		assert.Equal(t, int16(3), params.Points)
		assert.Equal(t, int16(2), params.PlusMinus)
		assert.Equal(t, int16(5), params.ShotsOnGoal)
		assert.Equal(t, int32(22*60+30), params.TOISeconds)
		assert.Equal(t, int16(25), params.Shifts)
		assert.True(t, params.FaceoffWinningPctg.Valid)
		assert.InDelta(t, float32(0.65), params.FaceoffWinningPctg.Float32, 0.001)
		assert.Equal(t, int16(3), params.Hits)
		assert.Equal(t, int16(1), params.BlockedShots)
		assert.Equal(t, int16(2), params.PenaltyMinutes)
		assert.Equal(t, int16(1), params.Giveaways)
		assert.Equal(t, int16(2), params.Takeaways)
		assert.Equal(t, int16(1), params.PowerPlayGoals)
	})

	t.Run("without faceoff percentage", func(t *testing.T) {
		skater := &nhlapi.SkaterStats{
			PlayerID:           nhlapi.PlayerID(8476453),
			Position:           "D",
			FaceoffWinningPctg: 0,
		}

		params := skaterToBatchParams(nhlapi.GameID(2024020001), 22, false, skater)

		assert.False(t, params.IsHome)
		assert.Equal(t, "D", params.Position)
		assert.False(t, params.FaceoffWinningPctg.Valid)
	})
}

// =============================================================================
// Test goalieToBatchParams
// =============================================================================

func TestGoalieToBatchParams(t *testing.T) {
	t.Parallel()

	t.Run("with all optional fields", func(t *testing.T) {
		decision := nhlapi.GoalieDecision("W")
		starter := true
		savePctg := 0.925
		pim := 2

		goalie := &nhlapi.GoalieStats{
			PlayerID:                 nhlapi.PlayerID(8477424),
			SweaterNumber:            31,
			Decision:                 &decision,
			Starter:                  &starter,
			ShotsAgainst:             30,
			Saves:                    28,
			SavePctg:                 &savePctg,
			GoalsAgainst:             2,
			EvenStrengthGoalsAgainst: 1,
			PowerPlayGoalsAgainst:    1,
			ShorthandedGoalsAgainst:  0,
			TOI:                      "60:00",
			PIM:                      &pim,
		}

		params := goalieToBatchParams(nhlapi.GameID(2024020001), 22, true, goalie)

		assert.Equal(t, int64(2024020001), params.GameID)
		assert.Equal(t, int64(8477424), params.PlayerID)
		assert.Equal(t, int64(22), params.TeamID)
		assert.True(t, params.IsHome)
		assert.Equal(t, int16(31), params.SweaterNumber)
		assert.True(t, params.Decision.Valid)
		assert.Equal(t, "W", params.Decision.String)
		assert.True(t, params.Starter.Valid)
		assert.True(t, params.Starter.Bool)
		assert.Equal(t, int32(30), params.ShotsAgainst)
		assert.Equal(t, int32(28), params.Saves)
		assert.True(t, params.SavePctg.Valid)
		assert.InDelta(t, float32(0.925), params.SavePctg.Float32, 0.001)
		assert.Equal(t, int16(2), params.GoalsAgainst)
		assert.Equal(t, int16(1), params.EvenStrengthGoalsAgainst)
		assert.Equal(t, int16(1), params.PowerPlayGoalsAgainst)
		assert.Equal(t, int16(0), params.ShorthandedGoalsAgainst)
		assert.Equal(t, int32(60*60), params.TOISeconds)
		assert.True(t, params.PenaltyMinutes.Valid)
		assert.Equal(t, int16(2), params.PenaltyMinutes.Int16)
	})

	t.Run("without optional fields", func(t *testing.T) {
		goalie := &nhlapi.GoalieStats{
			PlayerID:      nhlapi.PlayerID(8477424),
			SweaterNumber: 31,
			ShotsAgainst:  10,
			Saves:         10,
			TOI:           "15:00",
		}

		params := goalieToBatchParams(nhlapi.GameID(2024020001), 22, false, goalie)

		assert.False(t, params.IsHome)
		assert.False(t, params.Decision.Valid)
		assert.False(t, params.Starter.Valid)
		assert.False(t, params.SavePctg.Valid)
		assert.False(t, params.PenaltyMinutes.Valid)
	})
}

// =============================================================================
// Test ImportBoxscoresForDate
// =============================================================================

type ImportBoxscoresSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment

	testDate  time.Time
	testInput ImportBoxscoresForDateInput
}

func (s *ImportBoxscoresSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.testDate = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	s.testInput = ImportBoxscoresForDateInput{shared.DateSeasonInput{Date: s.testDate, Season: 2023}}
}

func TestImportBoxscoresSuite(t *testing.T) {
	suite.Run(t, new(ImportBoxscoresSuite))
}

func (s *ImportBoxscoresSuite) runImport(act *ImportActivities, queries BoxscoreUpserter) (ImportBoxscoresForDateResult, error) {
	input := s.testInput
	wrapper := func(ctx context.Context, in ImportBoxscoresForDateInput) (ImportBoxscoresForDateResult, error) {
		return act.importBoxscoresForDate(ctx, queries, in)
	}
	s.env.RegisterActivity(wrapper)
	val, err := s.env.ExecuteActivity(wrapper, input)
	if err != nil {
		return ImportBoxscoresForDateResult{}, err
	}
	var result ImportBoxscoresForDateResult
	s.Require().NoError(val.Get(&result))
	return result, nil
}

func (s *ImportBoxscoresSuite) TestNoScheduleFile() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	result, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().NoError(err)
	s.Assert().Equal(0, result.GamesImported)
	s.Assert().Equal(0, result.GamesSkipped)
}

func (s *ImportBoxscoresSuite) TestScheduleFileParseError() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), []byte("invalid json"))

	result, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "read daily schedule")
	s.Assert().Equal(0, result.GamesImported)
}

func (s *ImportBoxscoresSuite) TestSkipsNonFinalGames() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"LIVE"},{"id":2024020002,"gameState":"PRE"}]}`)
	mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), scheduleJSON)

	result, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().NoError(err)
	s.Assert().Equal(0, result.GamesImported)
	s.Assert().Equal(2, result.GamesSkipped)
}

func (s *ImportBoxscoresSuite) TestSkipsPreseasonGames() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	scheduleJSON := []byte(`{"games":[{"id":2024010001,"gameState":"OFF"}]}`)
	mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), scheduleJSON)

	result, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().NoError(err)
	s.Assert().Equal(0, result.GamesImported)
	s.Assert().Equal(1, result.GamesSkipped)
}

func (s *ImportBoxscoresSuite) TestBoxscoreFileMissing() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
	mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), scheduleJSON)

	_, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "boxscore file missing")
}

func (s *ImportBoxscoresSuite) TestBoxscoreFileParseError() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
	mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), scheduleJSON)
	mem.Write(resource.Boxscore{Date: s.testDate, GameID: nhlapi.GameID(2024020001)}.Path(), []byte("invalid json"))

	_, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "read boxscore")
}

func (s *ImportBoxscoresSuite) TestUpsertGameError() {
	mem := store.NewMemStorage()
	upserter := NewMockBoxscoreUpserter()

	scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
	boxscore := createTestBoxscore(nhlapi.GameID(2024020001))
	boxscoreJSON, err := json.Marshal(boxscore)
	s.Require().NoError(err)

	s.Require().NoError(mem.Write(resource.DailySchedule{Date: s.testDate}.Path(), scheduleJSON))
	s.Require().NoError(mem.Write(resource.Boxscore{Date: s.testDate, GameID: nhlapi.GameID(2024020001)}.Path(), boxscoreJSON))

	upserter.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).
		Return(errors.New("database error"))

	result, err := s.runImport(&ImportActivities{Storage: mem}, upserter)

	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "upsert game")
	s.Assert().Equal(0, result.GamesImported)
	upserter.AssertExpectations(s.T())
}

// =============================================================================
// Test upsertSkaterStats
// =============================================================================

func TestUpsertSkaterStats(t *testing.T) {
	t.Parallel()

	t.Run("empty skaters", func(t *testing.T) {
		upserter := NewMockBoxscoreUpserter()
		boxscore := &nhlapi.Boxscore{
			ID: nhlapi.GameID(2024020001),
			PlayerByGameStats: nhlapi.PlayerByGameStats{
				AwayTeam: nhlapi.TeamPlayerStats{},
				HomeTeam: nhlapi.TeamPlayerStats{},
			},
		}

		count, err := upsertSkaterStats(context.Background(), upserter, boxscore)

		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

// =============================================================================
// Test upsertGoalieStats
// =============================================================================

func TestUpsertGoalieStats(t *testing.T) {
	t.Parallel()

	t.Run("empty goalies", func(t *testing.T) {
		upserter := NewMockBoxscoreUpserter()
		boxscore := &nhlapi.Boxscore{
			ID: nhlapi.GameID(2024020001),
			PlayerByGameStats: nhlapi.PlayerByGameStats{
				AwayTeam: nhlapi.TeamPlayerStats{},
				HomeTeam: nhlapi.TeamPlayerStats{},
			},
		}

		count, err := upsertGoalieStats(context.Background(), upserter, boxscore)

		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

// =============================================================================
// Helpers
// =============================================================================

func createTestBoxscore(gameID nhlapi.GameID) *nhlapi.Boxscore {
	return &nhlapi.Boxscore{
		ID:                nhlapi.GameID(gameID),
		Season:            nhlapi.NewSeason(2023),
		GameType:          2,
		GameDate:          "2024-01-15",
		StartTimeUTC:      "2024-01-15T19:00:00Z",
		Venue:             nhlapi.LocalizedString{Default: "Test Arena"},
		VenueLocation:     nhlapi.LocalizedString{Default: "Test City"},
		GameState:         nhlapi.GameState("OFF"),
		GameScheduleState: nhlapi.GameScheduleState("OK"),
		PeriodDescriptor: nhlapi.PeriodDescriptor{
			Number:               3,
			PeriodType:           "REG",
			MaxRegulationPeriods: 3,
		},
		HomeTeam: nhlapi.BoxscoreTeam{
			ID:    nhlapi.TeamID(1),
			Score: 3,
			SOG:   30,
		},
		AwayTeam: nhlapi.BoxscoreTeam{
			ID:    nhlapi.TeamID(2),
			Score: 2,
			SOG:   25,
		},
		PlayerByGameStats: nhlapi.PlayerByGameStats{
			AwayTeam: nhlapi.TeamPlayerStats{},
			HomeTeam: nhlapi.TeamPlayerStats{},
		},
	}
}

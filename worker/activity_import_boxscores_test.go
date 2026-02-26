package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock BoxscoreUpserter
// =============================================================================

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

// =============================================================================
// Mock Batch Results
// =============================================================================

type MockSkaterBatchResults struct {
	errors map[int]error
}

func NewMockSkaterBatchResults(errors map[int]error) *sqlcdb.UpsertGameSkaterStatsBatchBatchResults {
	// We can't directly create the real type, so we'll use a different approach
	// by returning nil and handling in the mock
	return nil
}

type MockGoalieBatchResults struct {
	errors map[int]error
}

// mockSkaterBatchExec simulates the Exec behavior for testing
type mockSkaterBatchExec struct {
	count  int
	errors map[int]error
}

func (m *mockSkaterBatchExec) Exec(f func(int, error)) {
	for i := 0; i < m.count; i++ {
		err := m.errors[i]
		f(i, err)
	}
}

type mockGoalieBatchExec struct {
	count  int
	errors map[int]error
}

func (m *mockGoalieBatchExec) Exec(f func(int, error)) {
	for i := 0; i < m.count; i++ {
		err := m.errors[i]
		f(i, err)
	}
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
		boxscore := &nhl.Boxscore{
			ID:           nhl.GameID(2024020001),
			GameType:     2,
			GameDate:     "2024-01-15",
			StartTimeUTC: "2024-01-15T19:00:00Z",
			Venue:        nhl.LocalizedString{Default: "Madison Square Garden"},
			VenueLocation: nhl.LocalizedString{Default: "New York, NY"},
			EasternUTCOffset: "-05:00",
			VenueUTCOffset:   "-05:00",
			GameState:         nhl.GameState("OFF"),
			GameScheduleState: nhl.GameScheduleState("OK"),
			PeriodDescriptor: nhl.PeriodDescriptor{
				Number:               3,
				PeriodType:           "REG",
				MaxRegulationPeriods: 3,
			},
			Clock: nhl.GameClock{
				TimeRemaining:    "00:00",
				SecondsRemaining: 0,
				Running:          false,
				InIntermission:   false,
			},
			HomeTeam: nhl.BoxscoreTeam{
				ID:    nhl.TeamID(3),
				Score: 4,
				SOG:   35,
			},
			AwayTeam: nhl.BoxscoreTeam{
				ID:    nhl.TeamID(10),
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
		boxscore := &nhl.Boxscore{
			ID:       nhl.GameID(2024020001),
			GameDate: "invalid-date",
		}

		params := boxscoreToGameParams(boxscore, 2023)

		assert.False(t, params.GameDate.Valid)
	})

	t.Run("invalid start time format", func(t *testing.T) {
		boxscore := &nhl.Boxscore{
			ID:           nhl.GameID(2024020001),
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
		skater := &nhl.SkaterStats{
			PlayerID:           nhl.PlayerID(8476453),
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

		params := skaterToBatchParams(nhl.GameID(2024020001), 22, true, skater)

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
		skater := &nhl.SkaterStats{
			PlayerID:           nhl.PlayerID(8476453),
			Position:           "D",
			FaceoffWinningPctg: 0, // Defensemen typically don't take faceoffs
		}

		params := skaterToBatchParams(nhl.GameID(2024020001), 22, false, skater)

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
		decision := nhl.GoalieDecision("W")
		starter := true
		savePctg := 0.925
		pim := 2

		goalie := &nhl.GoalieStats{
			PlayerID:                 nhl.PlayerID(8477424),
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

		params := goalieToBatchParams(nhl.GameID(2024020001), 22, true, goalie)

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
		goalie := &nhl.GoalieStats{
			PlayerID:      nhl.PlayerID(8477424),
			SweaterNumber: 31,
			ShotsAgainst:  10,
			Saves:         10,
			TOI:           "15:00",
		}

		params := goalieToBatchParams(nhl.GameID(2024020001), 22, false, goalie)

		assert.False(t, params.IsHome)
		assert.False(t, params.Decision.Valid)
		assert.False(t, params.Starter.Valid)
		assert.False(t, params.SavePctg.Valid)
		assert.False(t, params.PenaltyMinutes.Valid)
	})
}

// =============================================================================
// Test importBoxscoresForDateImpl
// =============================================================================

func TestImportBoxscoresForDateImpl(t *testing.T) {
	t.Parallel()

	testDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	testInput := ImportBoxscoresForDateInput{Date: testDate, Season: 2023}

	t.Run("no schedule file", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		// No schedule file set up - it doesn't exist
		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.NoError(t, err)
		assert.Equal(t, 0, result.GamesImported)
		assert.Equal(t, 0, result.GamesSkipped)
	})

	t.Run("schedule file parse error", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		// Save invalid JSON to schedule
		repos.Schedule.Save(testDate, []byte("invalid json"))

		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read daily schedule")
		assert.Equal(t, 0, result.GamesImported)
	})

	t.Run("skips non-final games", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"LIVE"},{"id":2024020002,"gameState":"PRE"}]}`)
		repos.Schedule.Save(testDate, scheduleJSON)

		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.NoError(t, err)
		assert.Equal(t, 0, result.GamesImported)
		assert.Equal(t, 2, result.GamesSkipped)
	})

	t.Run("skips preseason games", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		// Preseason game ID: 2024010001 (01 = preseason)
		scheduleJSON := []byte(`{"games":[{"id":2024010001,"gameState":"OFF"}]}`)
		repos.Schedule.Save(testDate, scheduleJSON)

		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.NoError(t, err)
		assert.Equal(t, 0, result.GamesImported)
		assert.Equal(t, 1, result.GamesSkipped)
	})

	t.Run("boxscore file missing", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
		repos.Schedule.Save(testDate, scheduleJSON)
		// Boxscore not saved - it's missing

		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.NoError(t, err)
		assert.Equal(t, 0, result.GamesImported)
		assert.Equal(t, 1, result.GamesSkipped)
	})

	t.Run("boxscore file parse error", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()

		scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
		repos.Schedule.Save(testDate, scheduleJSON)
		repos.Boxscore.Save(testDate, nhl.GameID(2024020001), []byte("invalid json"))

		result, err := importBoxscoresForDateImpl(context.Background(), repos, upserter, testInput)

		require.NoError(t, err)
		assert.Equal(t, 0, result.GamesImported)
		assert.Equal(t, 1, result.GamesSkipped)
	})

	t.Run("UpsertGame error", func(t *testing.T) {
		repos := store.NewMemRepos()
		upserter := NewMockBoxscoreUpserter()
		ctx := context.Background()

		scheduleJSON := []byte(`{"games":[{"id":2024020001,"gameState":"OFF"}]}`)
		boxscore := createTestBoxscore(nhl.GameID(2024020001))
		boxscoreJSON, err := json.Marshal(boxscore)
		require.NoError(t, err)

		require.NoError(t, repos.Schedule.Save(testDate, scheduleJSON))
		require.NoError(t, repos.Boxscore.Save(testDate, nhl.GameID(2024020001), boxscoreJSON))

		upserter.On("UpsertGame", ctx, mock.AnythingOfType("sqlcdb.UpsertGameParams")).
			Return(errors.New("database error"))

		result, err := importBoxscoresForDateImpl(ctx, repos, upserter, testInput)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "upsert game")
		assert.Equal(t, 0, result.GamesImported)
		upserter.AssertExpectations(t)
	})
}

// =============================================================================
// Test upsertSkaterStats
// =============================================================================

func TestUpsertSkaterStats(t *testing.T) {
	t.Parallel()

	t.Run("empty skaters", func(t *testing.T) {
		upserter := NewMockBoxscoreUpserter()
		boxscore := &nhl.Boxscore{
			ID: nhl.GameID(2024020001),
			PlayerByGameStats: nhl.PlayerByGameStats{
				AwayTeam: nhl.TeamPlayerStats{},
				HomeTeam: nhl.TeamPlayerStats{},
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
		boxscore := &nhl.Boxscore{
			ID: nhl.GameID(2024020001),
			PlayerByGameStats: nhl.PlayerByGameStats{
				AwayTeam: nhl.TeamPlayerStats{},
				HomeTeam: nhl.TeamPlayerStats{},
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

func createTestBoxscore(gameID nhl.GameID) *nhl.Boxscore {
	return &nhl.Boxscore{
		ID:           gameID,
		Season:       nhl.NewSeason(2023), // 2023-2024 season
		GameType:     2,
		GameDate:     "2024-01-15",
		StartTimeUTC: "2024-01-15T19:00:00Z",
		Venue:        nhl.LocalizedString{Default: "Test Arena"},
		VenueLocation: nhl.LocalizedString{Default: "Test City"},
		GameState:         nhl.GameState("OFF"),
		GameScheduleState: nhl.GameScheduleState("OK"),
		PeriodDescriptor: nhl.PeriodDescriptor{
			Number:               3,
			PeriodType:           "REG",
			MaxRegulationPeriods: 3,
		},
		HomeTeam: nhl.BoxscoreTeam{
			ID:    nhl.TeamID(1),
			Score: 3,
			SOG:   30,
		},
		AwayTeam: nhl.BoxscoreTeam{
			ID:    nhl.TeamID(2),
			Score: 2,
			SOG:   25,
		},
		PlayerByGameStats: nhl.PlayerByGameStats{
			AwayTeam: nhl.TeamPlayerStats{},
			HomeTeam: nhl.TeamPlayerStats{},
		},
	}
}

func ptrInt(i int) *int {
	return &i
}

func ptrFloat64(f float64) *float64 {
	return &f
}

func ptrBool(b bool) *bool {
	return &b
}

func ptrGoalieDecision(d nhl.GoalieDecision) *nhl.GoalieDecision {
	return &d
}

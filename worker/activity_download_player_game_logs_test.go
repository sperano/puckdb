package worker

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestDownloadPlayerGameLogsImpl_EmptyInput(t *testing.T) {
	mockFS := NewMockFileSystem()

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{},
		StartYear: 2024,
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), mockFS, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Empty(t, result.Errors)
}

func TestDownloadPlayerGameLogsImpl_CacheHit(t *testing.T) {
	mockFS := NewMockFileSystem()

	// Set up cache hit - file already exists
	file := store.PlayerGameLogFile{
		PlayerID: nhl.PlayerID(8478402),
		Season:   20242025,
		GameType: nhl.GameTypeRegularSeason.ToInt(),
	}
	mockFS.On("Exists", file).Return(true)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{nhl.GameTypeRegularSeason.ToInt()},
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), mockFS, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Empty(t, result.Errors)
	mockFS.AssertExpectations(t)
}

func TestDownloadPlayerGameLogsImpl_MultipleGameTypes(t *testing.T) {
	mockFS := NewMockFileSystem()

	playerID := nhl.PlayerID(8478402)

	// Regular season cached
	regularFile := store.PlayerGameLogFile{
		PlayerID: playerID,
		Season:   20242025,
		GameType: nhl.GameTypeRegularSeason.ToInt(),
	}
	mockFS.On("Exists", regularFile).Return(true)

	// Playoffs cached
	playoffsFile := store.PlayerGameLogFile{
		PlayerID: playerID,
		Season:   20242025,
		GameType: nhl.GameTypePlayoffs.ToInt(),
	}
	mockFS.On("Exists", playoffsFile).Return(true)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()},
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), mockFS, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 2, result.CacheHits)
	assert.Empty(t, result.Errors)
	mockFS.AssertExpectations(t)
}

func TestDownloadPlayerGameLogsImpl_InvalidGameType(t *testing.T) {
	mockFS := NewMockFileSystem()

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{99}, // Invalid game type
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), mockFS, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "invalid game type")
}

func TestDownloadPlayerGameLogsImpl_DefaultsToRegularSeason(t *testing.T) {
	mockFS := NewMockFileSystem()

	// Should default to regular season (game type 2)
	file := store.PlayerGameLogFile{
		PlayerID: nhl.PlayerID(8478402),
		Season:   20242025,
		GameType: nhl.GameTypeRegularSeason.ToInt(),
	}
	mockFS.On("Exists", file).Return(true)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		// No GameTypes specified - should default to regular season
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), mockFS, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	mockFS.AssertExpectations(t)
}

func TestDownloadPlayerGameLogsImpl_ContextCancellation(t *testing.T) {
	mockFS := NewMockFileSystem()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402, 8479318},
		StartYear: 2024,
	}

	result, err := downloadPlayerGameLogsImpl(ctx, mockFS, input)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.NotNil(t, result)
}

func TestSeasonToInt(t *testing.T) {
	tests := []struct {
		startYear int
		expected  int
	}{
		{2024, 20242025},
		{2023, 20232024},
		{1917, 19171918},
		{2000, 20002001},
	}

	for _, tc := range tests {
		season := nhl.NewSeason(tc.startYear)
		result := seasonToInt(season)
		assert.Equal(t, tc.expected, result, "Season %d-%d", tc.startYear, tc.startYear+1)
	}
}

// MockPlayerGameLogFS extends MockFileSystem with additional setup helpers.
type MockPlayerGameLogFS struct {
	mock.Mock
}

func (m *MockPlayerGameLogFS) Exists(file store.File) bool {
	args := m.Called(file)
	return args.Bool(0)
}

func (m *MockPlayerGameLogFS) MkdirAll(dir string, perm int) error {
	args := m.Called(dir, perm)
	return args.Error(0)
}

func (m *MockPlayerGameLogFS) Write(file store.File, content []byte) error {
	args := m.Called(file, content)
	return args.Error(0)
}

func (m *MockPlayerGameLogFS) Read(file store.File) ([]byte, error) {
	args := m.Called(file)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

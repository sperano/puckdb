package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testPlayerLanding creates a minimal valid PlayerLanding for testing.
func testPlayerLanding(id int64) *nhl.PlayerLanding {
	return &nhl.PlayerLanding{
		PlayerID:       nhl.PlayerID(id),
		IsActive:       true,
		FirstName:      nhl.LocalizedString{Default: "Test"},
		LastName:       nhl.LocalizedString{Default: "Player"},
		Position:       nhl.Position("C"),
		Headshot:       "https://example.com/headshot.jpg",
		HeightInInches: 72,
		WeightInPounds: 200,
		BirthDate:      "1990-01-01",
		ShootsCatches:  nhl.Handedness("L"),
	}
}

func TestDownloadPlayerLandingBatch_AllCacheHits(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerIDs := []int64{1, 2, 3}

	// All players are cached
	for _, id := range playerIDs {
		file := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(id)}
		fs.On("Exists", file).Return(true).Once()

		landing := testPlayerLanding(id)
		data, _ := json.Marshal(landing)
		fs.On("Read", file).Return(data, nil).Once()
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, playerIDs)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 3, result.CacheHits)
	assert.Equal(t, 0, result.Errors)

	client.AssertNotCalled(t, "PlayerLanding")
}

func TestDownloadPlayerLandingBatch_AllDownloads(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerIDs := []int64{100, 200}

	for _, id := range playerIDs {
		file := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(id)}
		fs.On("Exists", file).Return(false).Once()

		landing := testPlayerLanding(id)
		client.On("PlayerLanding", ctx, nhl.PlayerID(id)).Return(landing, nil).Once()

		fs.On("Write", file, mock.Anything).Return(nil).Once()
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, playerIDs)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Errors)
}

func TestDownloadPlayerLandingBatch_MixedResults(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerIDs := []int64{1, 2, 3}

	// Player 1: cached
	file1 := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(1)}
	fs.On("Exists", file1).Return(true).Once()
	landing1 := testPlayerLanding(1)
	data1, _ := json.Marshal(landing1)
	fs.On("Read", file1).Return(data1, nil).Once()

	// Player 2: not cached, download succeeds
	file2 := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(2)}
	fs.On("Exists", file2).Return(false).Once()
	landing2 := testPlayerLanding(2)
	client.On("PlayerLanding", ctx, nhl.PlayerID(2)).Return(landing2, nil).Once()
	fs.On("Write", file2, mock.Anything).Return(nil).Once()

	// Player 3: not cached, download fails
	file3 := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(3)}
	fs.On("Exists", file3).Return(false).Once()
	client.On("PlayerLanding", ctx, nhl.PlayerID(3)).Return(nil, errors.New("player not found")).Once()

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, playerIDs)

	require.NoError(t, err) // Batch continues despite individual errors
	assert.Equal(t, 1, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 1, result.Errors)
}

func TestDownloadPlayerLandingBatch_EmptyBatch(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, []int64{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Errors)
}

func TestDownloadPlayerLandingBatch_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerIDs := []int64{1, 2, 3}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, playerIDs)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Errors)
}

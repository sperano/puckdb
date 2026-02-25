package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
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

// testBoxscorePlayer creates a BoxscorePlayer for testing.
func testBoxscorePlayer(id int64) store.BoxscorePlayer {
	return store.BoxscorePlayer{
		ID:        id,
		FirstName: "Test",
		LastName:  "Player",
		Position:  "C",
	}
}

// testBoxscorePlayers creates a slice of BoxscorePlayers for testing.
func testBoxscorePlayers(ids ...int64) []store.BoxscorePlayer {
	players := make([]store.BoxscorePlayer, len(ids))
	for i, id := range ids {
		players[i] = testBoxscorePlayer(id)
	}
	return players
}

func TestDownloadPlayerLandingBatch_AllCacheHits(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	// All players are cached (check missing file first, then landing file)
	for _, p := range players {
		missingFile := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(p.ID)}
		fs.On("Exists", missingFile).Return(false).Once()

		landingFile := store.PlayerLandingFile{PlayerID: nhl.PlayerID(p.ID)}
		fs.On("Exists", landingFile).Return(true).Once()
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 3, result.CacheHits)
	assert.Equal(t, 0, result.Missing)

	client.AssertNotCalled(t, "PlayerLanding")
}

func TestDownloadPlayerLandingBatch_AllDownloads(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(100, 200)

	for _, p := range players {
		missingFile := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(p.ID)}
		fs.On("Exists", missingFile).Return(false).Once()

		landingFile := store.PlayerLandingFile{PlayerID: nhl.PlayerID(p.ID)}
		fs.On("Exists", landingFile).Return(false).Once()

		landing := testPlayerLanding(p.ID)
		client.On("PlayerLanding", ctx, nhl.PlayerID(p.ID)).Return(landing, nil).Once()

		fs.On("Write", landingFile, mock.Anything).Return(nil).Once()
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestDownloadPlayerLandingBatch_FailsOnError(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	// Player 1: cached
	missingFile1 := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(1)}
	fs.On("Exists", missingFile1).Return(false).Once()
	landingFile1 := store.PlayerLandingFile{PlayerID: nhl.PlayerID(1)}
	fs.On("Exists", landingFile1).Return(true).Once()

	// Player 2: not cached, download fails
	missingFile2 := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(2)}
	fs.On("Exists", missingFile2).Return(false).Once()
	landingFile2 := store.PlayerLandingFile{PlayerID: nhl.PlayerID(2)}
	fs.On("Exists", landingFile2).Return(false).Once()
	client.On("PlayerLanding", ctx, nhl.PlayerID(2)).Return(nil, errors.New("network error")).Once()

	// Player 3: never reached due to error on player 2

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	require.Error(t, err)
	assert.Equal(t, "network error", err.Error())
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestDownloadPlayerLandingBatch_404CachesAsMissing(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(404)

	// Not in cache
	missingFile := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(404)}
	fs.On("Exists", missingFile).Return(false).Once()
	landingFile := store.PlayerLandingFile{PlayerID: nhl.PlayerID(404)}
	fs.On("Exists", landingFile).Return(false).Once()

	// API returns 404
	notFoundErr := nhl.NewResourceNotFoundError("player not found")
	client.On("PlayerLanding", ctx, nhl.PlayerID(404)).Return(nil, notFoundErr).Once()

	// Missing file should be written with player data
	fs.On("Write", missingFile, mock.MatchedBy(func(data []byte) bool {
		var result store.MissingPlayerLandingData
		if err := json.Unmarshal(data, &result); err != nil {
			return false
		}
		return result.FirstName == "Test" && result.LastName == "Player" && result.Position == "C"
	})).Return(nil).Once()

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 1, result.Missing)
}

func TestDownloadPlayerLandingBatch_AlreadyMissing(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(404)

	// Already marked as missing
	missingFile := store.MissingPlayerLandingFile{PlayerID: nhl.PlayerID(404)}
	fs.On("Exists", missingFile).Return(true).Once()

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 1, result.Missing)

	// Should not call API
	client.AssertNotCalled(t, "PlayerLanding")
}

func TestDownloadPlayerLandingBatch_EmptyBatch(t *testing.T) {
	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, []store.BoxscorePlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestDownloadPlayerLandingBatch_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	result, err := downloadPlayerLandingBatchImpl(ctx, fs, client, players)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestEnsurePlayerLandingCached_WriteError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerID := nhl.PlayerID(123)
	boxscorePlayer := testBoxscorePlayer(123)

	// Not in cache
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", landingFile).Return(false)

	// API returns successfully
	landing := testPlayerLanding(123)
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Write fails
	fs.On("Write", landingFile, mock.Anything).Return(errors.New("disk full"))

	status, err := ensurePlayerLandingCached(ctx, fs, client, playerID, boxscorePlayer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write player")
	assert.Contains(t, err.Error(), "disk full")
	assert.Equal(t, playerLandingStatus(0), status)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestEnsurePlayerLandingCached_404SaveMissingError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	playerID := nhl.PlayerID(404)
	boxscorePlayer := testBoxscorePlayer(404)

	// Not in cache
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", landingFile).Return(false)

	// API returns 404
	notFoundErr := nhl.NewResourceNotFoundError("player not found")
	client.On("PlayerLanding", ctx, playerID).Return(nil, notFoundErr)

	// Writing missing file fails (but operation should still succeed)
	fs.On("Write", missingFile, mock.Anything).Return(errors.New("disk full"))

	status, err := ensurePlayerLandingCached(ctx, fs, client, playerID, boxscorePlayer)

	// Should still succeed - save failure is logged but not fatal
	require.NoError(t, err)
	assert.Equal(t, playerLandingMissing, status)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

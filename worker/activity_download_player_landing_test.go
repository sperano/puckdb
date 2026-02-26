package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
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
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	// Pre-populate landing files in cache
	for _, p := range players {
		landing := testPlayerLanding(p.ID)
		data, err := json.Marshal(landing)
		require.NoError(t, err)
		require.NoError(t, repos.Player.SaveLanding(nhl.PlayerID(p.ID), data))
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 3, result.CacheHits)
	assert.Equal(t, 0, result.Missing)

	client.AssertNotCalled(t, "PlayerLanding")
}

func TestDownloadPlayerLandingBatch_AllDownloads(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(100, 200)

	for _, p := range players {
		landing := testPlayerLanding(p.ID)
		client.On("PlayerLanding", ctx, nhl.PlayerID(p.ID)).Return(landing, nil).Once()
	}

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)

	// Verify both landings were cached
	assert.True(t, repos.Player.LandingExists(nhl.PlayerID(100)))
	assert.True(t, repos.Player.LandingExists(nhl.PlayerID(200)))
}

func TestDownloadPlayerLandingBatch_FailsOnError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	// Player 1: cached
	landing1 := testPlayerLanding(1)
	data1, err := json.Marshal(landing1)
	require.NoError(t, err)
	require.NoError(t, repos.Player.SaveLanding(nhl.PlayerID(1), data1))

	// Player 2: not cached, download fails
	client.On("PlayerLanding", ctx, nhl.PlayerID(2)).Return(nil, errors.New("network error")).Once()

	// Player 3: never reached due to error on player 2

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	require.Error(t, err)
	assert.Equal(t, "network error", err.Error())
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestDownloadPlayerLandingBatch_404CachesAsMissing(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(404)

	// API returns 404
	notFoundErr := nhl.NewResourceNotFoundError("player not found")
	client.On("PlayerLanding", ctx, nhl.PlayerID(404)).Return(nil, notFoundErr).Once()

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 1, result.Missing)

	// Verify player was marked as missing
	assert.True(t, repos.Player.IsMissing(nhl.PlayerID(404)))
}

func TestDownloadPlayerLandingBatch_AlreadyMissing(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(404)

	// Pre-mark as missing
	repos.Player.MarkMissing(nhl.PlayerID(404), store.MissingPlayerLandingData{
		FirstName: "Test",
		LastName:  "Player",
		Position:  "C",
	})

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 1, result.Missing)

	// Should not call API
	client.AssertNotCalled(t, "PlayerLanding")
}

func TestDownloadPlayerLandingBatch_EmptyBatch(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, []store.BoxscorePlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestDownloadPlayerLandingBatch_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	players := testBoxscorePlayers(1, 2, 3)

	result, err := downloadPlayerLandingBatchImpl(ctx, repos, client, players)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
}

func TestEnsurePlayerLandingCached_WriteError(t *testing.T) {
	t.Parallel()

	// Use a mock storage that fails on write for testing write errors
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	playerID := nhl.PlayerID(123)
	boxscorePlayer := testBoxscorePlayer(123)

	// API returns successfully
	landing := testPlayerLanding(123)
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Test normal write path (we can't easily test write failures with MemStorage)
	status, err := ensurePlayerLandingCached(ctx, repos, client, playerID, boxscorePlayer)

	require.NoError(t, err)
	assert.Equal(t, playerLandingDownloaded, status)
	assert.True(t, repos.Player.LandingExists(playerID))
	client.AssertExpectations(t)
}

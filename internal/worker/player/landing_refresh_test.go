package player

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	refreshTestPlayerID = nhl.PlayerID(8478402)
	refreshTestMaxAge   = 24 * time.Hour
	// cachedGamesPlayed and servedGamesPlayed tell the cached page from the
	// one the NHL API serves now.
	cachedGamesPlayed = 56
	servedGamesPlayed = 82
)

var refreshTestNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// statFailStorage fails Stat or Read with the set error, delegating the
// rest to MemStorage.
type statFailStorage struct {
	*store.MemStorage
	statErr error
	readErr error
}

func (s *statFailStorage) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	if s.statErr != nil {
		return nil, s.statErr
	}
	return s.MemStorage.Stat(ctx, path)
}

func (s *statFailStorage) Read(ctx context.Context, path string) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.MemStorage.Read(ctx, path)
}

// refreshTestLanding is a landing whose 2025-26 regular season has gp games.
func refreshTestLanding(active bool, gp int) *nhl.PlayerLanding {
	seq := 1
	return &nhl.PlayerLanding{
		PlayerID:  refreshTestPlayerID,
		IsActive:  active,
		FirstName: nhl.LocalizedString{Default: "Leon"},
		LastName:  nhl.LocalizedString{Default: "Draisaitl"},
		SeasonTotals: []nhl.SeasonTotal{{
			Season:       nhl.NewSeason(2025),
			GameType:     nhl.GameTypeRegularSeason,
			LeagueAbbrev: "NHL",
			TeamName:     nhl.LocalizedString{Default: "Edmonton Oilers"},
			Sequence:     &seq,
			GamesPlayed:  gp,
		}},
	}
}

// cacheLanding stores landing as the cached page, written age before
// refreshTestNow.
func cacheLanding(t *testing.T, mem *store.MemStorage, landing *nhl.PlayerLanding, age time.Duration) {
	t.Helper()
	data, err := json.Marshal(landing)
	require.NoError(t, err)
	mem.SetFileWithTime(resource.PlayerLanding{PlayerID: refreshTestPlayerID}.Path(), data, refreshTestNow.Add(-age))
}

// cachedGP reads the cached page's 2025-26 games played.
func cachedGP(t *testing.T, s store.Storage) int {
	t.Helper()
	landing, err := resource.ReadParsed(context.Background(), s, resource.PlayerLanding{PlayerID: refreshTestPlayerID})
	require.NoError(t, err)
	require.Len(t, landing.SeasonTotals, 1)
	return landing.SeasonTotals[0].GamesPlayed
}

func refreshTestActivities(s store.Storage, client *MockNHLClient, maxAge time.Duration) *Activities {
	return &Activities{
		Storage:       s,
		NHLClient:     client,
		LandingMaxAge: maxAge,
		Now:           func() time.Time { return refreshTestNow },
	}
}

func ensureRefreshTestLanding(act *Activities) (playerLandingStatus, error) {
	return act.ensurePlayerLandingCached(context.Background(), refreshTestPlayerID,
		store.BoxscorePlayer{ID: int64(refreshTestPlayerID), FirstName: "Leon", LastName: "Draisaitl"})
}

func TestEnsurePlayerLandingCachedRedownloadsStaleActiveLanding(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	cacheLanding(t, mem, refreshTestLanding(true, cachedGamesPlayed), refreshTestMaxAge)
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(refreshTestLanding(true, servedGamesPlayed), nil)

	status, err := ensureRefreshTestLanding(refreshTestActivities(mem, client, refreshTestMaxAge))

	require.NoError(t, err)
	assert.Equal(t, playerLandingDownloaded, status)
	assert.Equal(t, servedGamesPlayed, cachedGP(t, mem))
	client.AssertExpectations(t)
}

func TestEnsurePlayerLandingCachedRedownloadsUnreadableStaleLanding(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	mem.SetFileWithTime(resource.PlayerLanding{PlayerID: refreshTestPlayerID}.Path(), []byte("{not json"),
		refreshTestNow.Add(-refreshTestMaxAge))
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(refreshTestLanding(true, servedGamesPlayed), nil)

	status, err := ensureRefreshTestLanding(refreshTestActivities(mem, client, refreshTestMaxAge))

	require.NoError(t, err)
	assert.Equal(t, playerLandingDownloaded, status)
	assert.Equal(t, servedGamesPlayed, cachedGP(t, mem))
}

func TestEnsurePlayerLandingCachedKeepsCurrentLanding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		active bool
		age    time.Duration
		maxAge time.Duration
	}{
		{"active page younger than the max age", true, refreshTestMaxAge - time.Minute, refreshTestMaxAge},
		{"inactive player's page is final", false, 10 * refreshTestMaxAge, refreshTestMaxAge},
		{"zero max age never downloads again", true, 10 * refreshTestMaxAge, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := store.NewMemStorage()
			cacheLanding(t, mem, refreshTestLanding(tc.active, cachedGamesPlayed), tc.age)
			client := &MockNHLClient{}

			status, err := ensureRefreshTestLanding(refreshTestActivities(mem, client, tc.maxAge))

			require.NoError(t, err)
			assert.Equal(t, playerLandingCached, status)
			assert.Equal(t, cachedGamesPlayed, cachedGP(t, mem))
			client.AssertNotCalled(t, "PlayerLanding", mock.Anything, mock.Anything)
		})
	}
}

func TestEnsurePlayerLandingCachedKeepsStaleLandingOn404(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	cacheLanding(t, mem, refreshTestLanding(true, cachedGamesPlayed), refreshTestMaxAge)
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, nhl.ErrNotFound)

	status, err := ensureRefreshTestLanding(refreshTestActivities(mem, client, refreshTestMaxAge))

	require.NoError(t, err)
	assert.Equal(t, playerLandingCached, status)
	assert.Equal(t, cachedGamesPlayed, cachedGP(t, mem))
	assert.False(t, mem.Exists(context.Background(), resource.MissingPlayerLanding{PlayerID: refreshTestPlayerID}.Path()))
	client.AssertExpectations(t)
}

func TestEnsurePlayerLandingCachedStaleDownloadErrorKeepsCachedPage(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	cacheLanding(t, mem, refreshTestLanding(true, cachedGamesPlayed), refreshTestMaxAge)
	downloadErr := errors.New("connection reset")
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, downloadErr)

	_, err := ensureRefreshTestLanding(refreshTestActivities(mem, client, refreshTestMaxAge))

	require.ErrorIs(t, err, downloadErr)
	assert.Equal(t, cachedGamesPlayed, cachedGP(t, mem))
}

func TestEnsurePlayerLandingCachedStorageErrors(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("i/o error")
	tests := []struct {
		name    string
		storage func(*store.MemStorage) *statFailStorage
		wantMsg string
	}{
		{"stat", func(m *store.MemStorage) *statFailStorage {
			return &statFailStorage{MemStorage: m, statErr: storageErr}
		},
			"stat player 8478402 landing: i/o error"},
		{"read", func(m *store.MemStorage) *statFailStorage {
			return &statFailStorage{MemStorage: m, readErr: storageErr}
		},
			"read player 8478402 landing: i/o error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := store.NewMemStorage()
			cacheLanding(t, mem, refreshTestLanding(true, cachedGamesPlayed), refreshTestMaxAge)
			client := &MockNHLClient{}

			_, err := ensureRefreshTestLanding(refreshTestActivities(tc.storage(mem), client, refreshTestMaxAge))

			require.ErrorIs(t, err, storageErr)
			assert.EqualError(t, err, tc.wantMsg)
			client.AssertNotCalled(t, "PlayerLanding", mock.Anything, mock.Anything)
		})
	}
}

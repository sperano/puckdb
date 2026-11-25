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

// statFailStorage fails Stat, Read (after readsBeforeErr successful reads)
// or Write with the set error, records the writes and delegates the rest to
// MemStorage.
type statFailStorage struct {
	*store.MemStorage
	statErr        error
	readErr        error
	readsBeforeErr int
	writeErr       error
	writes         map[string][][]byte
}

func (s *statFailStorage) Write(ctx context.Context, path string, data []byte) error {
	if s.writes == nil {
		s.writes = make(map[string][][]byte)
	}
	s.writes[path] = append(s.writes[path], data)
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.MemStorage.Write(ctx, path, data)
}

func (s *statFailStorage) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	if s.statErr != nil {
		return nil, s.statErr
	}
	return s.MemStorage.Stat(ctx, path)
}

func (s *statFailStorage) Read(ctx context.Context, path string) ([]byte, error) {
	if s.readErr != nil {
		if s.readsBeforeErr == 0 {
			return nil, s.readErr
		}
		s.readsBeforeErr--
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
	return ensureRefreshTestLandingCtx(context.Background(), act)
}

func ensureRefreshTestLandingCtx(ctx context.Context, act *Activities) (playerLandingStatus, error) {
	return act.ensurePlayerLandingCached(ctx, refreshTestPlayerID,
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

// cacheStaleLandingBytes stores data as the cached page, refreshTestMaxAge
// old, and returns the storage recording writes over it.
func cacheStaleLandingBytes(t *testing.T, data []byte) *statFailStorage {
	t.Helper()
	mem := store.NewMemStorage()
	mem.SetFileWithTime(resource.PlayerLanding{PlayerID: refreshTestPlayerID}.Path(), data,
		refreshTestNow.Add(-refreshTestMaxAge))
	return &statFailStorage{MemStorage: mem}
}

func TestEnsurePlayerLandingCachedKeepsStaleLandingOn404(t *testing.T) {
	t.Parallel()
	parsed, err := json.Marshal(refreshTestLanding(true, cachedGamesPlayed))
	require.NoError(t, err)
	tests := []struct {
		name string
		data []byte
	}{
		{"parsed page", parsed},
		{"unreadable page", []byte("{not json")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := cacheStaleLandingBytes(t, tc.data)
			client := &MockNHLClient{}
			client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, nhl.ErrNotFound)

			status, err := ensureRefreshTestLanding(refreshTestActivities(s, client, refreshTestMaxAge))

			require.NoError(t, err)
			assert.Equal(t, playerLandingCached, status)
			// The page is rewritten with its own bytes, so its age restarts
			// and the NHL is not asked again before LandingMaxAge.
			landingPath := resource.PlayerLanding{PlayerID: refreshTestPlayerID}.Path()
			assert.Equal(t, map[string][][]byte{landingPath: {tc.data}}, s.writes)
			assert.Equal(t, tc.data, s.Get(landingPath))
			assert.False(t, s.Has(resource.MissingPlayerLanding{PlayerID: refreshTestPlayerID}.Path()))
			client.AssertExpectations(t)
		})
	}
}

func TestEnsurePlayerLandingCachedStaleDownloadErrorUsesCachedPage(t *testing.T) {
	t.Parallel()
	parsed, err := json.Marshal(refreshTestLanding(true, cachedGamesPlayed))
	require.NoError(t, err)
	s := cacheStaleLandingBytes(t, parsed)
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, errors.New("connection reset"))

	status, err := ensureRefreshTestLanding(refreshTestActivities(s, client, refreshTestMaxAge))

	require.NoError(t, err)
	assert.Equal(t, playerLandingCached, status)
	assert.Empty(t, s.writes, "a transient failure leaves the page stale, to be retried next run")
	assert.Equal(t, cachedGamesPlayed, cachedGP(t, s))
	client.AssertExpectations(t)
}

func TestEnsurePlayerLandingCachedUnreadableStaleDownloadErrorFails(t *testing.T) {
	t.Parallel()
	unreadable := []byte("{not json")
	s := cacheStaleLandingBytes(t, unreadable)
	downloadErr := errors.New("connection reset")
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, downloadErr)

	_, err := ensureRefreshTestLanding(refreshTestActivities(s, client, refreshTestMaxAge))

	require.ErrorIs(t, err, downloadErr, "an unreadable page cannot stand in, so the batch retries")
	assert.Empty(t, s.writes)
	landingPath := resource.PlayerLanding{PlayerID: refreshTestPlayerID}.Path()
	assert.Equal(t, unreadable, s.Get(landingPath))
	assert.False(t, s.Has(resource.MissingPlayerLanding{PlayerID: refreshTestPlayerID}.Path()))
	client.AssertExpectations(t)
}

func TestEnsurePlayerLandingCachedStaleRefreshCancelled(t *testing.T) {
	t.Parallel()
	parsed, err := json.Marshal(refreshTestLanding(true, cachedGamesPlayed))
	require.NoError(t, err)
	s := cacheStaleLandingBytes(t, parsed)
	ctx, cancel := context.WithCancel(context.Background())
	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).
		Run(func(mock.Arguments) { cancel() }).Return(nil, context.Canceled)

	_, err = ensureRefreshTestLandingCtx(ctx, refreshTestActivities(s, client, refreshTestMaxAge))

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, s.writes)
}

func TestEnsurePlayerLandingCachedStale404RenewErrors(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("i/o error")
	tests := []struct {
		name    string
		set     func(*statFailStorage)
		wantMsg string
	}{
		{"read", func(s *statFailStorage) { s.readErr, s.readsBeforeErr = storageErr, 1 },
			"read stale player 8478402 landing: i/o error"},
		{"write", func(s *statFailStorage) { s.writeErr = storageErr },
			"renew stale player 8478402 landing: i/o error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := json.Marshal(refreshTestLanding(true, cachedGamesPlayed))
			require.NoError(t, err)
			s := cacheStaleLandingBytes(t, parsed)
			tc.set(s)
			client := &MockNHLClient{}
			client.On("PlayerLanding", mock.Anything, refreshTestPlayerID).Return(nil, nhl.ErrNotFound)

			_, err = ensureRefreshTestLanding(refreshTestActivities(s, client, refreshTestMaxAge))

			require.ErrorIs(t, err, storageErr)
			assert.EqualError(t, err, tc.wantMsg)
		})
	}
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

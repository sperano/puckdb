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

func TestDownloadFranchises_CacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	// Pre-populate franchises in cache
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
	}
	franchisesJSON, err := json.Marshal(franchises)
	require.NoError(t, err)
	require.NoError(t, repos.Franchise.Save(franchisesJSON))

	result, err := downloadFranchisesImpl(ctx, repos, client)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	assert.True(t, result.FromCache)
	client.AssertNotCalled(t, "Franchises")
}

func TestDownloadFranchises_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	// API returns franchises
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
		{ID: 3, FullName: "Boston Bruins"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	result, err := downloadFranchisesImpl(ctx, repos, client)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Count)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)

	// Verify data was cached
	assert.True(t, repos.Franchise.Exists())
}

func TestDownloadFranchises_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	// API returns error
	client.On("Franchises", ctx).Return(nil, errors.New("API unavailable"))

	result, err := downloadFranchisesImpl(ctx, repos, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, result.Count)
	client.AssertExpectations(t)
}

func TestDownloadFranchises_CacheCorrupt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	client := &MockNHLClient{}

	// Cache exists but corrupt JSON
	mem.SetFile(store.FranchisesPath(), []byte("not valid json"))

	// Falls back to API
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	result, err := downloadFranchisesImpl(ctx, repos, client)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)
}

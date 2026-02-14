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

func TestDownloadFranchises_CacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Franchises file exists in cache
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
	}
	franchisesJSON, _ := json.Marshal(franchises)

	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(franchisesJSON, nil)

	result, err := downloadFranchisesImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	assert.True(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertNotCalled(t, "Franchises")
}

func TestDownloadFranchises_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Cache miss
	fs.On("Exists", file).Return(false)

	// API returns franchises
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
		{ID: 2, FullName: "Toronto Maple Leafs"},
		{ID: 3, FullName: "Boston Bruins"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	// Write to cache succeeds
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadFranchisesImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadFranchises_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Cache miss
	fs.On("Exists", file).Return(false)

	// API returns error
	client.On("Franchises", ctx).Return(nil, errors.New("API unavailable"))

	result, err := downloadFranchisesImpl(ctx, fs, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, result.Count)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadFranchises_CacheCorrupt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Cache exists but corrupt JSON
	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return([]byte("not valid json"), nil)

	// Falls back to API
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	// Write to cache succeeds
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadFranchisesImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadFranchises_CacheReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Cache exists but read fails
	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(nil, errors.New("disk error"))

	// Falls back to API
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	// Write to cache succeeds
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadFranchisesImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadFranchises_WriteError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.FranchisesFile{}

	// Cache miss
	fs.On("Exists", file).Return(false)

	// API returns franchises
	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens"},
	}
	client.On("Franchises", ctx).Return(franchises, nil)

	// Write to cache fails (but operation still succeeds)
	fs.On("Write", file, mock.Anything).Return(errors.New("disk full"))

	result, err := downloadFranchisesImpl(ctx, fs, client)

	// Should still succeed - cache write failure is not fatal
	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

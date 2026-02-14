package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUpsertFranchises_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
		{ID: 2, FullName: "Toronto Maple Leafs", TeamCommonName: "Maple Leafs", TeamPlaceName: "Toronto"},
		{ID: 3, FullName: "Boston Bruins", TeamCommonName: "Bruins", TeamPlaceName: "Boston"},
	}

	upserter.On("UpsertFranchise", ctx, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(nil).Times(3)

	result, err := upsertFranchisesImpl(ctx, upserter, franchises)

	require.NoError(t, err)
	assert.Equal(t, 3, result.FranchisesUpserted)
	upserter.AssertExpectations(t)
}

func TestUpsertFranchises_EmptyInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockFranchiseUpserter{}

	result, err := upsertFranchisesImpl(ctx, upserter, []nhl.Franchise{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.FranchisesUpserted)
	upserter.AssertNotCalled(t, "UpsertFranchise")
}

func TestUpsertFranchises_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
	}

	upserter.On("UpsertFranchise", ctx, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(errors.New("database error"))

	result, err := upsertFranchisesImpl(ctx, upserter, franchises)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert franchise")
	assert.Contains(t, err.Error(), "Montreal Canadiens")
	assert.Equal(t, 0, result.FranchisesUpserted)
	upserter.AssertExpectations(t)
}

func TestUpsertFranchises_PartialFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockFranchiseUpserter{}

	franchises := []nhl.Franchise{
		{ID: 1, FullName: "Montreal Canadiens", TeamCommonName: "Canadiens", TeamPlaceName: "Montreal"},
		{ID: 2, FullName: "Toronto Maple Leafs", TeamCommonName: "Maple Leafs", TeamPlaceName: "Toronto"},
	}

	// First succeeds, second fails
	upserter.On("UpsertFranchise", ctx, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(nil).Once()
	upserter.On("UpsertFranchise", ctx, mock.AnythingOfType("sqlcdb.UpsertFranchiseParams")).Return(errors.New("database error")).Once()

	result, err := upsertFranchisesImpl(ctx, upserter, franchises)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Toronto Maple Leafs")
	assert.Equal(t, 1, result.FranchisesUpserted) // First one succeeded
	upserter.AssertExpectations(t)
}

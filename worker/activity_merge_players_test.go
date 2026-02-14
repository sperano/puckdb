package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergePlayerBatchesFromRedisImpl_EmptyKeys(t *testing.T) {
	ctx := context.Background()
	client, _ := redismock.NewClientMock()

	result, err := mergePlayerBatchesFromRedisImpl(ctx, client, []string{}, "yahoo")

	assert.NoError(t, err)
	assert.Len(t, result, 0)
}

func TestMergePlayerBatchesFromRedisImpl_YahooSource(t *testing.T) {
	ctx := context.Background()
	client, mock := redismock.NewClientMock()

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:           8476453,
			FirstName:    "Connor",
			LastName:     "McDavid",
			HasYahooData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get and Del
	mock.ExpectGet("test-key").SetVal(buf.String())
	mock.ExpectDel("test-key").SetVal(1)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, client, []string{"test-key"}, "yahoo")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMergePlayerBatchesFromRedisImpl_BoxscoreSource(t *testing.T) {
	ctx := context.Background()
	client, mock := redismock.NewClientMock()

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			HasBoxscoreData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get and Del
	mock.ExpectGet("test-key").SetVal(buf.String())
	mock.ExpectDel("test-key").SetVal(1)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, client, []string{"test-key"}, "boxscore")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.True(t, result[8476453].HasBoxscoreData)
	require.NoError(t, mock.ExpectationsWereMet())
}

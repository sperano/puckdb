package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLoadUnmatchedYahooPlayers_EmptySet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{})

	result, err := loadUnmatchedYahooPlayersImpl(ctx, client)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Return two unmatched IDs
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"123", "456"})

	// Return player details for each
	player1 := store.YahooPlayer{
		YahooID:      123,
		FirstName:    "Connor",
		LastName:     "McDavid",
		Team:         "EDM",
		JerseyNumber: 97,
	}
	player1JSON, _ := json.Marshal(player1)
	mockRedis.ExpectHGet(YahooIDPoolKey, "123").SetVal(string(player1JSON))

	player2 := store.YahooPlayer{
		YahooID:      456,
		FirstName:    "Leon",
		LastName:     "Draisaitl",
		Team:         "EDM",
		JerseyNumber: 29,
	}
	player2JSON, _ := json.Marshal(player2)
	mockRedis.ExpectHGet(YahooIDPoolKey, "456").SetVal(string(player2JSON))

	result, err := loadUnmatchedYahooPlayersImpl(ctx, client)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_SMembersError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("redis connection failed"))

	result, err := loadUnmatchedYahooPlayersImpl(ctx, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection failed")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_PlayerDetailsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Return one unmatched ID
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"999"})

	// Player details not found
	mockRedis.ExpectHGet(YahooIDPoolKey, "999").SetErr(errors.New("redis: nil"))

	result, err := loadUnmatchedYahooPlayersImpl(ctx, client)

	// Should still return with basic info (YahooID only)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, store.YahooPlayerID(999), result[0].YahooID)
	assert.Empty(t, result[0].FirstName)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCleanupYahooIDPool_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetVal(2)

	err := cleanupYahooIDPoolImpl(ctx, client)

	require.NoError(t, err)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCleanupYahooIDPool_Error(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetErr(errors.New("redis error"))

	err := cleanupYahooIDPoolImpl(ctx, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis error")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestListPlayerLandingIDs_Empty(t *testing.T) {
	t.Parallel()

	fs := NewMockFileSystem()

	fs.On("ListFiles", store.PlayerLandingFile{}, mock.AnythingOfType("store.FilenameParser")).Return([]store.File{}, nil)

	result, err := listPlayerLandingIDsImpl(fs)

	require.NoError(t, err)
	assert.Empty(t, result)
	fs.AssertExpectations(t)
}

func TestListPlayerLandingIDs_Success(t *testing.T) {
	t.Parallel()

	fs := NewMockFileSystem()

	files := []store.File{
		store.PlayerLandingFile{PlayerID: nhl.PlayerID(8478402)},
		store.PlayerLandingFile{PlayerID: nhl.PlayerID(8477934)},
		store.PlayerLandingFile{PlayerID: nhl.PlayerID(8480069)},
	}
	fs.On("ListFiles", store.PlayerLandingFile{}, mock.AnythingOfType("store.FilenameParser")).Return(files, nil)

	result, err := listPlayerLandingIDsImpl(fs)

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Contains(t, result, int64(8478402))
	assert.Contains(t, result, int64(8477934))
	assert.Contains(t, result, int64(8480069))
	fs.AssertExpectations(t)
}

func TestListPlayerLandingIDs_Error(t *testing.T) {
	t.Parallel()

	fs := NewMockFileSystem()

	fs.On("ListFiles", store.PlayerLandingFile{}, mock.AnythingOfType("store.FilenameParser")).Return(nil, errors.New("filesystem error"))

	result, err := listPlayerLandingIDsImpl(fs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "filesystem error")
	assert.Nil(t, result)
	fs.AssertExpectations(t)
}

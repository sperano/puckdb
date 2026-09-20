package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoxscorePlayersKey(t *testing.T) {
	t.Parallel()

	season := nhl.NewSeason(2024)
	key := BoxscorePlayersKey(season)
	assert.Equal(t, "puckdb:boxscore-players:20242025", key)
}

func TestSaveAndLoadBoxscorePlayers(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	season := nhl.NewSeason(2024)
	key := BoxscorePlayersKey(season)
	ttl := 10 * time.Minute

	players := []store.BoxscorePlayer{
		{ID: 1, FirstName: "Connor", LastName: "McDavid", Position: "C"},
		{ID: 2, FirstName: "Leon", LastName: "Draisaitl", Position: "C"},
	}

	// Encode expected Get payload independently so Save and Load are tested end-to-end.
	var gobBuf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&gobBuf).Encode(players))
	gobBytes := gobBuf.Bytes()

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(key, "x", ttl).SetVal("OK")

	err := SaveBoxscorePlayers(ctx, client, season, players, ttl)
	require.NoError(t, err)

	mock.ExpectGet(key).SetVal(string(gobBytes))

	got, err := LoadBoxscorePlayers(ctx, client, season)
	require.NoError(t, err)
	assert.Equal(t, players, got)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadBoxscorePlayers_Miss(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	season := nhl.NewSeason(2024)
	key := BoxscorePlayersKey(season)

	// Doc contract: a missing key returns a nil slice and no error.
	mock.ExpectGet(key).RedisNil()

	got, err := LoadBoxscorePlayers(ctx, client, season)
	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadBoxscorePlayers_Error(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	season := nhl.NewSeason(2024)
	key := BoxscorePlayersKey(season)
	redisErr := errors.New("connection refused")

	mock.ExpectGet(key).SetErr(redisErr)

	got, err := LoadBoxscorePlayers(ctx, client, season)
	require.Error(t, err)
	assert.ErrorIs(t, err, redisErr)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAllBoxscorePlayers_Miss(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()

	// Doc contract: a missing key returns a nil slice and no error.
	mock.ExpectGet(AllBoxscorePlayersKey).RedisNil()

	got, err := LoadAllBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAllBoxscorePlayers_Error(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	redisErr := errors.New("connection refused")

	mock.ExpectGet(AllBoxscorePlayersKey).SetErr(redisErr)

	got, err := LoadAllBoxscorePlayers(ctx, client)
	require.Error(t, err)
	assert.ErrorIs(t, err, redisErr)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAndLoadAllBoxscorePlayers(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	ttl := 30 * time.Minute

	players := []store.BoxscorePlayer{
		{ID: 8, FirstName: "Alex", LastName: "Ovechkin", Position: "LW"},
	}

	var gobBuf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&gobBuf).Encode(players))
	gobBytes := gobBuf.Bytes()

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(AllBoxscorePlayersKey, "x", ttl).SetVal("OK")

	err := SaveAllBoxscorePlayers(ctx, client, players, ttl)
	require.NoError(t, err)

	mock.ExpectGet(AllBoxscorePlayersKey).SetVal(string(gobBytes))

	got, err := LoadAllBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, players, got)

	require.NoError(t, mock.ExpectationsWereMet())
}

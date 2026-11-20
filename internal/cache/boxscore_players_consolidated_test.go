package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniredisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

func gobBytes(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(v))
	return buf.Bytes()
}

func TestSaveAndLoadConsolidatedBoxscorePlayers(t *testing.T) {
	t.Parallel()
	server, client := newMiniredisClient(t)
	ctx := context.Background()
	const ttl = 30 * time.Minute
	players := []store.BoxscorePlayer{{ID: 8471214, FirstName: "Alex", LastName: "Ovechkin", Position: "LW"}}

	require.NoError(t, SaveConsolidatedBoxscorePlayers(ctx, client, players, []int{2024, 2025}, ttl))
	assert.Equal(t, ttl, server.TTL(AllBoxscorePlayersKey))
	assert.Equal(t, ttl, server.TTL(AllBoxscorePlayerSeasonsKey))

	gotPlayers, gotSeasons, err := LoadConsolidatedBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, players, gotPlayers)
	assert.Equal(t, []int{2024, 2025}, gotSeasons)

	all, err := LoadAllBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, players, all, "the per-key reader sees the same set")
}

// Both keys are written in one MULTI/EXEC, so no reader sees the new seasons
// with the previous player set.
func TestSaveConsolidatedBoxscorePlayers_WritesBothKeysInOneTransaction(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	players := []store.BoxscorePlayer{{ID: 1}}

	mock.ExpectTxPipeline()
	mock.ExpectSet(AllBoxscorePlayersKey, gobBytes(t, players), time.Minute).SetVal("OK")
	mock.ExpectSet(AllBoxscorePlayerSeasonsKey, gobBytes(t, []int{2025}), time.Minute).SetVal("OK")
	mock.ExpectTxPipelineExec()

	require.NoError(t, SaveConsolidatedBoxscorePlayers(ctx, client, players, []int{2025}, time.Minute))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveConsolidatedBoxscorePlayers_Error(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	ctx := context.Background()
	redisErr := errors.New("connection refused")
	players := []store.BoxscorePlayer{{ID: 1}}

	mock.ExpectTxPipeline()
	mock.ExpectSet(AllBoxscorePlayersKey, gobBytes(t, players), time.Minute).SetVal("OK")
	mock.ExpectSet(AllBoxscorePlayerSeasonsKey, gobBytes(t, []int{2025}), time.Minute).SetErr(redisErr)
	// redismock answers a transaction's commands in order and stops at the
	// first error, so no EXEC is expected after the failing SET.

	err := SaveConsolidatedBoxscorePlayers(ctx, client, players, []int{2025}, time.Minute)
	assert.ErrorIs(t, err, redisErr)
	assert.ErrorContains(t, err, "save consolidated boxscore players to redis")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadConsolidatedBoxscorePlayers_MissingKeys(t *testing.T) {
	t.Parallel()
	server, client := newMiniredisClient(t)
	ctx := context.Background()

	players, seasons, err := LoadConsolidatedBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Nil(t, players, "no set")
	assert.Nil(t, seasons, "no seasons")

	set := []store.BoxscorePlayer{{ID: 1}}
	require.NoError(t, server.Set(AllBoxscorePlayersKey, string(gobBytes(t, set))))
	players, seasons, err = LoadConsolidatedBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, set, players)
	assert.Nil(t, seasons, "a set saved without seasons has unknown seasons")

	require.NoError(t, server.Set(AllBoxscorePlayerSeasonsKey, string(gobBytes(t, []int{}))))
	_, seasons, err = LoadConsolidatedBoxscorePlayers(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, []int{}, seasons, "an empty saved list is known: no season")
}

func TestLoadConsolidatedBoxscorePlayers_Errors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client, mock := redismock.NewClientMock()
	redisErr := errors.New("connection refused")
	mock.ExpectMGet(AllBoxscorePlayersKey, AllBoxscorePlayerSeasonsKey).SetErr(redisErr)
	_, _, err := LoadConsolidatedBoxscorePlayers(ctx, client)
	assert.ErrorIs(t, err, redisErr)
	require.NoError(t, mock.ExpectationsWereMet())

	server, real := newMiniredisClient(t)
	require.NoError(t, server.Set(AllBoxscorePlayersKey, "not gob"))
	_, _, err = LoadConsolidatedBoxscorePlayers(ctx, real)
	assert.ErrorContains(t, err, "decode all boxscore players")

	require.NoError(t, server.Set(AllBoxscorePlayersKey, string(gobBytes(t, []store.BoxscorePlayer{{ID: 1}}))))
	require.NoError(t, server.Set(AllBoxscorePlayerSeasonsKey, "not gob"))
	_, _, err = LoadConsolidatedBoxscorePlayers(ctx, real)
	assert.ErrorContains(t, err, "decode boxscore player seasons")
}

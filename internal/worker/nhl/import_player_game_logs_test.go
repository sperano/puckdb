package nhl

import (
	"context"
	"encoding/json"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestImportPlayerGameLogsBatchCountsPlayersWithoutLog checks that a batch
// accounts for every player: imported, without a cached log, or unreadable.
func TestImportPlayerGameLogsBatchCountsPlayersWithoutLog(t *testing.T) {
	mem := store.NewMemStorage()
	season := nhlapi.NewSeason(2023)
	const imported, withoutLog, unreadable = int64(1), int64(2), int64(3)
	logRes := func(id int64) resource.PlayerGameLog {
		return resource.PlayerGameLog{PlayerID: nhlapi.NewPlayerID(id), Season: season, GameType: nhlapi.GameTypeRegularSeason}
	}
	data, err := json.Marshal(&nhlapi.PlayerGameLog{Season: season, GameType: nhlapi.GameTypeRegularSeason})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), logRes(imported).Path(), data))
	require.NoError(t, mem.Write(context.Background(), logRes(unreadable).Path(), []byte("{not json")))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	act := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: &MockQueries{}}
	env.RegisterActivity(act.ImportPlayerGameLogsBatch)
	future, err := env.ExecuteActivity(act.ImportPlayerGameLogsBatch, ImportPlayerGameLogsBatchInput{
		Season: season, PlayerIDs: []int64{imported, withoutLog, unreadable},
	})
	require.NoError(t, err)

	var result ImportPlayerGameLogsBatchResult
	require.NoError(t, future.Get(&result))
	assert.Equal(t, 1, result.PlayersProcessed)
	assert.Equal(t, 1, result.PlayersWithoutLog)
	assert.Equal(t, 1, result.PlayersUnreadable)
}

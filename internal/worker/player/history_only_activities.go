package player

import (
	"context"
	"errors"

	"github.com/sperano/puckdb/internal/store"
)

var errRetiredYahooPagePool = errors.New("retired Yahoo page pool activity")

// These names remain registered for completed Temporal histories. New
// executions build the pool from committed game player lists.
func (*Activities) ListYahooPlayerFiles(context.Context) ([]store.YahooPlayerID, error) {
	return nil, errRetiredYahooPagePool
}

func (*Activities) ParseYahooPlayerBatch(context.Context, []store.YahooPlayerID) ([]store.YahooPlayer, error) {
	return nil, errRetiredYahooPagePool
}

func (*Activities) SaveYahooPlayersToRedis(context.Context, []store.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	return nil, errRetiredYahooPagePool
}

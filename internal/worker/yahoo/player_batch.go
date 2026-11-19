package yahoo

import (
	"context"
	"time"

	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// FetchYahooPlayerBatch is the retired player ID scan's batch: it downloads
// nothing and reports nothing found. It stays registered because a
// FetchYahooPlayersWorkflow execution recorded before the game player lists
// (the legacy scan, fetch_yahoo_players_legacy.go) still schedules it; such
// an execution finishes its batches at once and continues as new into the
// game player list download. The arguments are the scan's ID range and
// missing-marker TTL, kept so those histories' inputs still decode.
func (a *FetchActivities) FetchYahooPlayerBatch(ctx context.Context, startID, endID store.YahooPlayerID, _ time.Duration) (shared.FetchStats, error) {
	activity.GetLogger(ctx).Info("Skipping retired Yahoo player ID scan batch",
		"start_id", int(startID), "end_id", int(endID))
	return shared.FetchStats{}, nil
}

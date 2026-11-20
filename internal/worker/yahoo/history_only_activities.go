package yahoo

import (
	"context"
	"time"

	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// FetchYahooPlayerBatch stays registered for completed scan histories.
func (*FetchActivities) FetchYahooPlayerBatch(ctx context.Context, startID, endID store.YahooPlayerID, _ time.Duration) (shared.FetchStats, error) {
	activity.GetLogger(ctx).Info("Skipping retired Yahoo player ID scan batch",
		"start_id", int(startID), "end_id", int(endID))
	return shared.FetchStats{}, nil
}

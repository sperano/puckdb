package cache

import (
	"context"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
)

func GetSeasons(
	ctx context.Context,
	storage store.Storage,
	gobCache *GobCache,
) ([]nhl.SeasonInfo, core.DataOrigin, error) {
	logger := activity.GetLogger(ctx)
	seasons, origin, err := ReadParsedCached(ctx, storage, gobCache, resource.SeasonsManifest{})
	if err != nil {
		return nil, origin, fmt.Errorf("read seasons from %s: %w", origin, err)
	}
	logger.Debug("returned seasons", "count", len(seasons.Seasons), "origin", origin)
	return seasons.Seasons, origin, nil
}

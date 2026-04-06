package shared

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// FetchOrCache checks the cache (Redis gob → filesystem), and on miss calls
// fetch to retrieve the data, then persists to both layers via WriteParsedCached.
// Download metrics (hit/miss/error) are recorded automatically using the
// resource's FileType.
func FetchOrCache[T any](
	ctx context.Context,
	s store.Storage,
	gobCache *cache.GobCache,
	r core.ReadWritable[T],
	fetch func(ctx context.Context) (T, error),
) (T, core.DataOrigin, error) {
	obj, origin, err := cache.ReadParsedCached(ctx, s, gobCache, r)
	if err == nil {
		metrics.IncDownload(r.Type(), metrics.ResultHit)
		return obj, origin, nil
	}

	obj, err = fetch(ctx)
	if err != nil {
		var zero T
		metrics.IncDownload(r.Type(), metrics.ResultError)
		return zero, core.OriginUnknown, err
	}

	if writeErr := cache.WriteParsedCached(ctx, s, gobCache, r, obj); writeErr != nil {
		var zero T
		metrics.IncDownload(r.Type(), metrics.ResultError)
		return zero, core.OriginUnknown, fmt.Errorf("write %s to cache: %w", r.Type(), writeErr)
	}

	metrics.IncDownload(r.Type(), metrics.ResultMiss)
	return obj, core.OriginRemoteNHLAPI, nil
}

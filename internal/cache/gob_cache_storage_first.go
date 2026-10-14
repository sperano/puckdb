package cache

import (
	"context"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

// StorageFirst returns a view of the cache whose parsed reads skip the Redis
// lookup: ReadParsedCached parses the resource from storage, then overwrites
// its Redis entry with the result. Use it when a read must see the stored
// file, for example after a changed modification time made the caller read
// it: a Redis entry can outlive a rewrite of its file (a fetch whose Redis
// write failed, a concurrent fetch between its two writes) until its TTL.
// The view shares the Redis client and TTL settings; a nil cache stays nil.
func (cache *GobCache) StorageFirst() *GobCache {
	if cache == nil {
		return nil
	}
	view := *cache
	view.storageFirst = true
	return &view
}

func (cache *GobCache) readsStorageFirst() bool {
	return cache != nil && cache.storageFirst
}

// refreshParsed reads the resource from storage and overwrites its Redis
// entry, under the resource lock when lock is set.
func (cache *GobCache) refreshParsed[T any](
	ctx context.Context,
	s store.Storage,
	r core.Parseable[T],
	key string,
	lock bool,
) (T, core.DataOrigin, error) {
	if lock {
		release, err := cache.LockResource(ctx, key)
		if err != nil {
			var zero T
			return zero, core.OriginUnknown, err
		}
		defer release()
	}
	return cache.readParsedStorageAndCache(ctx, s, r, key)
}

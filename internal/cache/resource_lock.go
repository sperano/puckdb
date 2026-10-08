package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/bsm/redislock"
)

const (
	resourceCacheLockTTL            = 2 * time.Minute
	resourceCacheLockRetry          = 25 * time.Millisecond
	resourceCacheLockReleaseTimeout = 2 * time.Second
	resourceCacheLockPrefix         = "puckdb:resource-cache-lock:"
)

// LockResource serializes a cache-miss filesystem read with a coherent
// refresh across processes. A nil cache degrades to a no-op for tests and
// filesystem-only deployments.
func (cache *GobCache) LockResource(ctx context.Context, resourceKey string) (func(), error) {
	if cache == nil || cache.client == nil {
		return func() {}, nil
	}
	key := resourceCacheLockPrefix + resourceKey
	busyErr := fmt.Errorf("context ended before the lock was available: %w", redislock.ErrNotObtained)
	release, err := acquireRedisLock(ctx, cache.client, redisLockConfig{
		key:             key,
		ttl:             resourceCacheLockTTL,
		retry:           resourceCacheLockRetry,
		releaseTimeout:  resourceCacheLockReleaseTimeout,
		busyErr:         busyErr,
		releaseLogField: "resource_key",
		releaseLogValue: resourceKey,
		releaseLogMsg:   "failed to release resource cache lock",
	})
	if err != nil {
		return nil, fmt.Errorf("lock resource cache %s: %w", resourceKey, err)
	}
	return release, nil
}

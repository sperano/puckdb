package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsm/redislock"
	"github.com/rs/zerolog/log"
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
	options := &redislock.Options{RetryStrategy: redislock.LinearBackoff(resourceCacheLockRetry)}
	lock, err := redislock.New(cache.client).Obtain(ctx, key, resourceCacheLockTTL, options)
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, fmt.Errorf("lock resource cache %s: context ended before the lock was available: %w", resourceKey, err)
	}
	if err != nil {
		return nil, fmt.Errorf("lock resource cache %s: %w", resourceKey, err)
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceCacheLockReleaseTimeout)
		defer cancel()
		if err := lock.Release(releaseCtx); err != nil {
			log.Warn().Err(err).Str("resource_key", resourceKey).Msg("failed to release resource cache lock")
		}
	}, nil
}

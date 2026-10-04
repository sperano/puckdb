package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

// ErrNilCache is returned when a cache operation is called on a nil GobCache or client.
var ErrNilCache = errors.New("nil GobCache or client")

// GobCacheTTL is the default TTL for gob-encoded cache entries.
const GobCacheTTL = 60 * time.Minute

// GobCache provides gob-encoded object caching in Redis.
// This is more efficient than caching raw JSON bytes because:
// 1. Gob encoding/decoding is faster than JSON parsing
// 2. We skip the Parse step entirely on cache hits
type GobCache struct {
	client *redis.Client
	ttl    time.Duration
	config *GobCacheConfig
}

// GobCacheOption configures a GobCache. Pass to NewGobCache.
type GobCacheOption func(*GobCache)

// WithTTL overrides the default TTL.
func WithTTL(ttl time.Duration) GobCacheOption {
	return func(c *GobCache) { c.ttl = ttl }
}

// WithConfig attaches a per-type configuration. The config's DefaultTTL
// (when > 0) overrides the default TTL; per-type entries override that
// for specific file types.
func WithConfig(config *GobCacheConfig) GobCacheOption {
	return func(c *GobCache) {
		c.config = config
		if config != nil && config.DefaultTTL > 0 {
			c.ttl = config.DefaultTTL
		}
	}
}

// NewGobCache creates a new GobCache with the given options. With no options
// the cache uses GobCacheTTL as the default. Use WithTTL or WithConfig to
// customize. Matches the functional-options pattern used in nhl.NewClientConfig.
func NewGobCache(client *redis.Client, opts ...GobCacheOption) *GobCache {
	c := &GobCache{
		client: client,
		ttl:    GobCacheTTL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// resolveTTL returns the TTL for the given file type.
// Checks per-type config first, then falls back to the cache's default TTL.
func (cache *GobCache) resolveTTL(ft core.FileType) time.Duration {
	if cache == nil {
		return GobCacheTTL
	}
	if cache.config != nil {
		if resolved := cache.config.ttlFor(ft); resolved > 0 {
			return resolved
		}
	}
	return cache.ttl
}

// shouldCache returns whether the given file type should be stored in Redis.
func (cache *GobCache) shouldCache(ft core.FileType) bool {
	if cache == nil || cache.config == nil {
		return true
	}
	return cache.config.shouldCache(ft)
}

// Get retrieves a gob-encoded object from the cache.
// Returns (obj, true, nil) on hit, (zero, false, nil) on miss, (zero, false, err) on error.
func (cache *GobCache) Get[T any](ctx context.Context, key string) (T, bool, error) {
	var zero T

	if cache == nil || cache.client == nil {
		return zero, false, ErrNilCache
	}

	data, err := cache.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return zero, false, nil
		}
		return zero, false, fmt.Errorf("redis get %s: %w", key, err)
	}

	var obj T
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&obj); err != nil {
		// Stale/incompatible gob data (e.g., type registry changed between deploys).
		// Delete the poisoned key and return a cache miss so the caller re-fetches.
		log.Warn().Str("key", key).Err(err).Msg("gob decode failed, evicting stale cache entry")
		_ = cache.client.Del(ctx, key).Err()
		return zero, false, nil
	}

	return obj, true, nil
}

// Set stores a gob-encoded object in the cache using the default TTL.
func (cache *GobCache) Set[T any](ctx context.Context, key string, obj T) error {
	if cache == nil {
		return ErrNilCache
	}
	return cache.setWithTTL(ctx, key, obj, cache.ttl)
}

// setWithTTL stores a gob-encoded object in the cache with an explicit TTL.
func (cache *GobCache) setWithTTL[T any](ctx context.Context, key string, obj T, ttl time.Duration) error {
	if cache == nil || cache.client == nil {
		return ErrNilCache
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(obj); err != nil {
		return fmt.Errorf("gob encode %s: %w", key, err)
	}

	if err := cache.client.Set(ctx, key, buf.Bytes(), ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}

	return nil
}

// Delete removes an object from the cache.
func (cache *GobCache) Delete(ctx context.Context, key string) error {
	if cache == nil || cache.client == nil {
		return ErrNilCache
	}
	return cache.client.Del(ctx, key).Err()
}

// ReadParsedCached reads a resource with gob caching.
// On cache hit: returns the gob-decoded object (skips JSON parsing entirely).
// On cache miss: parses from storage and populates the cache.
// Respects per-type config: if skip_redis is set for the resource's type,
// the Redis layer is bypassed entirely and data is read from storage.
func (cache *GobCache) ReadParsedCached[T any](
	ctx context.Context,
	s store.Storage,
	r core.Parseable[T],
) (T, core.DataOrigin, error) {
	return cache.readParsedCached(ctx, s, r, RequiresCoherentResourceLock(r.Type()))
}

// ReadParsedCachedUnderResourceLock performs the cache read without acquiring
// the resource lock. The caller must already hold the lock returned by
// LockResource for this resource's Redis key.
func (cache *GobCache) ReadParsedCachedUnderResourceLock[T any](
	ctx context.Context,
	s store.Storage,
	r core.Parseable[T],
) (T, core.DataOrigin, error) {
	return cache.readParsedCached(ctx, s, r, false)
}

func (cache *GobCache) readParsedCached[T any](
	ctx context.Context,
	s store.Storage,
	r core.Parseable[T],
	lockOnMiss bool,
) (T, core.DataOrigin, error) {
	ft := r.Type()

	if cache != nil && !cache.shouldCache(ft) {
		obj, err := resource.ReadParsed(ctx, s, r)
		if err != nil {
			var zero T
			return zero, core.OriginFileSystem, err
		}
		return obj, core.OriginFileSystem, nil
	}

	key := core.RedisKey(r)
	obj, ok, err := cache.Get[T](ctx, key)
	if err != nil && !errors.Is(err, ErrNilCache) {
		var zero T
		return zero, core.OriginUnknown, fmt.Errorf("gob cache read: %w", err)
	}
	if ok {
		return obj, core.OriginRedis, nil
	}

	if lockOnMiss {
		release, lockErr := cache.LockResource(ctx, key)
		if lockErr != nil {
			var zero T
			return zero, core.OriginUnknown, lockErr
		}
		defer release()
		obj, ok, err = cache.Get[T](ctx, key)
		if err != nil {
			if !errors.Is(err, ErrNilCache) {
				var zero T
				return zero, core.OriginUnknown, fmt.Errorf("gob cache re-read: %w", err)
			}
		}
		if ok {
			return obj, core.OriginRedis, nil
		}
	}
	return cache.readParsedStorageAndCache(ctx, s, r, key)
}

func (cache *GobCache) readParsedStorageAndCache[T any](
	ctx context.Context,
	s store.Storage,
	r core.Parseable[T],
	key string,
) (T, core.DataOrigin, error) {
	obj, err := resource.ReadParsed(ctx, s, r)
	if err != nil {
		var zero T
		return zero, core.OriginFileSystem, err
	}

	ttl := cache.resolveTTL(r.Type())
	if err := cache.setWithTTL(ctx, key, obj, ttl); err != nil && !errors.Is(err, ErrNilCache) {
		log.Warn().Str("key", key).Err(err).Msg("gob cache write failed after storage read; serving read result")
	}

	return obj, core.OriginFileSystem, nil
}

// RequiresCoherentResourceLock identifies mutable Yahoo resources whose
// filesystem and Redis representations must change as one serialized unit.
func RequiresCoherentResourceLock(ft core.FileType) bool {
	return ft == core.League || ft == core.YahooDraftResults
}

// SetParsed stores an already-parsed resource in the gob cache, honouring the
// per-type skip_redis and TTL config. Use it when the raw bytes were written
// to storage separately (e.g. Yahoo XML kept verbatim), so WriteParsedCached's
// re-serialization is not wanted. A nil cache returns ErrNilCache.
func (cache *GobCache) SetParsed[T any](ctx context.Context, r core.Resource, obj T) error {
	if cache != nil && !cache.shouldCache(r.Type()) {
		return nil
	}
	return cache.setWithTTL(ctx, core.RedisKey(r), obj, cache.resolveTTL(r.Type()))
}

// WriteParsedCached writes an object to storage and updates the gob cache.
// This ensures cache consistency when data is modified.
// Respects per-type config: if skip_redis is set, only the storage write happens.
func (cache *GobCache) WriteParsedCached[T any](
	ctx context.Context,
	s store.Storage,
	r core.Formattable[T],
	obj T,
) error {
	// Write to storage first
	if err := resource.WriteParsed(ctx, s, r, obj); err != nil {
		return err
	}

	// Skip Redis if configured for this type.
	if cache != nil && !cache.shouldCache(r.Type()) {
		return nil
	}

	// Update cache with per-type TTL
	key := core.RedisKey(r)
	ttl := cache.resolveTTL(r.Type())
	if err := cache.setWithTTL(ctx, key, obj, ttl); err != nil {
		return fmt.Errorf("gob cache write: %w", err)
	}

	return nil
}

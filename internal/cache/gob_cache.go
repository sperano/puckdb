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
func Get[T any](cache *GobCache, ctx context.Context, key string) (T, bool, error) {
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
func Set[T any](cache *GobCache, ctx context.Context, key string, obj T) error {
	if cache == nil {
		return ErrNilCache
	}
	return setWithTTL(cache, ctx, key, obj, cache.ttl)
}

// setWithTTL stores a gob-encoded object in the cache with an explicit TTL.
func setWithTTL[T any](cache *GobCache, ctx context.Context, key string, obj T, ttl time.Duration) error {
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
func ReadParsedCached[T any](
	ctx context.Context,
	s store.Storage,
	cache *GobCache,
	r core.Parseable[T],
) (T, core.DataOrigin, error) {
	ft := r.Type()

	// Check if this file type should skip Redis entirely.
	if cache != nil && !cache.shouldCache(ft) {
		obj, err := resource.ReadParsed(ctx, s, r)
		if err != nil {
			var zero T
			return zero, core.OriginFileSystem, err
		}
		return obj, core.OriginFileSystem, nil
	}

	key := core.RedisKey(r)

	// Try gob cache first (fast path - no JSON parsing).
	// A nil cache (ErrNilCache) degrades gracefully to storage reads.
	obj, ok, err := Get[T](cache, ctx, key)
	if err != nil && !errors.Is(err, ErrNilCache) {
		var zero T
		return zero, core.OriginUnknown, fmt.Errorf("gob cache read: %w", err)
	}
	if ok {
		return obj, core.OriginRedis, nil
	}

	// Cache miss - parse from storage
	obj, err = resource.ReadParsed(ctx, s, r)
	if err != nil {
		var zero T
		return zero, core.OriginFileSystem, err
	}

	// Populate cache for next time. The storage read already succeeded, so a
	// best-effort cache write is not allowed to fail the read: log and continue.
	// ErrNilCache is expected when the cache degrades gracefully — don't log it.
	ttl := cache.resolveTTL(ft)
	if err := setWithTTL(cache, ctx, key, obj, ttl); err != nil && !errors.Is(err, ErrNilCache) {
		log.Warn().Str("key", key).Err(err).Msg("gob cache write failed after storage read; serving read result")
	}

	return obj, core.OriginFileSystem, nil
}

// SetParsed stores an already-parsed resource in the gob cache, honouring the
// per-type skip_redis and TTL config. Use it when the raw bytes were written
// to storage separately (e.g. Yahoo XML kept verbatim), so WriteParsedCached's
// re-serialization is not wanted. A nil cache returns ErrNilCache.
func SetParsed[T any](ctx context.Context, cache *GobCache, r core.Resource, obj T) error {
	if cache != nil && !cache.shouldCache(r.Type()) {
		return nil
	}
	return setWithTTL(cache, ctx, core.RedisKey(r), obj, cache.resolveTTL(r.Type()))
}

// WriteParsedCached writes an object to storage and updates the gob cache.
// This ensures cache consistency when data is modified.
// Respects per-type config: if skip_redis is set, only the storage write happens.
func WriteParsedCached[T any](
	ctx context.Context,
	s store.Storage,
	cache *GobCache,
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
	if err := setWithTTL(cache, ctx, key, obj, ttl); err != nil {
		return fmt.Errorf("gob cache write: %w", err)
	}

	return nil
}

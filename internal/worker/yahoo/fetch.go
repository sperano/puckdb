package yahoo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
)

// metricsAPIYahoo is the `api` label for HTTP metrics recorded against the Yahoo API.
const metricsAPIYahoo = "yahoo"

// Resource is a Yahoo Fantasy API resource that Fetcher can obtain: it has a
// storage path, a remote URL, a file type for metrics, and parses to FantasyContent.
type Resource interface {
	core.URLResource
	Parse(data []byte) (*store.FantasyContent, error)
}

// ErrDownload marks a failure to retrieve a resource from the Yahoo API, as
// opposed to a storage, parse or cache failure. Callers probing for the end of
// a series (e.g. matchup weeks) use it to tell "no more data" from a fault.
var ErrDownload = errors.New("download from Yahoo failed")

// Fetcher is the single cache-or-download primitive for Yahoo API resources.
// It reads validated content from the Redis → filesystem cache and, on a miss,
// downloads, validates, stores the raw XML verbatim, populates Redis, records
// download metrics and throttles.
//
// Raw XML is never written before it parses, so a malformed response cannot
// become a cache entry. An existing file that no longer parses is reported as
// a miss and overwritten by the next valid download.
type Fetcher struct {
	Storage  store.Storage
	GobCache *cache.GobCache
	Download shared.Downloader
	// Throttle runs after every successful download. Nil means SleepAfterYahooDownload.
	Throttle func()
}

// Fetch returns the parsed resource and where it came from: OriginRedis or
// OriginFileSystem for a cache hit, OriginRemoteYahooAPI for a download.
func (f Fetcher) Fetch(ctx context.Context, res Resource) (*store.FantasyContent, core.DataOrigin, error) {
	content, origin, err := f.readCached(ctx, res)
	if err != nil {
		metrics.IncDownload(res.Type(), metrics.ResultError)
		return nil, core.OriginUnknown, err
	}
	if content != nil {
		metrics.IncDownload(res.Type(), metrics.ResultHit)
		return content, origin, nil
	}
	return f.download(ctx, res)
}

// readCached reads through the gob cache and reports a miss as (nil, _, nil).
// A missing file is a miss; a file that exists but does not parse is logged
// and also treated as a miss so the caller re-downloads it. Any other failure
// (Redis or storage I/O) is returned rather than turned into a Yahoo download.
func (f Fetcher) readCached(ctx context.Context, res Resource) (*store.FantasyContent, core.DataOrigin, error) {
	content, origin, err := cache.ReadParsedCached(ctx, f.Storage, f.GobCache, res)
	var parseErr *resource.ParseError
	switch {
	case err == nil:
		return content, origin, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, core.OriginUnknown, nil
	case errors.As(err, &parseErr):
		log.Warn().Str("path", res.Path()).Err(err).Msg("cached Yahoo file is malformed, re-downloading")
		return nil, core.OriginUnknown, nil
	default:
		return nil, core.OriginUnknown, fmt.Errorf("read cached %s: %w", res.Type(), err)
	}
}

// download fetches, validates, stores and caches the resource.
func (f Fetcher) download(ctx context.Context, res Resource) (*store.FantasyContent, core.DataOrigin, error) {
	ft := res.Type()

	start := time.Now()
	raw, err := f.Download(ctx, res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP(metricsAPIYahoo, http.MethodGet, 0, duration, 0)
		metrics.IncDownload(ft, metrics.ResultError)
		return nil, core.OriginUnknown, fmt.Errorf("%w: %s: %w", ErrDownload, ft, err)
	}
	metrics.ObserveHTTP(metricsAPIYahoo, http.MethodGet, http.StatusOK, duration, len(raw))

	// Validate before storing so a bad response never becomes a cache entry.
	content, err := res.Parse(raw)
	if err != nil {
		metrics.IncDownload(ft, metrics.ResultError)
		return nil, core.OriginUnknown, err
	}
	if err := f.Storage.Write(ctx, res.Path(), raw); err != nil {
		metrics.IncDownload(ft, metrics.ResultError)
		return nil, core.OriginUnknown, fmt.Errorf("save %s: %w", ft, err)
	}

	// Storage is the source of truth; a Redis write failure is not allowed to
	// fail the fetch (the next read repopulates Redis from the file).
	if err := cache.SetParsed(ctx, f.GobCache, res, content); err != nil && !errors.Is(err, cache.ErrNilCache) {
		log.Warn().Str("path", res.Path()).Err(err).Msg("gob cache write failed after Yahoo download; continuing")
	}

	metrics.IncDownload(ft, metrics.ResultMiss)
	f.throttle()
	return content, core.OriginRemoteYahooAPI, nil
}

func (f Fetcher) throttle() {
	if f.Throttle != nil {
		f.Throttle()
		return
	}
	SleepAfterYahooDownload()
}

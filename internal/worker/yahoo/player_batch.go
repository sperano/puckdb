package yahoo

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

const (
	// yahooCanaryPlayerID is a player whose Yahoo page exists and has existed
	// for years (Connor McDavid). When fresh 404s pile up, a canary request
	// tells a throttled Yahoo (the canary 404s too) from genuinely absent ids
	// (the canary answers 200).
	yahooCanaryPlayerID store.YahooPlayerID = 6743

	// notFoundVerifyThreshold is how many unverified fresh 404s a batch buffers
	// before it checks the canary. It bounds the damage of a throttle (at most
	// this many wasted requests per batch before the batch aborts) while
	// costing one extra request per this many 404s in the legitimately empty
	// tail of the id range (ids above the highest real player all 404).
	notFoundVerifyThreshold = 10
)

// errYahooThrottled marks a batch aborted because Yahoo stopped serving a
// page that is known to exist. It is retryable: Temporal backs the activity
// off, and the 404s are never recorded.
var errYahooThrottled = fmt.Errorf("yahoo is not serving known player pages (canary %d returned 404): treating 404s as throttling", yahooCanaryPlayerID)

// notFoundBuffer holds a batch's fresh 404s until a canary request has shown
// that Yahoo is serving real pages. Why a canary instead of a "consecutive 404"
// rule: the 2026-01-31 incident looked identical to the empty tail (a whole
// batch of 404s with no success), so no count of 404s alone can tell them
// apart without either failing the legitimate tail forever or missing a storm.
type notFoundBuffer struct {
	storage    store.Storage
	downloader shared.Downloader
	pending    []store.YahooPlayerID
}

// add buffers a fresh 404 and, once notFoundVerifyThreshold are pending,
// verifies and flushes them.
func (b *notFoundBuffer) add(ctx context.Context, id store.YahooPlayerID) error {
	b.pending = append(b.pending, id)
	if len(b.pending) < notFoundVerifyThreshold {
		return nil
	}
	return b.flush(ctx)
}

// flush checks the canary and, if Yahoo is serving, writes the pending
// markers. On a failed check the pending ids are dropped unmarked.
func (b *notFoundBuffer) flush(ctx context.Context) error {
	if len(b.pending) == 0 {
		return nil
	}
	if err := b.verifyServing(ctx); err != nil {
		log.Warn().Int("unmarked_404s", len(b.pending)).Int("first_id", int(b.pending[0])).Err(err).Msg("Not writing Yahoo missing markers")
		b.pending = nil
		return err
	}
	for _, id := range b.pending {
		if err := writeMissingMarker(ctx, b.storage, id); err != nil {
			return err
		}
	}
	b.pending = b.pending[:0]
	return nil
}

// verifyServing requests the canary page directly, bypassing the cache.
func (b *notFoundBuffer) verifyServing(ctx context.Context) error {
	_, err := b.downloader(ctx, resource.YahooPlayer{PlayerID: yahooCanaryPlayerID}.URL())
	switch {
	case err == nil:
		return nil
	case isYahooNotFound(err):
		return errYahooThrottled
	default:
		return fmt.Errorf("canary download player %d: %w", yahooCanaryPlayerID, err)
	}
}

// FetchYahooPlayerBatch fetches a range of Yahoo player pages (from cache or network).
// Processes players from startID to endID (inclusive).
// Each player uses targeted stat calls to check cache status, which is faster
// on JuiceFS than listing the full 34k-entry missing directory per batch.
//
// A missing marker younger than missingTTL means "skip"; an older one is
// ignored and the page is fetched again. 404s are buffered and only recorded
// as markers once a canary request confirms Yahoo is serving real pages (see
// notFoundBuffer); otherwise the batch fails with a retryable error.
func (a *FetchActivities) FetchYahooPlayerBatch(ctx context.Context, startID, endID store.YahooPlayerID, missingTTL time.Duration) (shared.FetchStats, error) {
	var result shared.FetchStats
	logger := activity.GetLogger(ctx)
	logger.Debug("FetchYahooPlayerBatch started", "start_id", startID, "end_id", endID)

	notFound := &notFoundBuffer{storage: a.Storage, downloader: a.PublicDownloader}
	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		activity.RecordHeartbeat(ctx, playerID)

		status, err := fetchYahooPlayerImpl(ctx, a.Storage, a.PublicDownloader, playerID, missingTTL, time.Now())
		if err != nil {
			return result, err
		}
		switch status {
		case fetchStatusDownloaded:
			result.Downloaded++
		case fetchStatusMissing:
			result.Missing++
		case fetchStatusCached:
			result.CacheHits++
		case fetchStatusNotFound:
			result.Missing++
			if err := notFound.add(ctx, playerID); err != nil {
				return result, err
			}
		}
	}
	return result, notFound.flush(ctx)
}

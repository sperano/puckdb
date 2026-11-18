package yahoo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
)

type fetchStatus int

const (
	fetchStatusDownloaded fetchStatus = iota
	// fetchStatusMissing is a player whose still-fresh missing marker was found.
	fetchStatusMissing
	fetchStatusCached
	// fetchStatusNotFound is a fresh 404 from Yahoo whose marker has NOT been
	// written yet: the caller decides whether the 404 is genuine (see
	// FetchYahooPlayerBatch) and writes the marker with writeMissingMarker.
	fetchStatusNotFound
)

// missingMarkerFresh reports whether playerID has a missing marker younger
// than ttl at now. A marker at or past ttl is expired and ignored, so the page
// is fetched again; a ttl <= 0 means markers never expire.
func missingMarkerFresh(ctx context.Context, storage store.Storage, missingRes resource.MissingYahooPlayer, ttl time.Duration, now time.Time) (bool, error) {
	info, err := resource.Stat(ctx, storage, missingRes)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat missing marker for player %d: %w", missingRes.PlayerID, err)
	}
	if ttl <= 0 {
		return true, nil
	}
	return now.Sub(info.ModTime()) < ttl, nil
}

// writeMissingMarker records that Yahoo answered 404 for playerID.
func writeMissingMarker(ctx context.Context, storage store.Storage, playerID store.YahooPlayerID) error {
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	if err := resource.WriteParsed(ctx, storage, missingRes, []byte("missing")); err != nil {
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return fmt.Errorf("save missing player: %w", err)
	}
	log.Info().Int("player_id", int(playerID)).Msg("Saved as missing Yahoo player")
	metrics.IncDownload(core.YahooPlayer, metrics.ResultMiss)
	return nil
}

// fetchYahooPlayerImpl is the testable implementation. It never writes a
// missing marker itself: a 404 is reported as fetchStatusNotFound so the batch
// can first rule out throttling.
func fetchYahooPlayerImpl(ctx context.Context, storage store.Storage, downloader shared.Downloader, playerID store.YahooPlayerID, missingTTL time.Duration, now time.Time) (fetchStatus, error) {
	defer metrics.TrackActivityDuration("DownloadYahooPlayer")()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	playerRes := resource.YahooPlayer{PlayerID: playerID}
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	// Check if a fresh missing marker exists (most common case)
	fresh, err := missingMarkerFresh(ctx, storage, missingRes, missingTTL, now)
	if err != nil {
		return 0, err
	}
	if fresh {
		log.Debug().Int("player_id", int(playerID)).Msg("Yahoo player already marked as missing")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusMissing, nil
	}
	// Check if player file already exists
	if resource.Exists(ctx, storage, playerRes) {
		log.Debug().Int("player_id", int(playerID)).Msg("Yahoo player already cached")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusCached, nil
	}
	log.Info().Int("player_id", int(playerID)).Msg("Downloading Yahoo player")
	// Download the player page
	content, err := downloader(ctx, playerRes.URL())
	if err != nil {
		if isYahooNotFound(err) {
			return fetchStatusNotFound, nil
		}
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return 0, fmt.Errorf("download player %d: %w", playerID, err)
	}
	// Save successful download
	if err := resource.WriteParsed(ctx, storage, playerRes, content); err != nil {
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return 0, fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("player_id", int(playerID)).Str("path", playerRes.Path()).Msg("Saved Yahoo player")
	metrics.IncDownload(core.YahooPlayer, metrics.ResultMiss)
	return fetchStatusDownloaded, nil
}

// isYahooNotFound reports whether err is an HTTP 404.
func isYahooNotFound(err error) bool {
	var httpErr *httpx.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == 404
}

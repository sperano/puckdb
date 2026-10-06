package yahoo

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

type fetchStatus int

const (
	fetchStatusDownloaded fetchStatus = iota
	fetchStatusMissing
	fetchStatusCached
)

// HTTPDownloader is the interface for downloading content via HTTP.
type HTTPDownloader interface {
	Download(ctx context.Context, url string) ([]byte, error)
}

// HTTPDownloaderFunc adapts a function to the HTTPDownloader interface.
type HTTPDownloaderFunc func(ctx context.Context, url string) ([]byte, error)

func (f HTTPDownloaderFunc) Download(ctx context.Context, url string) ([]byte, error) {
	return f(ctx, url)
}

// fetchYahooPlayerImpl is the testable implementation.
func fetchYahooPlayerImpl(ctx context.Context, storage store.Storage, downloader HTTPDownloader, playerID store.YahooPlayerID) (fetchStatus, error) {
	defer metrics.TrackActivityDuration("DownloadYahooPlayer")()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	playerRes := resource.YahooPlayer{PlayerID: playerID}
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	// Check if missing player file already exists (most common case)
	if resource.Exists(ctx, storage, missingRes) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already marked as missing")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusMissing, nil
	}
	// Check if player file already exists
	if resource.Exists(ctx, storage, playerRes) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already cached")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusCached, nil
	}
	log.Info().Int("playerID", int(playerID)).Msg("Downloading Yahoo player")
	// Download the player page
	content, err := downloader.Download(ctx, playerRes.URL())
	if err != nil {
		var httpErr *httpx.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == 404 {
				// Save as missing player
				if writeErr := resource.WriteParsed(ctx, storage, missingRes, []byte("missing")); writeErr != nil {
					metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
					return 0, fmt.Errorf("save missing player: %w", writeErr)
				}
				log.Info().Int("playerID", int(playerID)).Msg("Saved as missing Yahoo player")
				metrics.IncDownload(core.YahooPlayer, metrics.ResultMiss)
				SleepAfterYahooDownload()
				return fetchStatusMissing, nil
			}
		}
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return 0, fmt.Errorf("download player %d: %w", playerID, err)
	}
	// Save successful download
	if err := resource.WriteParsed(ctx, storage, playerRes, content); err != nil {
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return 0, fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", int(playerID)).Str("path", playerRes.Path()).Msg("Saved Yahoo player")
	metrics.IncDownload(core.YahooPlayer, metrics.ResultMiss)
	SleepAfterYahooDownload()
	return fetchStatusDownloaded, nil
}

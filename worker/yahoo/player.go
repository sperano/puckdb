package yahoo

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/core"
	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
)

type fetchStatus int

const (
	fetchStatusDownloaded fetchStatus = iota
	fetchStatusMissing
	fetchStatusCached
)

// HTTPDownloader is the interface for downloading content via HTTP.
type HTTPDownloader interface {
	Download(url string) ([]byte, error)
}

// HTTPDownloaderFunc adapts a function to the HTTPDownloader interface.
type HTTPDownloaderFunc func(url string) ([]byte, error)

func (f HTTPDownloaderFunc) Download(url string) ([]byte, error) {
	return f(url)
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
	if storage.Exists(missingRes.Path()) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already marked as missing")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusMissing, nil
	}
	// Check if player file already exists
	if storage.Exists(playerRes.Path()) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already cached")
		metrics.IncDownload(core.YahooPlayer, metrics.ResultHit)
		return fetchStatusCached, nil
	}
	log.Info().Int("playerID", int(playerID)).Msg("Downloading Yahoo player")
	// Download the player page
	content, err := downloader.Download(playerRes.URL())
	if err != nil {
		var httpErr *puckhttp.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == 404 {
				// Save as missing player
				if writeErr := resource.WriteParsed(storage, missingRes, []byte("missing")); writeErr != nil {
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
	if err := resource.WriteParsed(storage, playerRes, content); err != nil {
		metrics.IncDownload(core.YahooPlayer, metrics.ResultError)
		return 0, fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", int(playerID)).Str("path", playerRes.Path()).Msg("Saved Yahoo player")
	metrics.IncDownload(core.YahooPlayer, metrics.ResultMiss)
	SleepAfterYahooDownload()
	return fetchStatusDownloaded, nil
}

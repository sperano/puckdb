package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/urls"
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

// httpDownloaderFunc adapts a function to the HTTPDownloader interface.
type httpDownloaderFunc func(url string) ([]byte, error)

func (f httpDownloaderFunc) Download(url string) ([]byte, error) {
	return f(url)
}

// FetchYahooPlayerBatchResult contains counts from a batch fetch operation.
type FetchYahooPlayerBatchResult struct {
	Downloaded int // Network downloads (player pages fetched from Yahoo)
	Missing    int // 404 responses (player doesn't exist)
	Cached     int // Cache hits (already in local cache)
}

// FetchYahooPlayerBatchActivity fetches a range of Yahoo player pages (from cache or network).
// Processes players from startID to endID (inclusive).
func FetchYahooPlayerBatchActivity(ctx context.Context, startID, endID store.YahooPlayerID) (FetchYahooPlayerBatchResult, error) {
	var result FetchYahooPlayerBatchResult
	fs := store.NewStore()
	downloader := httpDownloaderFunc(puckhttp.DownloadPublic)
	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)
		if err != nil {
			return result, err
		}
		switch status {
		case fetchStatusDownloaded:
			result.Downloaded++
		case fetchStatusMissing:
			result.Missing++
		case fetchStatusCached:
			result.Cached++
		}
	}
	return result, nil
}

// fetchYahooPlayerImpl is the testable implementation.
func fetchYahooPlayerImpl(ctx context.Context, fs store.Store, downloader HTTPDownloader, playerID store.YahooPlayerID) (fetchStatus, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadYahooPlayer", time.Since(start))
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Check if missing player file already exists (most common case)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	if fs.Exists(missingFile) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already marked as missing")
		metrics.IncDownload("YahooPlayer", "hit")
		return fetchStatusMissing, nil
	}

	// Check if player file already exists
	playerFile := store.YahooPlayerFile{PlayerID: playerID}
	if fs.Exists(playerFile) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already cached")
		metrics.IncDownload("YahooPlayer", "hit")
		return fetchStatusCached, nil
	}

	log.Info().Int("playerID", int(playerID)).Msg("Downloading Yahoo player")

	// Ensure directories exist
	if err := fs.MkdirAll(playerFile.Dir(), 0755); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return 0, fmt.Errorf("mkdir player dir: %w", err)
	}
	if err := fs.MkdirAll(missingFile.Dir(), 0755); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return 0, fmt.Errorf("mkdir missing dir: %w", err)
	}

	// Download the player page
	url := urls.YahooPlayerURL(int(playerID))
	content, err := downloader.Download(url)
	if err != nil {
		var httpErr *puckhttp.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == 404 {
				// Save as missing player
				if writeErr := fs.Write(missingFile, []byte("404 Not Found")); writeErr != nil {
					metrics.IncDownload("YahooPlayer", "error")
					return 0, fmt.Errorf("save missing player: %w", writeErr)
				}
				log.Info().Int("playerID", int(playerID)).Msg("Saved as missing Yahoo player")
				metrics.IncDownload("YahooPlayer", "miss")
				sleepAfterYahooDownload()
				return fetchStatusMissing, nil
			}
		}
		metrics.IncDownload("YahooPlayer", "error")
		return 0, fmt.Errorf("download player %d: %w", playerID, err)
	}

	// Save successful download
	if err := fs.Write(playerFile, content); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return 0, fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", int(playerID)).Str("path", store.Path(playerFile)).Msg("Saved Yahoo player")
	metrics.IncDownload("YahooPlayer", "miss")
	sleepAfterYahooDownload()
	return fetchStatusDownloaded, nil
}

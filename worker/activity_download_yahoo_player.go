package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
)

type downloadStatus int

const (
	downloadStatusDownloaded downloadStatus = iota
	downloadStatusMissing
	downloadStatusCached
)

// DownloadYahooPlayerBatchResult contains counts from a batch download.
type DownloadYahooPlayerBatchResult struct {
	Downloaded int // Successfully downloaded player pages
	Missing    int // 404 responses (player doesn't exist)
	Cached     int // Already in cache (hit)
}

// DownloadYahooPlayerBatch downloads a range of Yahoo player pages.
// Processes players from startID to endID (inclusive).
func DownloadYahooPlayerBatch(ctx context.Context, startID, endID int) (DownloadYahooPlayerBatchResult, error) {
	var result DownloadYahooPlayerBatchResult
	fs := cache.NewSimpleCache()
	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		status, err := downloadYahooPlayerImpl(ctx, fs, playerID)
		if err != nil {
			return result, err
		}
		switch status {
		case downloadStatusDownloaded:
			result.Downloaded++
		case downloadStatusMissing:
			result.Missing++
		case downloadStatusCached:
			result.Cached++
		}
	}
	return result, nil
}

// downloadYahooPlayerImpl is the testable implementation.
func downloadYahooPlayerImpl(ctx context.Context, fs cache.FileSystem, playerID int) (downloadStatus, error) {
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
	missingFile := cache.MissingYahooPlayerFile{PlayerID: playerID}
	if fs.Exists(missingFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already marked as missing")
		metrics.IncDownload("YahooPlayer", "hit")
		return downloadStatusMissing, nil
	}

	// Check if player file already exists
	playerFile := cache.YahooPlayerFile{PlayerID: playerID}
	if fs.Exists(playerFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already cached")
		metrics.IncDownload("YahooPlayer", "hit")
		return downloadStatusCached, nil
	}

	log.Info().Int("playerID", playerID).Msg("Downloading Yahoo player")

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
	url := http.YahooPlayerURL(playerID)
	content, err := http.DownloadPublic(url)
	if err != nil {
		var httpErr *http.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == 404 {
				// Save as missing player
				if writeErr := fs.Write(missingFile, []byte("404 Not Found")); writeErr != nil {
					metrics.IncDownload("YahooPlayer", "error")
					return 0, fmt.Errorf("save missing player: %w", writeErr)
				}
				log.Info().Int("playerID", playerID).Msg("Saved as missing Yahoo player")
				metrics.IncDownload("YahooPlayer", "miss")
				sleepAfterYahooDownload()
				return downloadStatusMissing, nil
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
	log.Info().Int("playerID", playerID).Str("path", cache.Path(playerFile)).Msg("Saved Yahoo player")
	metrics.IncDownload("YahooPlayer", "miss")
	sleepAfterYahooDownload()
	return downloadStatusDownloaded, nil
}

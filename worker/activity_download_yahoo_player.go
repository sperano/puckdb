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

// DownloadYahooPlayer downloads a Yahoo player page and saves it to cache.
// If the player already exists (either as YahooPlayer or MissingYahooPlayer), it returns early.
// On 200: saves as YahooPlayerFile
// On 404: saves as MissingYahooPlayerFile
// Other status codes: returns error
func DownloadYahooPlayer(ctx context.Context, playerID int) error {
	fs := cache.NewSimpleCache()
	return downloadYahooPlayerImpl(ctx, fs, playerID)
}

// DownloadYahooPlayerBatch downloads a range of Yahoo player pages.
// Processes players from startID to endID (inclusive).
func DownloadYahooPlayerBatch(ctx context.Context, startID, endID int) error {
	fs := cache.NewSimpleCache()
	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := downloadYahooPlayerImpl(ctx, fs, playerID); err != nil {
			return err
		}
	}
	return nil
}

// downloadYahooPlayerImpl is the testable implementation.
func downloadYahooPlayerImpl(ctx context.Context, fs cache.FileSystem, playerID int) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadYahooPlayer", time.Since(start))
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if missing player file already exists (most common case)
	missingFile := fs.New(cache.MissingYahooPlayerFileType, playerID)
	if fs.Exists(missingFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already marked as missing")
		metrics.IncDownload("YahooPlayer", "hit")
		return nil
	}

	// Check if player file already exists
	playerFile := fs.New(cache.YahooPlayerFileType, playerID)
	if fs.Exists(playerFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already cached")
		metrics.IncDownload("YahooPlayer", "hit")
		return nil
	}

	log.Info().Int("playerID", playerID).Msg("Downloading Yahoo player")

	// Ensure directories exist
	if err := fs.MkdirAll(playerFile.Dir(), 0755); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return fmt.Errorf("mkdir player dir: %w", err)
	}
	if err := fs.MkdirAll(missingFile.Dir(), 0755); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return fmt.Errorf("mkdir missing dir: %w", err)
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
					return fmt.Errorf("save missing player: %w", writeErr)
				}
				log.Info().Int("playerID", playerID).Msg("Saved as missing Yahoo player")
				metrics.IncDownload("YahooPlayer", "miss")
				sleepAfterYahooDownload()
				return nil
			}
		}
		metrics.IncDownload("YahooPlayer", "error")
		return fmt.Errorf("download player %d: %w", playerID, err)
	}

	// Save successful download
	if err := fs.Write(playerFile, content); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", playerID).Str("path", cache.Path(playerFile)).Msg("Saved Yahoo player")
	metrics.IncDownload("YahooPlayer", "miss")
	sleepAfterYahooDownload()
	return nil
}

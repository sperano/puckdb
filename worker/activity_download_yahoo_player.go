package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/http"
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

// downloadYahooPlayerImpl is the testable implementation.
func downloadYahooPlayerImpl(ctx context.Context, fs cache.FileSystem, playerID int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if player file already exists
	playerFile := fs.New(cache.YahooPlayerFileType, playerID)
	if fs.Exists(playerFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already cached")
		return nil
	}

	// Check if missing player file already exists
	missingFile := fs.New(cache.MissingYahooPlayerFileType, playerID)
	if fs.Exists(missingFile) {
		log.Debug().Int("playerID", playerID).Msg("Yahoo player already marked as missing")
		return nil
	}

	log.Info().Int("playerID", playerID).Msg("Downloading Yahoo player")

	// Ensure directories exist
	if err := fs.MkdirAll(playerFile.Dir(), 0755); err != nil {
		return fmt.Errorf("mkdir player dir: %w", err)
	}
	if err := fs.MkdirAll(missingFile.Dir(), 0755); err != nil {
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
					return fmt.Errorf("save missing player: %w", writeErr)
				}
				log.Info().Int("playerID", playerID).Msg("Saved as missing Yahoo player")
				return nil
			}
		}
		return fmt.Errorf("download player %d: %w", playerID, err)
	}

	// Save successful download
	if err := fs.Write(playerFile, content); err != nil {
		return fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", playerID).Str("path", cache.Path(playerFile)).Msg("Saved Yahoo player")
	return nil
}

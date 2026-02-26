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
	repos := store.NewDefaultRepos()
	downloader := httpDownloaderFunc(puckhttp.DownloadPublic)
	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		status, err := fetchYahooPlayerImpl(ctx, repos, downloader, playerID)
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
func fetchYahooPlayerImpl(ctx context.Context, repos *store.Repos, downloader HTTPDownloader, playerID store.YahooPlayerID) (fetchStatus, error) {
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
	if repos.Yahoo.IsPlayerMissing(playerID) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already marked as missing")
		metrics.IncDownload("YahooPlayer", "hit")
		return fetchStatusMissing, nil
	}

	// Check if player file already exists
	if repos.Yahoo.PlayerExists(playerID) {
		log.Debug().Int("playerID", int(playerID)).Msg("Yahoo player already cached")
		metrics.IncDownload("YahooPlayer", "hit")
		return fetchStatusCached, nil
	}

	log.Info().Int("playerID", int(playerID)).Msg("Downloading Yahoo player")

	// Download the player page
	url := urls.YahooPlayerURL(int(playerID))
	content, err := downloader.Download(url)
	if err != nil {
		var httpErr *puckhttp.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == 404 {
				// Save as missing player
				if writeErr := repos.Yahoo.MarkPlayerMissing(playerID); writeErr != nil {
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
	if err := repos.Yahoo.SavePlayer(playerID, content); err != nil {
		metrics.IncDownload("YahooPlayer", "error")
		return 0, fmt.Errorf("save player: %w", err)
	}
	log.Info().Int("playerID", int(playerID)).Str("path", store.YahooPlayerPath(playerID)).Msg("Saved Yahoo player")
	metrics.IncDownload("YahooPlayer", "miss")
	sleepAfterYahooDownload()
	return fetchStatusDownloaded, nil
}

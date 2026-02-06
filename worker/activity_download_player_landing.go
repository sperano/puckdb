package worker

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
)

// DownloadPlayerLandingBatchResult contains statistics from a batch download.
type DownloadPlayerLandingBatchResult struct {
	Downloaded int // Players downloaded from API
	CacheHits  int // Players found in cache
	Errors     int // Players that failed to download
}

// DownloadPlayerLandingBatchActivity downloads player landing pages for a batch of player IDs.
// Skips already-cached players (idempotent). Individual failures are logged and skipped.
func DownloadPlayerLandingBatchActivity(ctx context.Context, playerIDs []int64) (DownloadPlayerLandingBatchResult, error) {
	fs := cache.NewSimpleCache()
	client := newNHLClient()
	return downloadPlayerLandingBatchImpl(ctx, fs, client, playerIDs)
}

func downloadPlayerLandingBatchImpl(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
	playerIDs []int64,
) (DownloadPlayerLandingBatchResult, error) {
	result := DownloadPlayerLandingBatchResult{}

	for _, id := range playerIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		playerID := nhl.PlayerID(id)
		_, fromCache, err := getPlayerLandingWithCache(ctx, fs, client, playerID)
		if err != nil {
			log.Warn().Err(err).Int64("player_id", id).Msg("Failed to download player landing")
			result.Errors++
			metrics.IncDownload("PlayerLanding", "error")
			continue
		}

		if fromCache {
			result.CacheHits++
			metrics.IncDownload("PlayerLanding", "hit")
		} else {
			result.Downloaded++
			metrics.IncDownload("PlayerLanding", "miss")
		}
	}

	log.Debug().
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.CacheHits).
		Int("errors", result.Errors).
		Int("batch_size", len(playerIDs)).
		Msg("Player landing batch complete")

	return result, nil
}

// getPlayerLandingWithCache attempts to get player landing data from cache first,
// falling back to the NHL API if not cached. Returns the data and whether it came from cache.
func getPlayerLandingWithCache(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
	playerID nhl.PlayerID,
) (*nhl.PlayerLanding, bool, error) {
	file := cache.PlayerLandingFile{PlayerID: playerID}

	// Check cache first
	if fs.Exists(file) {
		data, err := fs.Read(file)
		if err == nil {
			var landing nhl.PlayerLanding
			if err := json.Unmarshal(data, &landing); err == nil {
				return &landing, true, nil
			}
			log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to unmarshal cached player landing")
		}
	}

	// Fetch from API
	landing, err := client.PlayerLanding(ctx, playerID)
	if err != nil {
		return nil, false, err
	}

	// Save to cache
	data, err := json.Marshal(landing)
	if err != nil {
		log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to marshal player landing for cache")
	} else {
		if err := fs.Write(file, data); err != nil {
			log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to write player landing to cache")
		}
	}

	return landing, false, nil
}

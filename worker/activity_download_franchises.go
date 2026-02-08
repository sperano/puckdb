package worker

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
)

// DownloadFranchisesResult contains statistics from the franchises download.
type DownloadFranchisesResult struct {
	Count     int  // Number of franchises downloaded
	FromCache bool // Whether data came from cache
}

// DownloadFranchisesActivity downloads all NHL franchises from the API.
// Uses SimpleFS cache; skips download if already cached.
func DownloadFranchisesActivity(ctx context.Context) (DownloadFranchisesResult, error) {
	fs := cache.NewSimpleCache()
	client := newNHLClient()
	return downloadFranchisesImpl(ctx, fs, client)
}

func downloadFranchisesImpl(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
) (DownloadFranchisesResult, error) {
	file := cache.FranchisesFile{}

	// Check cache first
	if fs.Exists(file) {
		data, err := fs.Read(file)
		if err == nil {
			var franchises []nhl.Franchise
			if err := json.Unmarshal(data, &franchises); err == nil {
				log.Debug().Int("count", len(franchises)).Msg("Franchises loaded from cache")
				metrics.IncDownload(cache.FileTypeFranchises, "hit")
				return DownloadFranchisesResult{Count: len(franchises), FromCache: true}, nil
			}
			log.Debug().Err(err).Msg("Failed to unmarshal cached franchises")
		}
	}

	// Fetch from API
	franchises, err := client.Franchises(ctx)
	if err != nil {
		metrics.IncDownload(cache.FileTypeFranchises, "error")
		return DownloadFranchisesResult{}, err
	}

	// Save to cache
	data, err := json.Marshal(franchises)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to marshal franchises for cache")
	} else {
		if err := fs.Write(file, data); err != nil {
			log.Warn().Err(err).Msg("Failed to write franchises to cache")
		}
	}

	log.Info().Int("count", len(franchises)).Msg("Franchises downloaded from API")
	metrics.IncDownload(cache.FileTypeFranchises, "miss")

	return DownloadFranchisesResult{Count: len(franchises), FromCache: false}, nil
}

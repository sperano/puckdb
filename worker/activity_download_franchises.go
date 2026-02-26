package worker

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// DownloadFranchisesResult contains statistics from the franchises download.
type DownloadFranchisesResult struct {
	Count     int  // Number of franchises downloaded
	FromCache bool // Whether data came from cache
}

// DownloadFranchisesActivity downloads all NHL franchises from the API.
// Uses SimpleFS cache; skips download if already cached.
func DownloadFranchisesActivity(ctx context.Context) (DownloadFranchisesResult, error) {
	repos := store.NewDefaultRepos()
	client := newNHLClient()
	return downloadFranchisesImpl(ctx, repos, client)
}

func downloadFranchisesImpl(
	ctx context.Context,
	repos *store.Repos,
	client NHLClient,
) (DownloadFranchisesResult, error) {
	// Check cache first
	if repos.Franchise.Exists() {
		franchises, err := repos.Franchise.Get()
		if err == nil {
			log.Debug().Int("count", len(franchises)).Msg("Franchises loaded from cache")
			metrics.IncDownload(store.FileTypeFranchises, "hit")
			return DownloadFranchisesResult{Count: len(franchises), FromCache: true}, nil
		}
		log.Debug().Err(err).Msg("Failed to read cached franchises")
	}

	// Fetch from API
	franchises, err := client.Franchises(ctx)
	if err != nil {
		metrics.IncDownload(store.FileTypeFranchises, "error")
		return DownloadFranchisesResult{}, err
	}

	// Save to cache (json.Marshal won't fail for []nhl.Franchise)
	data, _ := json.Marshal(franchises)
	if err := repos.Franchise.Save(data); err != nil {
		log.Warn().Err(err).Msg("Failed to write franchises to cache")
	}

	log.Info().Int("count", len(franchises)).Msg("Franchises downloaded from API")
	metrics.IncDownload(store.FileTypeFranchises, "miss")

	return DownloadFranchisesResult{Count: len(franchises), FromCache: false}, nil
}

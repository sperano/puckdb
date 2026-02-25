package worker

import (
	"context"

	"github.com/sperano/puckdb/store"
)

// FetchPlayerLandingsBatchResult contains statistics from a batch fetch.
type FetchPlayerLandingsBatchResult struct {
	Downloaded int // Players downloaded from API
	CacheHits  int // Players found in cache
	Missing    int // 404 responses (cached for future runs)
}

// FetchPlayerLandingsBatchActivity fetches player landing pages for a batch of players.
// Reuses the existing downloadPlayerLandingBatchImpl logic.
func FetchPlayerLandingsBatchActivity(ctx context.Context, players []store.BoxscorePlayer) (FetchPlayerLandingsBatchResult, error) {
	fs := store.NewStore()
	nhlClient := newNHLClient()

	// Reuse existing download implementation
	result, err := downloadPlayerLandingBatchImpl(ctx, fs, nhlClient, players)
	if err != nil {
		return FetchPlayerLandingsBatchResult{}, err
	}

	return FetchPlayerLandingsBatchResult{
		Downloaded: result.Downloaded,
		CacheHits:  result.CacheHits,
		Missing:    result.Missing,
	}, nil
}

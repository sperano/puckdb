package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// DownloadPlayerLandingBatchResult contains statistics from a batch download.
type DownloadPlayerLandingBatchResult struct {
	Downloaded int // Players downloaded from API
	CacheHits  int // Players found in cache
	Missing    int // 404 responses (cached for future runs)
}

func downloadPlayerLandingBatchImpl(
	ctx context.Context,
	repos *store.Repos,
	client NHLClient,
	players []store.BoxscorePlayer,
) (DownloadPlayerLandingBatchResult, error) {
	result := DownloadPlayerLandingBatchResult{}

	for _, p := range players {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		playerID := nhl.PlayerID(p.ID)
		status, err := ensurePlayerLandingCached(ctx, repos, client, playerID, p)
		if err != nil {
			log.Error().Err(err).Int64("player_id", p.ID).Msg("Failed to download player landing")
			return result, err
		}

		switch status {
		case playerLandingCached:
			result.CacheHits++
			metrics.IncDownload("PlayerLanding", "hit")
		case playerLandingDownloaded:
			result.Downloaded++
			metrics.IncDownload("PlayerLanding", "miss")
		case playerLandingMissing:
			result.Missing++
			metrics.IncDownload("PlayerLanding", "missing")
		}
	}

	log.Debug().
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.CacheHits).
		Int("missing", result.Missing).
		Int("batch_size", len(players)).
		Msg("Player landing batch complete")

	return result, nil
}

type playerLandingStatus int

const (
	playerLandingDownloaded playerLandingStatus = iota
	playerLandingCached
	playerLandingMissing
)

// ensurePlayerLandingCached downloads player landing data if not already cached.
// Returns a status indicating whether data was downloaded, already cached, or missing (404).
func ensurePlayerLandingCached(
	ctx context.Context,
	repos *store.Repos,
	client NHLClient,
	playerID nhl.PlayerID,
	boxscorePlayer store.BoxscorePlayer,
) (playerLandingStatus, error) {
	// Check if already marked as missing (most common case for 404s)
	if repos.Player.IsMissing(playerID) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already marked as missing")
		return playerLandingMissing, nil
	}

	// Check if landing page is already cached
	if repos.Player.LandingExists(playerID) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already cached")
		return playerLandingCached, nil
	}

	// Fetch from API
	landing, err := client.PlayerLanding(ctx, playerID)
	if err != nil {
		// Check if this is a 404 error
		var notFoundErr *nhl.ResourceNotFoundError
		if errors.As(err, &notFoundErr) {
			// Cache the 404 with boxscore player data
			missingInfo := store.MissingPlayerLandingData{
				FirstName: boxscorePlayer.FirstName,
				LastName:  boxscorePlayer.LastName,
				Position:  boxscorePlayer.Position,
			}
			if saveErr := repos.Player.MarkMissing(playerID, missingInfo); saveErr != nil {
				log.Warn().Err(saveErr).Str("player_id", playerID.String()).Msg("Failed to save missing player landing")
			} else {
				log.Info().Str("player_id", playerID.String()).Msg("Saved player as missing (404)")
			}
			return playerLandingMissing, nil
		}
		return 0, err
	}

	// Save successful response to cache (json.Marshal won't fail for *nhl.PlayerLanding)
	data, _ := json.Marshal(landing)
	if err := repos.Player.SaveLanding(playerID, data); err != nil {
		return 0, fmt.Errorf("write player %s landing to cache: %w", playerID.String(), err)
	}

	return playerLandingDownloaded, nil
}

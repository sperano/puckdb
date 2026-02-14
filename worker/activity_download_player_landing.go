package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/metrics"
)

// DownloadPlayerLandingBatchResult contains statistics from a batch download.
type DownloadPlayerLandingBatchResult struct {
	Downloaded int // Players downloaded from API
	CacheHits  int // Players found in cache
	Missing    int // 404 responses (cached for future runs)
}

// DownloadPlayerLandingBatchActivity downloads player landing pages for a batch of players.
// Skips already-cached players (idempotent). Caches 404s to avoid repeat failures.
func DownloadPlayerLandingBatchActivity(ctx context.Context, players []BoxscorePlayer) (DownloadPlayerLandingBatchResult, error) {
	fs := store.NewStore()
	client := newNHLClient()
	return downloadPlayerLandingBatchImpl(ctx, fs, client, players)
}

func downloadPlayerLandingBatchImpl(
	ctx context.Context,
	fs store.Store,
	client NHLClient,
	players []BoxscorePlayer,
) (DownloadPlayerLandingBatchResult, error) {
	result := DownloadPlayerLandingBatchResult{}

	for _, p := range players {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		playerID := nhl.PlayerID(p.ID)
		status, err := ensurePlayerLandingCached(ctx, fs, client, playerID, &p)
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
	fs store.Store,
	client NHLClient,
	playerID nhl.PlayerID,
	boxscorePlayer *BoxscorePlayer,
) (playerLandingStatus, error) {
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// Check if already marked as missing (most common case for 404s)
	if fs.Exists(missingFile) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already marked as missing")
		return playerLandingMissing, nil
	}

	// Check if landing page is already cached
	if fs.Exists(landingFile) {
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
			if saveErr := saveMissingPlayerLanding(fs, missingFile, boxscorePlayer); saveErr != nil {
				log.Warn().Err(saveErr).Str("player_id", playerID.String()).Msg("Failed to save missing player landing")
			} else {
				log.Info().Str("player_id", playerID.String()).Msg("Saved player as missing (404)")
			}
			return playerLandingMissing, nil
		}
		return 0, err
	}

	// Save successful response to cache
	data, err := json.Marshal(landing)
	if err != nil {
		return 0, fmt.Errorf("marshal player %s landing: %w", playerID.String(), err)
	}

	if err := fs.Write(landingFile, data); err != nil {
		return 0, fmt.Errorf("write player %s landing to cache: %w", playerID.String(), err)
	}

	return playerLandingDownloaded, nil
}

// saveMissingPlayerLanding saves boxscore player data to a missing player landing file.
func saveMissingPlayerLanding(fs store.Store, file store.MissingPlayerLandingFile, player *BoxscorePlayer) error {
	data := store.MissingPlayerLandingData{
		FirstName: player.FirstName,
		LastName:  player.LastName,
		Position:  player.Position,
	}

	content, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return fs.Write(file, content)
}

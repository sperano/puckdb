package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
)

// UnmatchedYahooPlayer represents a Yahoo player that wasn't matched to any NHL player.
type UnmatchedYahooPlayer struct {
	YahooID      int
	FirstName    string
	LastName     string
	Team         string
	JerseyNumber int
}

// ReportUnmatchedYahooIDsActivity reports all Yahoo IDs that weren't matched and cleans up Redis.
// This is Phase 3 of the import workflow.
func ReportUnmatchedYahooIDsActivity(ctx context.Context) ([]UnmatchedYahooPlayer, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return reportUnmatchedYahooIDsImpl(ctx, redisClient)
}

func reportUnmatchedYahooIDsImpl(ctx context.Context, redisClient redis.Client) ([]UnmatchedYahooPlayer, error) {
	// Get unmatched IDs
	unmatchedIDs, err := GetUnmatchedYahooIDs(ctx, redisClient)
	if err != nil {
		return nil, err
	}

	// Load full details for each unmatched ID
	var unmatched []UnmatchedYahooPlayer
	for _, id := range unmatchedIDs {
		player, err := GetYahooPlayerByID(ctx, redisClient, id)
		if err != nil {
			log.Warn().Err(err).Int("yahooID", id).Msg("Failed to load unmatched Yahoo player details")
			// Still include basic info
			unmatched = append(unmatched, UnmatchedYahooPlayer{YahooID: id})
			continue
		}

		unmatched = append(unmatched, UnmatchedYahooPlayer{
			YahooID:      player.YahooID,
			FirstName:    player.FirstName,
			LastName:     player.LastName,
			Team:         player.Team,
			JerseyNumber: player.JerseyNumber,
		})
	}

	log.Info().
		Int("unmatched", len(unmatched)).
		Msg("Found unmatched Yahoo players")

	// Log sample of unmatched players for debugging
	for i, p := range unmatched {
		if i >= 10 {
			log.Debug().Int("remaining", len(unmatched)-10).Msg("... and more unmatched players")
			break
		}
		log.Debug().
			Int("yahooID", p.YahooID).
			Str("name", p.FirstName+" "+p.LastName).
			Str("team", p.Team).
			Int("jersey", p.JerseyNumber).
			Msg("Unmatched Yahoo player")
	}

	// Cleanup Redis keys
	if err := CleanupYahooIDPool(ctx, redisClient); err != nil {
		log.Warn().Err(err).Msg("Failed to cleanup Yahoo ID pool")
	}

	return unmatched, nil
}

// ListPlayerLandingIDsActivity returns all player IDs from cached PlayerLanding files.
// Used to enumerate players for import.
func ListPlayerLandingIDsActivity(ctx context.Context) ([]int64, error) {
	fs := cache.NewSimpleCache().(*cache.InstrumentedFS).Inner()
	return listPlayerLandingIDsImpl(fs)
}

func listPlayerLandingIDsImpl(fs *cache.SimpleFS) ([]int64, error) {
	type playerID struct {
		ID int64
	}

	results, err := cache.ListAll(fs, cache.PlayerLandingFileType, func(file cache.File, _ []byte) (playerID, error) {
		pf := file.(cache.PlayerLandingFile)
		return playerID{ID: pf.PlayerID.AsInt64()}, nil
	})
	if err != nil {
		return nil, err
	}

	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}

	log.Info().
		Int("players", len(ids)).
		Msg("Listed PlayerLanding files")

	return ids, nil
}

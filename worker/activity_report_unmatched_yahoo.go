package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// UnmatchedYahooPlayer represents a Yahoo player that wasn't matched to any NHL player.
type UnmatchedYahooPlayer struct {
	YahooID      store.YahooPlayerID
	FirstName    string
	LastName     string
	Team         string
	JerseyNumber int
}

// UnmatchedReport contains the results of the unmatched player verification.
type UnmatchedReport struct {
	// TrulyUnmatched are players with NHL games who weren't matched (need investigation).
	TrulyUnmatched []VerifiedPlayer

	// VerifiedNonNHLCount is the number of players confirmed to have 0 NHL games.
	VerifiedNonNHLCount int

	// NotFoundCount is the number of players not found in the NHL database.
	NotFoundCount int
}

// LoadUnmatchedYahooPlayersActivity loads the list of unmatched Yahoo players from Redis.
// This is the first step of Phase 3, allowing the workflow to know the total count for progress tracking.
func LoadUnmatchedYahooPlayersActivity(ctx context.Context) ([]UnmatchedYahooPlayer, error) {
	logger := activity.GetLogger(ctx)
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

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
			log.Warn().Err(err).Int("yahooID", int(id)).Msg("Failed to load unmatched Yahoo player details")
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

	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatched))
	return unmatched, nil
}

// CleanupYahooIDPoolActivity cleans up the Yahoo ID pool from Redis.
// Called at the end of Phase 3 after verification is complete.
func CleanupYahooIDPoolActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	if err := CleanupYahooIDPool(ctx, redisClient); err != nil {
		return err
	}

	logger.Info("Cleaned up Yahoo ID pool from Redis")
	return nil
}

// ListPlayerLandingIDsActivity returns all player IDs from cached PlayerLanding files.
// Used to enumerate players for import.
func ListPlayerLandingIDsActivity(ctx context.Context) ([]int64, error) {
	logger := activity.GetLogger(ctx)
	fs := store.NewStore()

	files, err := fs.ListFiles(store.PlayerLandingFile{}, store.ParsePlayerLandingFilename)
	if err != nil {
		return nil, err
	}

	ids := make([]int64, len(files))
	for i, f := range files {
		ids[i] = f.(store.PlayerLandingFile).PlayerID.AsInt64()
	}

	logger.Info("Listed PlayerLanding files", "count", len(ids))
	return ids, nil
}

package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
	"go.temporal.io/sdk/activity"
)

// UnmatchedYahooPlayer represents a Yahoo player that wasn't matched to any NHL player.
type UnmatchedYahooPlayer struct {
	YahooID      int
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

// ReportUnmatchedYahooIDsActivity reports Yahoo IDs that weren't matched, verifies them
// against the NHL API, and only returns truly unmatched players (those with NHL games).
// Players with 0 NHL games are added to the verified non-NHL set for future exclusion.
// This is Phase 3 of the import workflow.
func ReportUnmatchedYahooIDsActivity(ctx context.Context) (*UnmatchedReport, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return reportUnmatchedYahooIDsImpl(ctx, redisClient)
}

func reportUnmatchedYahooIDsImpl(ctx context.Context, redisClient redis.Client) (*UnmatchedReport, error) {
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
		Int("unmatched_before_verification", len(unmatched)).
		Msg("Found unmatched Yahoo players, starting verification")

	// Verify unmatched players against NHL API
	verifyResult, err := VerifyUnmatchedYahooIDsActivity(ctx, unmatched)
	if err != nil {
		log.Error().Err(err).Msg("Failed to verify unmatched players")
		// On error, return empty report but don't fail the workflow
		return &UnmatchedReport{}, nil
	}

	// Log truly unmatched players (those with NHL games)
	for _, p := range verifyResult.TrulyUnmatched {
		log.Warn().
			Int("yahooID", p.YahooID).
			Str("yahoo_name", p.FirstName+" "+p.LastName).
			Str("nhl_name", p.NHLName).
			Int("nhl_games", p.NHLGames).
			Msg("Truly unmatched player with NHL games - needs investigation")
	}

	// Cleanup Redis keys
	if err := CleanupYahooIDPool(ctx, redisClient); err != nil {
		log.Warn().Err(err).Msg("Failed to cleanup Yahoo ID pool")
	}

	return &UnmatchedReport{
		TrulyUnmatched:      verifyResult.TrulyUnmatched,
		VerifiedNonNHLCount: len(verifyResult.VerifiedNonNHL),
		NotFoundCount:       len(verifyResult.NotFoundInNHL),
	}, nil
}

// ListPlayerLandingIDsActivity returns all player IDs from cached PlayerLanding files.
// Used to enumerate players for import.
func ListPlayerLandingIDsActivity(ctx context.Context) ([]int64, error) {
	logger := activity.GetLogger(ctx)
	fs := cache.NewSimpleCache().(*cache.InstrumentedFS).Inner()

	files, err := cache.ListAll(fs, cache.PlayerLandingFile{}, cache.ParsePlayerLandingFilename)
	if err != nil {
		return nil, err
	}

	ids := make([]int64, len(files))
	for i, f := range files {
		ids[i] = f.(cache.PlayerLandingFile).PlayerID.AsInt64()
	}

	logger.Info("Listed PlayerLanding files", "count", len(ids))
	return ids, nil
}

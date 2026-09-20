package player

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/store"
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

	// UnverifiedCount is the number of players whose verification failed
	// (NHL API error). They are not cached and will be re-verified next run.
	UnverifiedCount int
}

// LoadUnmatchedYahooPlayers loads the list of unmatched Yahoo players from Redis.
// This is the first step of Phase 3, allowing the workflow to know the total count for progress tracking.
func (a *Activities) LoadUnmatchedYahooPlayers(ctx context.Context) ([]UnmatchedYahooPlayer, error) {
	logger := activity.GetLogger(ctx)

	unmatched, err := a.loadUnmatchedYahooPlayersImpl(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatched))
	return unmatched, nil
}

// loadUnmatchedYahooPlayersImpl contains the testable logic for LoadUnmatchedYahooPlayers.
func (a *Activities) loadUnmatchedYahooPlayersImpl(ctx context.Context) ([]UnmatchedYahooPlayer, error) {
	unmatchedIDs, err := GetUnmatchedYahooIDs(ctx, a.RedisClient)
	if err != nil {
		return nil, err
	}

	var unmatched []UnmatchedYahooPlayer
	for _, id := range unmatchedIDs {
		player, err := GetYahooPlayerByID(ctx, a.RedisClient, id)
		if err != nil {
			log.Warn().Err(err).Int("yahooID", int(id)).Msg("Failed to load unmatched Yahoo player details")
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

	return unmatched, nil
}

// CleanupYahooIDPoolData cleans up the Yahoo ID pool from Redis.
// Called at the end of Phase 3 after verification is complete.
func (a *Activities) CleanupYahooIDPoolData(ctx context.Context) error {
	logger := activity.GetLogger(ctx)

	if err := CleanupYahooIDPool(ctx, a.RedisClient); err != nil {
		return err
	}

	logger.Info("Cleaned up Yahoo ID pool from Redis")
	return nil
}

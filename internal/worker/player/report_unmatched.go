package player

import (
	"context"
	"slices"

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

// MaxReportedTrulyUnmatched bounds UnmatchedReport.TrulyUnmatched, and so the
// workflow result: a run over a pool much wider than its seasons once listed
// thousands of players.
const MaxReportedTrulyUnmatched = 50

// UnmatchedReport contains the results of the unmatched player verification.
type UnmatchedReport struct {
	// TrulyUnmatched are players with NHL games who weren't matched (need
	// investigation): the first MaxReportedTrulyUnmatched of them, all of
	// them being logged.
	TrulyUnmatched []VerifiedPlayer

	// TrulyUnmatchedCount is the number of truly unmatched players, the ones
	// TrulyUnmatched leaves out included.
	TrulyUnmatchedCount int

	// MatchedNamesakeCount is the number of players whose every NHL namesake
	// is already matched to another Yahoo ID.
	MatchedNamesakeCount int

	// VerifiedNonNHLCount is the number of players confirmed to have 0 NHL games.
	VerifiedNonNHLCount int

	// NotFoundCount is the number of players not found in the NHL database.
	NotFoundCount int

	// UnverifiedCount is the number of players whose verification failed
	// (NHL API error). They are not cached and will be re-verified next run.
	UnverifiedCount int
}

// LoadUnmatchedInput selects the unmatched Yahoo IDs phase 3 verifies.
type LoadUnmatchedInput struct {
	// Seasons are the start years of the seasons the run covers: only the
	// Yahoo IDs a game player list of one of them names are loaded. Nil
	// (unknown: a legacy chain, or a manifest created without seasons) loads
	// every unmatched Yahoo ID.
	Seasons []int
}

// LoadUnmatchedYahooPlayers loads the list of unmatched Yahoo players from Redis.
// This is the first step of Phase 3, allowing the workflow to know the total count for progress tracking.
func (a *Activities) LoadUnmatchedYahooPlayers(ctx context.Context, input LoadUnmatchedInput) ([]UnmatchedYahooPlayer, error) {
	logger := activity.GetLogger(ctx)

	unmatched, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, input)
	if err != nil {
		return nil, err
	}

	logger.Info("Loaded unmatched Yahoo players", "count", len(unmatched),
		"seasons", input.Seasons, "skipped_other_seasons", skipped)
	return unmatched, nil
}

// loadUnmatchedYahooPlayersImpl contains the testable logic for
// LoadUnmatchedYahooPlayers. It also returns how many unmatched Yahoo IDs
// input.Seasons left out.
func (a *Activities) loadUnmatchedYahooPlayersImpl(ctx context.Context, input LoadUnmatchedInput) ([]UnmatchedYahooPlayer, int, error) {
	unmatchedIDs, err := GetUnmatchedYahooIDs(ctx, a.RedisClient)
	if err != nil {
		return nil, 0, err
	}

	var unmatched []UnmatchedYahooPlayer
	skipped := 0
	for _, id := range unmatchedIDs {
		player, err := GetYahooPlayerByID(ctx, a.RedisClient, id)
		if err != nil {
			log.Warn().Err(err).Int("yahoo_id", int(id)).Msg("Failed to load unmatched Yahoo player details")
			if input.Seasons != nil {
				skipped++ // without its seasons it is not known to be in the run's
				continue
			}
			unmatched = append(unmatched, UnmatchedYahooPlayer{YahooID: id})
			continue
		}
		if !listedInSeasons(player, input.Seasons) {
			skipped++
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

	return unmatched, skipped, nil
}

// listedInSeasons reports whether a game player list of one of seasons names
// player; every player is when seasons is nil (unknown).
func listedInSeasons(player *store.YahooPlayer, seasons []int) bool {
	if seasons == nil {
		return true
	}
	for _, season := range player.Seasons {
		if slices.Contains(seasons, season) {
			return true
		}
	}
	return false
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

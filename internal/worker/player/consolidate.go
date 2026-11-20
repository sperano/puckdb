package player

import (
	"context"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
)

// ConsolidatePlayersInput contains parameters for the consolidation activity.
type ConsolidatePlayersInput struct {
	Seasons []nhl.Season
}

// ConsolidatePlayersResult contains the result of consolidation.
type ConsolidatePlayersResult struct {
	TotalPlayers  int
	UniquePlayers int
}

// ConsolidateBoxscorePlayersActivity loads players from all seasons,
// deduplicates them, and saves the consolidated set to Redis atomically with
// the start years of the seasons it loaded. A season whose list cannot be
// read fails the activity (Temporal retries it) rather than leaving the
// season's unmatched Yahoo IDs out of phase 3; a season with no saved list
// (expired, never extracted) adds no players and is not one of the seasons.
func (a *Activities) ConsolidateBoxscorePlayersActivity(ctx context.Context, input ConsolidatePlayersInput) (ConsolidatePlayersResult, error) {
	logger := activity.GetLogger(ctx)
	redisClient := a.RedisClient

	playerMap := make(map[int64]store.BoxscorePlayer)
	totalPlayers := 0
	loadedSeasons := make([]int, 0, len(input.Seasons))

	for _, season := range input.Seasons {
		players, err := cache.LoadBoxscorePlayers(ctx, redisClient, season)
		if err != nil {
			return ConsolidatePlayersResult{}, fmt.Errorf("load boxscore players of season %d: %w", season.ID(), err)
		}
		if players == nil {
			logger.Warn("No saved boxscore players for season, leaving it out", "season", season.ID())
			continue
		}

		loadedSeasons = append(loadedSeasons, season.StartYear())
		totalPlayers += len(players)
		for _, p := range players {
			playerMap[p.ID] = p
		}
	}

	uniquePlayers := make([]store.BoxscorePlayer, 0, len(playerMap))
	for _, p := range playerMap {
		uniquePlayers = append(uniquePlayers, p)
	}

	if err := cache.SaveConsolidatedBoxscorePlayers(ctx, redisClient, uniquePlayers, loadedSeasons, a.BoxscorePlayerCacheTTL); err != nil {
		return ConsolidatePlayersResult{}, err
	}

	logger.Info("Consolidated boxscore players",
		"seasons", len(loadedSeasons),
		"requested_seasons", len(input.Seasons),
		"total_players", totalPlayers,
		"unique_players", len(uniquePlayers))

	return ConsolidatePlayersResult{
		TotalPlayers:  totalPlayers,
		UniquePlayers: len(uniquePlayers),
	}, nil
}

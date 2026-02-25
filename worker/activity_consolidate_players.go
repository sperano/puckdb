package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

// ConsolidatePlayersInput contains parameters for the consolidation activity.
type ConsolidatePlayersInput struct {
	Seasons []int
	TTL     time.Duration
}

// ConsolidatePlayersResult contains the result of consolidation.
type ConsolidatePlayersResult struct {
	TotalPlayers  int
	UniquePlayers int
}

// ConsolidateBoxscorePlayersActivity loads players from all seasons,
// deduplicates them, and saves the consolidated set to Redis.
func ConsolidateBoxscorePlayersActivity(ctx context.Context, input ConsolidatePlayersInput) (ConsolidatePlayersResult, error) {
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	// Map by player ID to deduplicate
	playerMap := make(map[int64]store.BoxscorePlayer)
	totalPlayers := 0

	for _, season := range input.Seasons {
		players, err := cache.LoadBoxscorePlayers(ctx, redisClient, season)
		if err != nil {
			log.Warn().Err(err).Int("season", season).Msg("Failed to load players for season, skipping")
			continue
		}

		totalPlayers += len(players)
		for _, p := range players {
			playerMap[p.ID] = p
		}
	}

	// Convert map to slice
	uniquePlayers := make([]store.BoxscorePlayer, 0, len(playerMap))
	for _, p := range playerMap {
		uniquePlayers = append(uniquePlayers, p)
	}

	// Save consolidated set
	if err := cache.SaveAllBoxscorePlayers(ctx, redisClient, uniquePlayers, input.TTL); err != nil {
		return ConsolidatePlayersResult{}, err
	}

	log.Info().
		Int("seasons", len(input.Seasons)).
		Int("totalPlayers", totalPlayers).
		Int("uniquePlayers", len(uniquePlayers)).
		Msg("Consolidated boxscore players")

	return ConsolidatePlayersResult{
		TotalPlayers:  totalPlayers,
		UniquePlayers: len(uniquePlayers),
	}, nil
}

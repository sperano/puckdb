package player

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
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
// deduplicates them, and saves the consolidated set to Redis.
func ConsolidateBoxscorePlayersActivity(ctx context.Context, input ConsolidatePlayersInput) (ConsolidatePlayersResult, error) {
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close redis client")
		}
	}()

	playerMap := make(map[int64]store.BoxscorePlayer)
	totalPlayers := 0

	for _, season := range input.Seasons {
		players, err := cache.LoadBoxscorePlayers(ctx, redisClient, season)
		if err != nil {
			log.Warn().Err(err).Int("season", season.ID()).Msg("Failed to load players for season, skipping")
			continue
		}

		totalPlayers += len(players)
		for _, p := range players {
			playerMap[p.ID] = p
		}
	}

	uniquePlayers := make([]store.BoxscorePlayer, 0, len(playerMap))
	for _, p := range playerMap {
		uniquePlayers = append(uniquePlayers, p)
	}

	ttl := time.Duration(viper.GetInt(config.FlagBoxscorePlayerCacheTTL)) * time.Minute
	if err := cache.SaveAllBoxscorePlayers(ctx, redisClient, uniquePlayers, ttl); err != nil {
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

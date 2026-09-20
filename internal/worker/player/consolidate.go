package player

import (
	"context"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/store"
	"github.com/spf13/viper"
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
// deduplicates them, and saves the consolidated set to Redis.
func ConsolidateBoxscorePlayersActivity(ctx context.Context, input ConsolidatePlayersInput) (ConsolidatePlayersResult, error) {
	logger := activity.GetLogger(ctx)
	redisClient := cache.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("failed to close redis client", "error", err)
		}
	}()

	playerMap := make(map[int64]store.BoxscorePlayer)
	totalPlayers := 0

	for _, season := range input.Seasons {
		players, err := cache.LoadBoxscorePlayers(ctx, redisClient, season)
		if err != nil {
			logger.Warn("Failed to load players for season, skipping", "season", season.ID(), "error", err)
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

	logger.Info("Consolidated boxscore players",
		"seasons", len(input.Seasons),
		"totalPlayers", totalPlayers,
		"uniquePlayers", len(uniquePlayers))

	return ConsolidatePlayersResult{
		TotalPlayers:  totalPlayers,
		UniquePlayers: len(uniquePlayers),
	}, nil
}

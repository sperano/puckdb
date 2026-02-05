package worker

import (
	"context"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
	"go.temporal.io/sdk/activity"
)

// ListYahooPlayerFilesActivity lists all Yahoo player file IDs from the cache.
// This is a fast operation that just reads the directory listing.
func ListYahooPlayerFilesActivity(ctx context.Context) ([]int, error) {
	logger := activity.GetLogger(ctx)
	fs := cache.NewSimpleCache().(*cache.InstrumentedFS).Inner()

	files, err := cache.ListAll(fs, cache.YahooPlayerFile{}, cache.ParseYahooPlayerFilename)
	if err != nil {
		return nil, err
	}

	ids := make([]int, len(files))
	for i, f := range files {
		ids[i] = f.(cache.YahooPlayerFile).PlayerID
	}

	logger.Info("Listed Yahoo player files", "count", len(ids))
	return ids, nil
}

// ParseYahooPlayerBatchActivity parses a batch of Yahoo player HTML files.
// Returns the parsed players for aggregation by the workflow.
func ParseYahooPlayerBatchActivity(ctx context.Context, playerIDs []int) ([]cache.YahooPlayer, error) {
	logger := activity.GetLogger(ctx)
	fs := cache.NewSimpleCache().(*cache.InstrumentedFS).Inner()

	players := make([]cache.YahooPlayer, 0, len(playerIDs))
	for _, id := range playerIDs {
		file := cache.YahooPlayerFile{PlayerID: id}
		content, err := fs.Read(file)
		if err != nil {
			logger.Warn("Failed to read Yahoo player file", "playerID", id, "error", err)
			continue
		}

		player, err := cache.ParseYahooPlayerHTML(id, content)
		if err != nil {
			logger.Warn("Failed to parse Yahoo player HTML", "playerID", id, "error", err)
			continue
		}

		players = append(players, *player)
	}

	logger.Debug("Parsed Yahoo player batch", "requested", len(playerIDs), "parsed", len(players))
	return players, nil
}

// SaveYahooPlayersToRedisActivity saves parsed Yahoo players to Redis.
// This is the final step of Phase 1.
// Returns the result including how many players were skipped (verified non-NHL).
func SaveYahooPlayersToRedisActivity(ctx context.Context, players []cache.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	logger := activity.GetLogger(ctx)
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	result, err := SaveYahooIDPool(ctx, redisClient, players)
	if err != nil {
		return nil, err
	}

	logger.Info("Saved Yahoo players to Redis",
		"total", result.TotalPlayers,
		"available", result.AvailablePlayers,
		"skipped_non_nhl", result.SkippedNonNHL)
	return result, nil
}

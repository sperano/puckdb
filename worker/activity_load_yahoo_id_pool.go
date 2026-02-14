package worker

import (
	"context"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// ListYahooPlayerFilesActivity lists all Yahoo player file IDs from the cache.
// This is a fast operation that just reads the directory listing.
func ListYahooPlayerFilesActivity(ctx context.Context) ([]store.YahooPlayerID, error) {
	logger := activity.GetLogger(ctx)
	fs := store.NewStore()

	ids, err := listYahooPlayerFilesImpl(fs)
	if err != nil {
		return nil, err
	}

	logger.Info("Listed Yahoo player files", "count", len(ids))
	return ids, nil
}

// listYahooPlayerFilesImpl contains the testable logic for ListYahooPlayerFilesActivity.
func listYahooPlayerFilesImpl(fs store.Store) ([]store.YahooPlayerID, error) {
	files, err := fs.ListFiles(store.YahooPlayerFile{}, store.ParseYahooPlayerFilename)
	if err != nil {
		return nil, err
	}

	ids := make([]store.YahooPlayerID, len(files))
	for i, f := range files {
		ids[i] = f.(store.YahooPlayerFile).PlayerID
	}

	return ids, nil
}

// ParseYahooPlayerBatchResult contains the results of parsing a batch of Yahoo player files.
type ParseYahooPlayerBatchResult struct {
	Players    []store.YahooPlayer
	ReadErrors int
	ParseErrors int
}

// ParseYahooPlayerBatchActivity parses a batch of Yahoo player HTML files.
// Returns the parsed players for aggregation by the workflow.
func ParseYahooPlayerBatchActivity(ctx context.Context, playerIDs []store.YahooPlayerID) ([]store.YahooPlayer, error) {
	logger := activity.GetLogger(ctx)
	fs := store.NewStore()

	result := parseYahooPlayerBatchImpl(fs, playerIDs)

	if result.ReadErrors > 0 || result.ParseErrors > 0 {
		logger.Warn("Some players failed to parse",
			"readErrors", result.ReadErrors,
			"parseErrors", result.ParseErrors)
	}
	logger.Debug("Parsed Yahoo player batch", "requested", len(playerIDs), "parsed", len(result.Players))
	return result.Players, nil
}

// parseYahooPlayerBatchImpl contains the testable logic for ParseYahooPlayerBatchActivity.
func parseYahooPlayerBatchImpl(fs store.Store, playerIDs []store.YahooPlayerID) ParseYahooPlayerBatchResult {
	result := ParseYahooPlayerBatchResult{
		Players: make([]store.YahooPlayer, 0, len(playerIDs)),
	}

	for _, id := range playerIDs {
		file := store.YahooPlayerFile{PlayerID: id}
		content, err := fs.Read(file)
		if err != nil {
			result.ReadErrors++
			continue
		}

		player, err := store.ParseYahooPlayerHTML(id, content)
		if err != nil {
			result.ParseErrors++
			continue
		}

		result.Players = append(result.Players, *player)
	}

	return result
}

// SaveYahooPlayersToRedisActivity saves parsed Yahoo players to Redis.
// This is the final step of Phase 1.
// Returns the result including how many players were skipped (verified non-NHL).
func SaveYahooPlayersToRedisActivity(ctx context.Context, players []store.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	logger := activity.GetLogger(ctx)
	redisClient := cache.NewClient()
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

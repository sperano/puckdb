package player

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"regexp"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// ListYahooPlayerFiles lists all Yahoo player file IDs from the cache.
// This is a fast operation that just reads the directory listing.
func (a *Activities) ListYahooPlayerFiles(ctx context.Context) ([]store.YahooPlayerID, error) {
	logger := activity.GetLogger(ctx)
	ids, err := listYahooPlayers(a.Storage)
	if err != nil {
		return nil, err
	}

	logger.Info("Listed Yahoo player files", "count", len(ids))
	return ids, nil
}

// listYahooPlayers returns all Yahoo player IDs that have data stored.
func listYahooPlayers(storage store.Storage) ([]store.YahooPlayerID, error) {
	names, err := storage.List(resource.YahooPlayersDir, "html")
	if err != nil {
		return nil, err
	}

	pattern := regexp.MustCompile(`^player-(\d+)$`)
	var ids []store.YahooPlayerID

	for _, name := range names {
		if m := pattern.FindStringSubmatch(name); m != nil {
			id, _ := strconv.Atoi(m[1])
			ids = append(ids, store.YahooPlayerID(id))
		}
	}

	return ids, nil
}

// ParseYahooPlayerBatchResult contains the results of parsing a batch of Yahoo player files.
type ParseYahooPlayerBatchResult struct {
	Players     []store.YahooPlayer
	ReadErrors  int
	ParseErrors int
}

// ParseYahooPlayerBatch parses a batch of Yahoo player HTML files.
// Returns the parsed players for aggregation by the workflow.
func (a *Activities) ParseYahooPlayerBatch(ctx context.Context, playerIDs []store.YahooPlayerID) ([]store.YahooPlayer, error) {
	logger := activity.GetLogger(ctx)
	result := parseYahooPlayerBatchImpl(ctx, a.Storage, a.GobCache, playerIDs)

	if result.ReadErrors > 0 || result.ParseErrors > 0 {
		logger.Warn("Some players failed to parse",
			"readErrors", result.ReadErrors,
			"parseErrors", result.ParseErrors)
	}
	logger.Debug("Parsed Yahoo player batch", "requested", len(playerIDs), "parsed", len(result.Players))
	return result.Players, nil
}

// parseYahooPlayerBatchImpl contains the testable logic for ParseYahooPlayerBatch.
func parseYahooPlayerBatchImpl(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, playerIDs []store.YahooPlayerID) ParseYahooPlayerBatchResult {
	result := ParseYahooPlayerBatchResult{
		Players: make([]store.YahooPlayer, 0, len(playerIDs)),
	}

	for _, id := range playerIDs {
		playerRes := resource.YahooPlayer{PlayerID: id}
		content, _, err := cache.ReadParsedCached(ctx, storage, gobCache, playerRes)
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

// SaveYahooPlayersToRedis saves parsed Yahoo players to Redis.
// This is the final step of Phase 1.
// Returns the result including how many players were skipped (verified non-NHL).
func (a *Activities) SaveYahooPlayersToRedis(ctx context.Context, players []store.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	logger := activity.GetLogger(ctx)

	result, err := saveYahooPlayersToRedisImpl(ctx, a.RedisClient, players)
	if err != nil {
		return nil, err
	}

	logger.Info("Saved Yahoo players to Redis",
		"total", result.TotalPlayers,
		"available", result.AvailablePlayers,
		"skipped_non_nhl", result.SkippedNonNHL)
	return result, nil
}

func saveYahooPlayersToRedisImpl(ctx context.Context, client cache.Client, players []store.YahooPlayer) (*SaveYahooIDPoolResult, error) {
	if len(players) == 0 {
		return &SaveYahooIDPoolResult{}, nil
	}

	verifiedNonNHL, err := LoadVerifiedNonNHLIDs(ctx, client)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load verified non-NHL IDs, including all players")
		verifiedNonNHL = make(map[store.YahooPlayerID]struct{})
	}

	pipe := client.Pipeline()

	for _, player := range players {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(player); err != nil {
			return nil, fmt.Errorf("encode yahoo player %d: %w", player.YahooID, err)
		}
		pipe.HSet(ctx, YahooIDPoolKey, player.YahooID.String(), buf.Bytes())
	}

	var availableIDs []interface{}
	excludedCount := 0
	for _, player := range players {
		if _, isVerifiedNonNHL := verifiedNonNHL[player.YahooID]; !isVerifiedNonNHL {
			availableIDs = append(availableIDs, player.YahooID.String())
		} else {
			excludedCount++
		}
	}

	if len(availableIDs) > 0 {
		pipe.SAdd(ctx, YahooIDAvailableKey, availableIDs...)
	}

	pipe.Expire(ctx, YahooIDPoolKey, ImportPlayersTTL)
	pipe.Expire(ctx, YahooIDAvailableKey, ImportPlayersTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("save yahoo id pool to redis: %w", err)
	}

	log.Info().
		Int("total_players", len(players)).
		Int("available", len(availableIDs)).
		Int("excluded_verified_non_nhl", excludedCount).
		Msg("Saved YahooID pool to Redis")

	return &SaveYahooIDPoolResult{
		TotalPlayers:     len(players),
		AvailablePlayers: len(availableIDs),
		SkippedNonNHL:    excludedCount,
	}, nil
}

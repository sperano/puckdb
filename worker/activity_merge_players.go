package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/redis"
)

// playerMatchKey creates a lookup key from name and sweater number for matching
// Yahoo players to NHL boxscore players.
func playerMatchKey(firstName, lastName string, sweaterNumber int) string {
	return fmt.Sprintf("%s|%s|%d",
		strings.ToLower(strings.TrimSpace(firstName)),
		strings.ToLower(strings.TrimSpace(lastName)),
		sweaterNumber)
}

// MergeSeasonPlayersActivity merges Yahoo and Boxscore players for one season.
// Yahoo and NHL use different player ID systems, so we match by name + uniform number.
// Boxscore players (with real NHL IDs) are the primary source; Yahoo provides image URLs.
// The resulting map uses NHL player IDs as keys.
func MergeSeasonPlayersActivity(
	ctx context.Context,
	yahoo map[int64]PartialPlayer,
	boxscore map[int64]PartialPlayer,
) (map[int64]PartialPlayer, error) {
	// Build Yahoo lookup by name+number for matching
	yahooByKey := make(map[string]PartialPlayer)
	for _, p := range yahoo {
		if p.SweaterNumber > 0 {
			key := playerMatchKey(p.FirstName, p.LastName, p.SweaterNumber)
			yahooByKey[key] = p
		}
	}

	// Start with boxscore players (they have real NHL IDs)
	merged := make(map[int64]PartialPlayer)
	matchCount := 0

	for id, bp := range boxscore {
		player := bp

		// Try to find matching Yahoo player by name+number
		if bp.SweaterNumber > 0 {
			key := playerMatchKey(bp.FirstName, bp.LastName, bp.SweaterNumber)
			if yp, ok := yahooByKey[key]; ok {
				// Found match - enrich with Yahoo image URLs
				player.YahooHomeURL = yp.YahooHomeURL
				player.YahooImageSmall = yp.YahooImageSmall
				player.YahooImageMedium = yp.YahooImageMedium
				player.YahooImageLarge = yp.YahooImageLarge
				player.HasYahooData = true
				matchCount++
			}
		}

		merged[id] = player
	}

	log.Info().
		Int("yahoo", len(yahoo)).
		Int("boxscore", len(boxscore)).
		Int("matched", matchCount).
		Int("merged", len(merged)).
		Msg("Season players merged (by name+number)")

	return merged, nil
}

// MergeAllSeasonsActivity merges player maps from all seasons into a single deduplicated map.
// For players appearing in multiple seasons, it keeps the most complete data.
func MergeAllSeasonsActivity(
	ctx context.Context,
	seasonResults []map[int64]PartialPlayer,
) (map[int64]PartialPlayer, error) {
	merged := make(map[int64]PartialPlayer)

	for _, seasonPlayers := range seasonResults {
		for id, p := range seasonPlayers {
			if existing, ok := merged[id]; ok {
				// Keep the most complete data
				// Prefer data with both sources, then boxscore, then yahoo
				if p.HasBoxscoreData && p.HasYahooData {
					merged[id] = p
				} else if !existing.HasBoxscoreData && p.HasBoxscoreData {
					// Keep existing Yahoo images, update with boxscore info
					p.YahooHomeURL = existing.YahooHomeURL
					p.YahooImageSmall = existing.YahooImageSmall
					p.YahooImageMedium = existing.YahooImageMedium
					p.YahooImageLarge = existing.YahooImageLarge
					p.HasYahooData = existing.HasYahooData
					merged[id] = p
				} else if !existing.HasYahooData && p.HasYahooData {
					// Keep existing boxscore info, add Yahoo images
					existing.YahooHomeURL = p.YahooHomeURL
					existing.YahooImageSmall = p.YahooImageSmall
					existing.YahooImageMedium = p.YahooImageMedium
					existing.YahooImageLarge = p.YahooImageLarge
					existing.HasYahooData = true
					merged[id] = existing
				}
				// Otherwise keep existing
			} else {
				merged[id] = p
			}
		}
	}

	log.Info().
		Int("seasons", len(seasonResults)).
		Int("total_players", len(merged)).
		Msg("All seasons merged")

	return merged, nil
}

// MergePlayerBatchesFromRedisActivity loads player batches from Redis and merges them.
// It deletes the Redis keys after successful merge.
// source is either "yahoo" or "boxscore" to determine merge strategy.
func MergePlayerBatchesFromRedisActivity(
	ctx context.Context,
	keys []string,
	source string,
) (map[int64]PartialPlayer, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return mergePlayerBatchesFromRedisImpl(ctx, redisClient, keys, source)
}

// mergePlayerBatchesFromRedisImpl is the testable implementation.
func mergePlayerBatchesFromRedisImpl(
	ctx context.Context,
	redisClient redis.Client,
	keys []string,
	source string,
) (map[int64]PartialPlayer, error) {
	if len(keys) == 0 {
		return make(map[int64]PartialPlayer), nil
	}

	log.Info().
		Int("batches", len(keys)).
		Str("source", source).
		Msg("Merge from Redis: starting")

	merged := make(map[int64]PartialPlayer)

	for i, key := range keys {
		data, err := redis.LoadPlayerBatch(ctx, redisClient, key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load batch from Redis, skipping")
			continue
		}

		var players map[int64]PartialPlayer
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to decode batch, skipping")
			continue
		}

		// Merge based on source type
		for id, p := range players {
			if source == "yahoo" {
				if existing, ok := merged[id]; !ok || !existing.HasYahooData {
					merged[id] = p
				}
			} else {
				if existing, ok := merged[id]; !ok || !existing.HasBoxscoreData {
					merged[id] = p
				}
			}
		}

		log.Debug().
			Int("batch", i+1).
			Int("total_batches", len(keys)).
			Int("batch_players", len(players)).
			Int("merged_players", len(merged)).
			Msg("Merge from Redis: batch merged")
	}

	// Clean up Redis keys
	if err := redis.DeletePlayerBatches(ctx, redisClient, keys); err != nil {
		log.Warn().Err(err).Msg("Failed to delete Redis keys, they will expire via TTL")
	}

	log.Info().
		Int("batches", len(keys)).
		Int("total_players", len(merged)).
		Str("source", source).
		Msg("Merge from Redis: complete")

	return merged, nil
}

/*
// StoreSeasonResultActivity stores a merged season result in Redis.
// Returns a BatchResult with the Redis key for later retrieval.
func StoreSeasonResultActivity(
	ctx context.Context,
	season config.Season,
	players map[int64]PartialPlayer,
) (BatchResult, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return storeSeasonResultImpl(ctx, redisClient, season, players)
}

// storeSeasonResultImpl is the testable implementation.
func storeSeasonResultImpl(
	ctx context.Context,
	redisClient redis.Client,
	season config.Season,
	players map[int64]PartialPlayer,
) (BatchResult, error) {
	key := redis.SeasonResultKey(season.StartYear())

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(players); err != nil {
		return BatchResult{}, err
	}

	if err := redis.SavePlayerBatch(ctx, redisClient, key, buf.Bytes()); err != nil {
		return BatchResult{}, err
	}

	log.Info().
		Int("season", season.StartYear()).
		Int("players", len(players)).
		Str("key", key).
		Msg("Stored season result in Redis")

	return BatchResult{RedisKey: key, PlayerCount: len(players)}, nil
}
*/

// StoreEnrichmentPlayersActivity stores merged players in Redis for enrichment.
// Returns the Redis key for retrieval by enrichment activities.
func StoreEnrichmentPlayersActivity(
	ctx context.Context,
	players map[int64]PartialPlayer,
) (string, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return storeEnrichmentPlayersImpl(ctx, redisClient, players)
}

// storeEnrichmentPlayersImpl is the testable implementation.
func storeEnrichmentPlayersImpl(
	ctx context.Context,
	redisClient redis.Client,
	players map[int64]PartialPlayer,
) (string, error) {
	key := redis.EnrichmentPlayersKey()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(players); err != nil {
		return "", err
	}

	if err := redis.SavePlayerBatch(ctx, redisClient, key, buf.Bytes()); err != nil {
		return "", err
	}

	log.Info().
		Int("players", len(players)).
		Str("key", key).
		Msg("Stored enrichment players in Redis")

	return key, nil
}

// LoadEnrichmentPlayersActivity loads merged players from Redis for enrichment.
func LoadEnrichmentPlayersActivity(
	ctx context.Context,
	key string,
) (map[int64]PartialPlayer, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return loadEnrichmentPlayersImpl(ctx, redisClient, key)
}

// loadEnrichmentPlayersImpl is the testable implementation.
func loadEnrichmentPlayersImpl(
	ctx context.Context,
	redisClient redis.Client,
	key string,
) (map[int64]PartialPlayer, error) {
	data, err := redis.LoadPlayerBatch(ctx, redisClient, key)
	if err != nil {
		return nil, err
	}

	var players map[int64]PartialPlayer
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
		return nil, err
	}

	log.Info().
		Int("players", len(players)).
		Str("key", key).
		Msg("Loaded enrichment players from Redis")

	return players, nil
}

// MergeAllSeasonsFromRedisActivity loads season results from Redis and merges them.
// This avoids passing large player maps through Temporal serialization.
func MergeAllSeasonsFromRedisActivity(
	ctx context.Context,
	seasonKeys []string,
) (map[int64]PartialPlayer, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return mergeAllSeasonsFromRedisImpl(ctx, redisClient, seasonKeys)
}

// mergeAllSeasonsFromRedisImpl is the testable implementation.
func mergeAllSeasonsFromRedisImpl(
	ctx context.Context,
	redisClient redis.Client,
	seasonKeys []string,
) (map[int64]PartialPlayer, error) {
	if len(seasonKeys) == 0 {
		return make(map[int64]PartialPlayer), nil
	}

	log.Info().
		Int("seasons", len(seasonKeys)).
		Msg("MergeAllSeasonsFromRedis: starting")

	merged := make(map[int64]PartialPlayer)

	for _, key := range seasonKeys {
		data, err := redis.LoadPlayerBatch(ctx, redisClient, key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load season from Redis, skipping")
			continue
		}

		var seasonPlayers map[int64]PartialPlayer
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&seasonPlayers); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to decode season, skipping")
			continue
		}

		// Merge this season's players
		for id, p := range seasonPlayers {
			if existing, ok := merged[id]; ok {
				// Keep the most complete data
				if p.HasBoxscoreData && p.HasYahooData {
					merged[id] = p
				} else if !existing.HasBoxscoreData && p.HasBoxscoreData {
					p.YahooHomeURL = existing.YahooHomeURL
					p.YahooImageSmall = existing.YahooImageSmall
					p.YahooImageMedium = existing.YahooImageMedium
					p.YahooImageLarge = existing.YahooImageLarge
					p.HasYahooData = existing.HasYahooData
					merged[id] = p
				} else if !existing.HasYahooData && p.HasYahooData {
					existing.YahooHomeURL = p.YahooHomeURL
					existing.YahooImageSmall = p.YahooImageSmall
					existing.YahooImageMedium = p.YahooImageMedium
					existing.YahooImageLarge = p.YahooImageLarge
					existing.HasYahooData = true
					merged[id] = existing
				}
			} else {
				merged[id] = p
			}
		}

		log.Debug().
			Str("key", key).
			Int("season_players", len(seasonPlayers)).
			Int("merged_total", len(merged)).
			Msg("MergeAllSeasonsFromRedis: season merged")
	}

	// Clean up Redis keys
	if err := redis.DeletePlayerBatches(ctx, redisClient, seasonKeys); err != nil {
		log.Warn().Err(err).Msg("Failed to delete season Redis keys, they will expire via TTL")
	}

	log.Info().
		Int("seasons", len(seasonKeys)).
		Int("total_players", len(merged)).
		Msg("MergeAllSeasonsFromRedis: complete")

	return merged, nil
}

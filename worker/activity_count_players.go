package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

const (
	// ExtractionCacheTTL is how long extracted player data stays in Redis.
	// Set to 2 hours to cover long-running workflows.
	ExtractionCacheTTL = 2 * time.Hour

	// extractionCachePrefix is the Redis key prefix for cached extractions.
	extractionCachePrefix = "puckdb:extraction:"
)

// extractionCacheKey returns the Redis key for a season's cached extraction.
func extractionCacheKey(startYear int) string {
	return fmt.Sprintf("%s%d", extractionCachePrefix, startYear)
}

// CountPlayersForSeasonActivity extracts players from boxscores, caches the result,
// and returns just the count. This is called by the parent workflow to get counts
// upfront before spawning child workflows.
func CountPlayersForSeasonActivity(ctx context.Context, season SeasonInfo) (int, error) {
	fs := store.NewStore()
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	extractor := func(ctx context.Context, day time.Time) ([]store.BoxscorePlayer, error) {
		return extractPlayersForDay(ctx, fs, redisClient, day)
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, redisClient, season)
	if err != nil {
		return 0, err
	}

	// Cache the result in Redis for child workflow to use
	if err := cacheExtractionResult(ctx, redisClient, season.StartYear(), result); err != nil {
		log.Warn().Err(err).Int("season", season.StartYear()).Msg("Failed to cache extraction result")
		// Continue anyway - child will re-extract if needed
	}

	return len(result.Players), nil
}

// cacheExtractionResult stores the extraction result in Redis.
func cacheExtractionResult(ctx context.Context, client cache.Client, startYear int, result BoxscoreExtractionResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal extraction result: %w", err)
	}

	key := extractionCacheKey(startYear)
	if err := client.Set(ctx, key, data, ExtractionCacheTTL).Err(); err != nil {
		return fmt.Errorf("set extraction cache: %w", err)
	}

	log.Debug().
		Int("season", startYear).
		Int("players", len(result.Players)).
		Str("key", key).
		Msg("Cached extraction result")

	return nil
}

// GetCachedExtractionActivity retrieves a cached extraction result from Redis.
// Returns the result if found, or an empty result if not cached.
func GetCachedExtractionActivity(ctx context.Context, startYear int) (BoxscoreExtractionResult, error) {
	redisClient := cache.NewClient()
	return getCachedExtraction(ctx, redisClient, startYear)
}

// getCachedExtraction retrieves a cached extraction result from Redis.
func getCachedExtraction(ctx context.Context, client cache.Client, startYear int) (BoxscoreExtractionResult, error) {
	key := extractionCacheKey(startYear)

	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		// Cache miss is not an error - return empty result
		log.Debug().Int("season", startYear).Msg("Extraction cache miss")
		return BoxscoreExtractionResult{}, nil
	}

	var result BoxscoreExtractionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return BoxscoreExtractionResult{}, fmt.Errorf("unmarshal cached extraction: %w", err)
	}

	log.Debug().
		Int("season", startYear).
		Int("players", len(result.Players)).
		Msg("Extraction cache hit")

	return result, nil
}

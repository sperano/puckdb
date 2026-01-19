package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/redis"
	"github.com/sperano/puckdb/sqlcdb"
)

const (
	// EnrichmentWorkerCount is the number of concurrent workers for API calls within a batch
	EnrichmentWorkerCount = 10
)

// enrichResult holds the result of enriching a single player
type enrichResult struct {
	index     int
	player    sqlcdb.Player
	enriched  bool
	fromCache bool
}

// PlayerUpserter is the interface for database operations needed by enrichment.
// Extracted from sqlcdb.Queries for testability.
type PlayerUpserter interface {
	UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error
}

// EnrichDeps holds dependencies for player enrichment.
// Used by enrichPlayerBatchImpl for testability.
type EnrichDeps struct {
	FS      cache.FileSystem
	NHL     NHLClient
	Redis   redis.Client
	Queries PlayerUpserter
}

// EnrichPlayerBatchActivity enriches a batch of players with NHL API data.
// It calls nhl.Client.PlayerLanding for each player to get detailed profile information.
// Player landing data is cached to avoid repeated API calls.
// Processing is parallelized using a worker pool for improved performance.
// The partialPlayersKey is a Redis key containing all partial players data.
// Players are saved directly to the database; returns the count of players saved.
func EnrichPlayerBatchActivity(
	ctx context.Context,
	playerIDs []int64,
	partialPlayersKey string,
) (int, error) {
	// Create production dependencies
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	deps := EnrichDeps{
		FS:      cache.NewSimpleCache(),
		NHL:     nhl.NewClient(),
		Redis:   redisClient,
		Queries: database.NewQueries(pool),
	}

	return enrichPlayerBatchImpl(ctx, deps, playerIDs, partialPlayersKey)
}

// enrichPlayerBatchImpl is the testable implementation of EnrichPlayerBatchActivity.
// It accepts dependencies as a parameter for easy mocking in tests.
func enrichPlayerBatchImpl(
	ctx context.Context,
	deps EnrichDeps,
	playerIDs []int64,
	partialPlayersKey string,
) (int, error) {
	if len(playerIDs) == 0 {
		return 0, nil
	}

	log.Info().
		Int("batch_size", len(playerIDs)).
		Int("workers", EnrichmentWorkerCount).
		Int64("first_id", playerIDs[0]).
		Int64("last_id", playerIDs[len(playerIDs)-1]).
		Msg("Enrich batch: starting")

	// Load partial players from Redis
	partialPlayers, err := loadPartialPlayersFromRedis(ctx, deps.Redis, partialPlayersKey)
	if err != nil {
		return 0, err
	}

	// Channel for distributing work
	jobs := make(chan int, len(playerIDs))
	// Channel for collecting results
	results := make(chan enrichResult, len(playerIDs))

	// Atomic counters for progress logging
	var processed atomic.Int32
	var enrichedCount atomic.Int32
	var cacheHits atomic.Int32

	// Start worker pool
	var wg sync.WaitGroup
	for w := 0; w < EnrichmentWorkerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				id := playerIDs[idx]
				partial := partialPlayers[id]

				// Try cache first, then fetch from API if not cached
				playerLanding, fromCache, err := getPlayerLandingWithCache(ctx, deps.FS, deps.NHL, nhl.PlayerID(id))

				var result enrichResult
				result.index = idx
				if err != nil {
					log.Debug().Err(err).Int64("player_id", id).Msg("Failed to fetch player from NHL API")
					result.player = partialToSqlcPlayer(partial, nil)
					result.enriched = false
				} else {
					result.player = partialToSqlcPlayer(partial, playerLanding)
					result.enriched = true
					result.fromCache = fromCache
				}

				results <- result

				// Update counters and log progress
				p := processed.Add(1)
				if result.enriched {
					enrichedCount.Add(1)
					if result.fromCache {
						cacheHits.Add(1)
					}
				}

				if p%10 == 0 || int(p) == len(playerIDs) {
					log.Info().
						Int32("processed", p).
						Int("total", len(playerIDs)).
						Int32("enriched", enrichedCount.Load()).
						Int32("cache_hits", cacheHits.Load()).
						Msg("Enrich batch: progress")
				}
			}
		}()
	}

	// Send all jobs
	for i := range playerIDs {
		jobs <- i
	}
	close(jobs)

	// Wait for workers to finish in a goroutine, then close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results into ordered slice
	players := make([]sqlcdb.Player, len(playerIDs))
	for result := range results {
		players[result.index] = result.player
	}

	// Check for context cancellation
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	// Save players to database
	for _, player := range players {
		if err := deps.Queries.UpsertPlayer(ctx, sqlcdb.UpsertPlayerParams{
			ID:                 player.ID,
			YahooID:            player.YahooID,
			FirstName:          player.FirstName,
			LastName:           player.LastName,
			NhlTeamID:          player.NhlTeamID,
			Position:           player.Position,
			ShootsCatches:      player.ShootsCatches,
			HeightInches:       player.HeightInches,
			WeightPounds:       player.WeightPounds,
			BirthDate:          player.BirthDate,
			BirthCity:          player.BirthCity,
			BirthStateProvince: player.BirthStateProvince,
			BirthCountry:       player.BirthCountry,
			SweaterNumber:      player.SweaterNumber,
			IsActive:           player.IsActive,
			HeadshotUrl:        player.HeadshotUrl,
			HeroImageUrl:       player.HeroImageUrl,
			YahooImageSmall:    player.YahooImageSmall,
			YahooImageMedium:   player.YahooImageMedium,
			YahooImageLarge:    player.YahooImageLarge,
			YahooHomeUrl:       player.YahooHomeUrl,
			PlayerSlug:         player.PlayerSlug,
			DraftYear:          player.DraftYear,
			DraftTeamAbbrev:    player.DraftTeamAbbrev,
			DraftRound:         player.DraftRound,
			DraftPickInRound:   player.DraftPickInRound,
			DraftOverallPick:   player.DraftOverallPick,
		}); err != nil {
			return 0, fmt.Errorf("upsert player %d: %w", player.ID, err)
		}
	}

	log.Info().
		Int("batch_size", len(playerIDs)).
		Int("saved", len(players)).
		Int32("enriched", enrichedCount.Load()).
		Int32("cache_hits", cacheHits.Load()).
		Msg("Enrich batch: complete")

	return len(players), nil
}

// getPlayerLandingWithCache attempts to get player landing data from cache first,
// falling back to the NHL API if not cached. Returns the data and whether it came from cache.
func getPlayerLandingWithCache(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
	playerID nhl.PlayerID,
) (*nhl.PlayerLanding, bool, error) {
	file := fs.New(cache.PlayerLandingFileType, playerID)

	// Check cache first
	if fs.Exists(file) {
		data, err := fs.Read(file)
		if err == nil {
			var landing nhl.PlayerLanding
			if err := json.Unmarshal(data, &landing); err == nil {
				return &landing, true, nil
			}
			log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to unmarshal cached player landing")
		}
	}

	// Fetch from API
	landing, err := client.PlayerLanding(ctx, playerID)
	if err != nil {
		return nil, false, err
	}

	// Save to cache
	data, err := json.Marshal(landing)
	if err != nil {
		log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to marshal player landing for cache")
	} else {
		if err := fs.Write(file, data); err != nil {
			log.Debug().Err(err).Str("player_id", playerID.String()).Msg("Failed to write player landing to cache")
		}
	}

	return landing, false, nil
}

// partialToSqlcPlayer converts PartialPlayer + optional NHL landing to sqlcdb.Player.
// NhlTeamID is only set from NHL API data since Yahoo uses different team IDs.
func partialToSqlcPlayer(partial PartialPlayer, landing *nhl.PlayerLanding) sqlcdb.Player {
	player := sqlcdb.Player{
		ID:               partial.ID,
		YahooID:          pgtype.Int8{Int64: partial.ID, Valid: true},
		FirstName:        partial.FirstName,
		LastName:         partial.LastName,
		Position:         partial.Position,
		SweaterNumber:    pgtype.Int4{Int32: int32(partial.SweaterNumber), Valid: partial.SweaterNumber > 0},
		NhlTeamID:        pgtype.Int8{Valid: false}, // Only set from NHL API, Yahoo uses different IDs
		YahooImageSmall:  partial.YahooImageSmall,
		YahooImageMedium: partial.YahooImageMedium,
		YahooImageLarge:  partial.YahooImageLarge,
		YahooHomeUrl:     partial.YahooHomeURL,
		IsActive:         false, // Will be updated from landing
		ShootsCatches:    "",    // Will be updated from landing
		HeadshotUrl:      "",    // Will be updated from landing
	}

	if landing != nil {
		// Override with official NHL data
		player.FirstName = landing.FirstName.Default
		player.LastName = landing.LastName.Default
		player.Position = string(landing.Position)
		player.ShootsCatches = string(landing.ShootsCatches)
		player.HeightInches = pgtype.Int4{Int32: int32(landing.HeightInInches), Valid: true}
		player.WeightPounds = pgtype.Int4{Int32: int32(landing.WeightInPounds), Valid: true}
		player.IsActive = landing.IsActive
		player.HeadshotUrl = landing.Headshot

		if landing.SweaterNumber != nil {
			player.SweaterNumber = pgtype.Int4{Int32: int32(*landing.SweaterNumber), Valid: true}
		}
		if landing.CurrentTeamID != nil {
			player.NhlTeamID = pgtype.Int8{Int64: int64(*landing.CurrentTeamID), Valid: true}
		} else {
			// Player is not on a current team - clear potentially stale team ID from partial data
			player.NhlTeamID = pgtype.Int8{Valid: false}
		}
		if landing.HeroImage != nil {
			player.HeroImageUrl = pgtype.Text{String: *landing.HeroImage, Valid: true}
		}
		if landing.PlayerSlug != nil {
			player.PlayerSlug = pgtype.Text{String: *landing.PlayerSlug, Valid: true}
		}

		// Birth info
		if birthDate, err := time.Parse("2006-01-02", landing.BirthDate); err == nil {
			player.BirthDate = pgtype.Date{Time: birthDate, Valid: true}
		}
		if landing.BirthCity != nil {
			player.BirthCity = pgtype.Text{String: landing.BirthCity.Default, Valid: true}
		}
		if landing.BirthStateProvince != nil {
			player.BirthStateProvince = pgtype.Text{String: landing.BirthStateProvince.Default, Valid: true}
		}
		if landing.BirthCountry != nil {
			player.BirthCountry = pgtype.Text{String: *landing.BirthCountry, Valid: true}
		}

		// Draft details
		if landing.DraftDetails != nil {
			player.DraftYear = pgtype.Int4{Int32: int32(landing.DraftDetails.Year), Valid: true}
			player.DraftTeamAbbrev = pgtype.Text{String: landing.DraftDetails.TeamAbbrev, Valid: true}
			player.DraftRound = pgtype.Int4{Int32: int32(landing.DraftDetails.Round), Valid: true}
			player.DraftPickInRound = pgtype.Int4{Int32: int32(landing.DraftDetails.PickInRound), Valid: true}
			player.DraftOverallPick = pgtype.Int4{Int32: int32(landing.DraftDetails.OverallPick), Valid: true}
		}
	}

	return player
}

// loadPartialPlayersFromRedis loads partial players data from Redis.
func loadPartialPlayersFromRedis(ctx context.Context, redisClient redis.Client, key string) (map[int64]PartialPlayer, error) {
	data, err := redis.LoadPlayerBatch(ctx, redisClient, key)
	if err != nil {
		return nil, err
	}

	var players map[int64]PartialPlayer
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&players); err != nil {
		return nil, err
	}

	return players, nil
}

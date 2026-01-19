package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
)

// ExtractYahooPlayersForDayActivity extracts players from all Yahoo game files for one day.
func ExtractYahooPlayersForDayActivity(ctx context.Context, day time.Time) (map[int64]PartialPlayer, error) {
	players := make(map[int64]PartialPlayer)
	fs := cache.NewSimpleCache()

	// Get list of game files for this day
	gameLinks, err := getGameLinksForDay(fs, day)
	if err != nil {
		// No games for this day is not an error
		log.Debug().Time("day", day).Msg("No games list for day")
		return players, nil
	}

	for _, link := range gameLinks {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		file := fs.New(cache.GameFileType, day, link)
		if !fs.Exists(file) {
			log.Debug().Str("file", link.Name()).Msg("Game file does not exist, skipping")
			continue
		}

		game, err := cache.ParseGameHTML(fs, file)
		if err != nil {
			log.Warn().Err(err).Str("file", link.Name()).Msg("Failed to parse game")
			continue
		}

		if game.IsPostponed() || game.IsAllStar() {
			continue
		}

		// Extract players from this game
		for _, pi := range game.Context.Dispatcher.Stores.PlayersStore.Players {
			id, err := pi.PlayerID.ID()
			if err != nil {
				log.Warn().Err(err).Msg("Failed to parse player ID")
				continue
			}

			teamID, err := pi.NHLTeamID.ID()
			if err != nil {
				log.Warn().Err(err).Msg("Failed to parse team ID")
				continue
			}

			uniform := 0
			if pi.UniformNumber != "" {
				uniform, _ = strconv.Atoi(pi.UniformNumber)
			}

			players[int64(id)] = PartialPlayer{
				ID:               int64(id),
				FirstName:        pi.FirstName,
				LastName:         pi.LastName,
				Position:         string(pi.PrimaryPositionID),
				SweaterNumber:    uniform,
				NHLTeamID:        int64(teamID),
				YahooHomeURL:     pi.HomeURL,
				YahooImageSmall:  pi.ImageSmall,
				YahooImageMedium: pi.ImageMedium,
				YahooImageLarge:  pi.ImageLarge,
				HasYahooData:     true,
			}
		}
	}

	log.Debug().Time("day", day).Int("players", len(players)).Msg("Yahoo extraction for day complete")
	return players, nil
}

// ExtractYahooPlayersForDayBatchActivity extracts players from Yahoo game files for a batch of days.
// Results are stored in Redis to avoid workflow history size limits.
// Returns a BatchResult with the Redis key and player count.
func ExtractYahooPlayersForDayBatchActivity(ctx context.Context, input BatchInput) (BatchResult, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return extractYahooPlayersForDayBatchImpl(ctx, redisClient, input)
}

// extractYahooPlayersForDayBatchImpl is the testable implementation.
func extractYahooPlayersForDayBatchImpl(
	ctx context.Context,
	redisClient redis.Client,
	input BatchInput,
) (BatchResult, error) {
	allPlayers := make(map[int64]PartialPlayer)

	if len(input.Days) == 0 {
		return BatchResult{}, nil
	}

	log.Info().
		Time("start", input.Days[0]).
		Time("end", input.Days[len(input.Days)-1]).
		Int("days", len(input.Days)).
		Int("season", input.Season).
		Int("batch", input.BatchIndex).
		Msg("Yahoo batch: starting")

	for i, day := range input.Days {
		select {
		case <-ctx.Done():
			return BatchResult{}, ctx.Err()
		default:
		}

		dayPlayers, err := ExtractYahooPlayersForDayActivity(ctx, day)
		if err != nil {
			log.Warn().Err(err).Time("day", day).Msg("Failed to extract Yahoo players for day, continuing")
			continue
		}

		// Merge players, keeping first occurrence with Yahoo data
		for id, p := range dayPlayers {
			if existing, ok := allPlayers[id]; !ok || !existing.HasYahooData {
				allPlayers[id] = p
			}
		}

		// Log progress every 10 days or on last day
		if (i+1)%10 == 0 || i == len(input.Days)-1 {
			log.Info().
				Int("processed", i+1).
				Int("total", len(input.Days)).
				Int("players_found", len(allPlayers)).
				Msg("Yahoo batch: progress")
		}
	}

	// Encode with gob
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(allPlayers); err != nil {
		return BatchResult{}, err
	}

	// Save to Redis
	redisKey := redis.PlayerBatchKey("yahoo", input.Season, input.BatchIndex)
	if err := redis.SavePlayerBatch(ctx, redisClient, redisKey, buf.Bytes()); err != nil {
		return BatchResult{}, err
	}

	log.Info().
		Int("days", len(input.Days)).
		Int("players", len(allPlayers)).
		Str("redis_key", redisKey).
		Msg("Yahoo batch: complete")

	return BatchResult{
		RedisKey:    redisKey,
		PlayerCount: len(allPlayers),
	}, nil
}

// getGameLinksForDay returns game links for a specific day from the games list file.
func getGameLinksForDay(fs *cache.SimpleFS, day time.Time) ([]cache.GameLink, error) {
	file := fs.New(cache.GamesListFileType, day)
	if !fs.Exists(file) {
		return nil, nil
	}
	return cache.ParseGamesList(fs, file)
}

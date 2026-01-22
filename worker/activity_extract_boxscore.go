package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
)

// ExtractBoxscorePlayersForDayActivity extracts players from all boxscore files for one day.
func ExtractBoxscorePlayersForDayActivity(ctx context.Context, day time.Time) (map[int64]PartialPlayer, error) {
	players := make(map[int64]PartialPlayer)
	fs := cache.NewSimpleCache()

	// Get list of boxscore files for this day via daily schedule
	boxscoreFiles, err := getBoxscoreFilesForDay(fs, day)
	if err != nil {
		log.Debug().Time("day", day).Msg("No daily schedule for day")
		return players, nil
	}

	for _, file := range boxscoreFiles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if !fs.Exists(file) {
			continue
		}

		content, err := fs.Read(file)
		if err != nil {
			log.Warn().Err(err).Str("file", cache.Path(file)).Msg("Failed to read boxscore")
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(content, &boxscore); err != nil {
			log.Warn().Err(err).Str("file", cache.Path(file)).Msg("Failed to parse boxscore")
			continue
		}

		// Extract from home team
		extractTeamPlayers(players, &boxscore.PlayerByGameStats.HomeTeam, int64(boxscore.HomeTeam.ID))
		// Extract from away team
		extractTeamPlayers(players, &boxscore.PlayerByGameStats.AwayTeam, int64(boxscore.AwayTeam.ID))
	}

	log.Debug().Time("day", day).Int("players", len(players)).Msg("Boxscore extraction for day complete")
	return players, nil
}

func extractTeamPlayers(players map[int64]PartialPlayer, stats *nhl.TeamPlayerStats, teamID int64) {
	// Forwards
	for _, s := range stats.Forwards {
		addSkaterPlayer(players, s, teamID)
	}
	// Defense
	for _, s := range stats.Defense {
		addSkaterPlayer(players, s, teamID)
	}
	// Goalies
	for _, g := range stats.Goalies {
		addGoaliePlayer(players, g, teamID)
	}
}

func addSkaterPlayer(players map[int64]PartialPlayer, s nhl.SkaterStats, teamID int64) {
	id := s.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(s.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(s.Position),
		SweaterNumber:   s.SweaterNumber,
		NHLTeamID:       teamID,
		HasBoxscoreData: true,
	}
}

func addGoaliePlayer(players map[int64]PartialPlayer, g nhl.GoalieStats, teamID int64) {
	id := g.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(g.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(g.Position),
		SweaterNumber:   g.SweaterNumber,
		NHLTeamID:       teamID,
		HasBoxscoreData: true,
	}
}

// parseLocalizedName splits "FirstName LastName" from nhl.LocalizedString.
func parseLocalizedName(name nhl.LocalizedString) (first, last string) {
	parts := strings.SplitN(name.Default, " ", 2)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return name.Default, ""
}

// ExtractBoxscorePlayersForDayBatchActivity extracts players from boxscore files for a batch of days.
// Results are stored in Redis to avoid workflow history size limits.
// Returns a BatchResult with the Redis key and player count.
func ExtractBoxscorePlayersForDayBatchActivity(ctx context.Context, input BatchInput) (BatchResult, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return extractBoxscorePlayersForDayBatchImpl(ctx, redisClient, input)
}

// extractBoxscorePlayersForDayBatchImpl is the testable implementation.
func extractBoxscorePlayersForDayBatchImpl(
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
		Msg("Boxscore batch: starting")

	for i, day := range input.Days {
		select {
		case <-ctx.Done():
			return BatchResult{}, ctx.Err()
		default:
		}

		dayPlayers, err := ExtractBoxscorePlayersForDayActivity(ctx, day)
		if err != nil {
			log.Warn().Err(err).Time("day", day).Msg("Failed to extract boxscore players for day, continuing")
			continue
		}

		// Merge players, keeping first occurrence with boxscore data
		for id, p := range dayPlayers {
			if existing, ok := allPlayers[id]; !ok || !existing.HasBoxscoreData {
				allPlayers[id] = p
			}
		}

		// Log progress every 10 days or on last day
		if (i+1)%10 == 0 || i == len(input.Days)-1 {
			log.Info().
				Int("processed", i+1).
				Int("total", len(input.Days)).
				Int("players_found", len(allPlayers)).
				Msg("Boxscore batch: progress")
		}
	}

	// Encode with gob
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(allPlayers); err != nil {
		return BatchResult{}, err
	}

	// Save to Redis
	redisKey := redis.PlayerBatchKey("boxscore", input.Season, input.BatchIndex)
	if err := redis.SavePlayerBatch(ctx, redisClient, redisKey, buf.Bytes()); err != nil {
		return BatchResult{}, err
	}

	log.Info().
		Int("days", len(input.Days)).
		Int("players", len(allPlayers)).
		Str("redis_key", redisKey).
		Msg("Boxscore batch: complete")

	return BatchResult{
		RedisKey:    redisKey,
		PlayerCount: len(allPlayers),
	}, nil
}

// getBoxscoreFilesForDay returns boxscore files for a specific day.
func getBoxscoreFilesForDay(fs cache.FileSystem, day time.Time) ([]cache.File, error) {
	// First get the daily schedule to know which game IDs exist
	scheduleFile := fs.New(cache.DailyScheduleFileType, day)
	if !fs.Exists(scheduleFile) {
		return nil, nil
	}

	gameIDs, err := cache.ParseDailySchedule(fs, scheduleFile)
	if err != nil {
		return nil, err
	}

	files := make([]cache.File, 0, len(gameIDs))
	for _, id := range gameIDs {
		file := fs.New(cache.BoxscoreFileType, day, id)
		files = append(files, file)
	}

	return files, nil
}

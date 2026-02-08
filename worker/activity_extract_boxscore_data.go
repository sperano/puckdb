package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/redis"
)

// BoxscoreExtractionResult contains player IDs extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	PlayerIDs []int64 `json:"playerIds"`
}

// ExtractBoxscoreDataForSeasonActivity extracts player IDs from all boxscores for a season.
func ExtractBoxscoreDataForSeasonActivity(ctx context.Context, season SeasonInfo) (BoxscoreExtractionResult, error) {
	return extractBoxscoreDataForSeasonImpl(ctx, cache.NewSimpleCache(), redis.NewClient(), season)
}

func extractBoxscoreDataForSeasonImpl(
	ctx context.Context,
	fs cache.FileSystem,
	redisClient redis.Client,
	season SeasonInfo,
) (BoxscoreExtractionResult, error) {
	playerIDs := make(map[int64]struct{})

	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	dayCount := 0
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return BoxscoreExtractionResult{}, ctx.Err()
		default:
		}

		dayPlayerIDs, err := extractPlayerIDsForDay(ctx, fs, redisClient, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract boxscore data for day")
			continue
		}

		for _, id := range dayPlayerIDs {
			playerIDs[id] = struct{}{}
		}

		dayCount++
		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.StartYear).
				Int("days_processed", dayCount).
				Int("unique_players", len(playerIDs)).
				Msg("Season extraction progress")
		}
	}

	// Convert map to slice
	playerIDSlice := make([]int64, 0, len(playerIDs))
	for id := range playerIDs {
		playerIDSlice = append(playerIDSlice, id)
	}

	log.Info().
		Int("season", season.StartYear).
		Int("days_processed", dayCount).
		Int("unique_players", len(playerIDSlice)).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		PlayerIDs: playerIDSlice,
	}, nil
}

// extractPlayerIDsForDay extracts player IDs from all boxscores for a single day.
func extractPlayerIDsForDay(
	ctx context.Context,
	fs cache.FileSystem,
	redisClient redis.Client,
	day time.Time,
) ([]int64, error) {
	boxscoreFiles, err := getBoxscoreFilesForDay(ctx, fs, redisClient, day)
	if err != nil {
		return nil, err
	}

	var playerIDs []int64

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
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(content, &boxscore); err != nil {
			continue
		}

		// Extract player IDs from both teams
		playerIDs = append(playerIDs, extractTeamPlayerIDs(&boxscore.PlayerByGameStats.HomeTeam)...)
		playerIDs = append(playerIDs, extractTeamPlayerIDs(&boxscore.PlayerByGameStats.AwayTeam)...)
	}

	return playerIDs, nil
}

// extractTeamPlayerIDs extracts all player IDs from a team's player stats.
func extractTeamPlayerIDs(stats *nhl.TeamPlayerStats) []int64 {
	var ids []int64

	for _, s := range stats.Forwards {
		ids = append(ids, s.PlayerID.AsInt64())
	}
	for _, s := range stats.Defense {
		ids = append(ids, s.PlayerID.AsInt64())
	}
	for _, g := range stats.Goalies {
		ids = append(ids, g.PlayerID.AsInt64())
	}

	return ids
}

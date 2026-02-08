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

// BoxscoreExtractionResult contains all data extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	PlayerIDs []int64         `json:"playerIds"`
	Teams     []ExtractedTeam `json:"teams"`
}

// ExtractBoxscoreDataForSeasonActivity extracts player IDs and teams from all boxscores for a season.
// This unified activity replaces separate player/team extraction, parsing each boxscore file once.
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
	teams := make(map[int64]ExtractedTeam)

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

		dayPlayerIDs, dayTeams, err := extractBoxscoreDataForDay(ctx, fs, redisClient, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract boxscore data for day")
			continue
		}

		for _, id := range dayPlayerIDs {
			playerIDs[id] = struct{}{}
		}
		for id, team := range dayTeams {
			teams[id] = team
		}

		dayCount++
		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.StartYear).
				Int("days_processed", dayCount).
				Int("unique_players", len(playerIDs)).
				Int("unique_teams", len(teams)).
				Msg("Season extraction progress")
		}
	}

	// Convert maps to slices
	playerIDSlice := make([]int64, 0, len(playerIDs))
	for id := range playerIDs {
		playerIDSlice = append(playerIDSlice, id)
	}

	teamSlice := make([]ExtractedTeam, 0, len(teams))
	for _, team := range teams {
		teamSlice = append(teamSlice, team)
	}

	log.Info().
		Int("season", season.StartYear).
		Int("days_processed", dayCount).
		Int("unique_players", len(playerIDSlice)).
		Int("unique_teams", len(teamSlice)).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		PlayerIDs: playerIDSlice,
		Teams:     teamSlice,
	}, nil
}

// extractBoxscoreDataForDay extracts player IDs and teams from all boxscores for a single day.
func extractBoxscoreDataForDay(
	ctx context.Context,
	fs cache.FileSystem,
	redisClient redis.Client,
	day time.Time,
) ([]int64, map[int64]ExtractedTeam, error) {
	boxscoreFiles, err := getBoxscoreFilesForDay(ctx, fs, redisClient, day)
	if err != nil {
		return nil, nil, err
	}

	var playerIDs []int64
	teams := make(map[int64]ExtractedTeam)

	for _, file := range boxscoreFiles {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
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

		// Extract team metadata from both teams
		homeTeam := extractTeamFromBoxscore(&boxscore.HomeTeam)
		awayTeam := extractTeamFromBoxscore(&boxscore.AwayTeam)
		teams[homeTeam.ID] = homeTeam
		teams[awayTeam.ID] = awayTeam
	}

	return playerIDs, teams, nil
}

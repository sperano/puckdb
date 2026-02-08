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

// ExtractedTeam holds team metadata extracted from a boxscore.
type ExtractedTeam struct {
	ID           int64  `json:"id"`
	Abbreviation string `json:"abbreviation"`
	City         string `json:"city"`
	Name         string `json:"name"`
}

// ExtractTeamsForSeasonsInput contains input for extracting teams across multiple seasons.
type ExtractTeamsForSeasonsInput struct {
	Seasons []SeasonInfo `json:"seasons"`
}

// ExtractTeamsForSeasonsActivity extracts all unique teams from boxscores across multiple seasons.
//
// Deprecated: Use ExtractBoxscoreDataForSeasonActivity instead, which extracts both player IDs
// and teams in a single pass through the boxscore files with season-level concurrency.
func ExtractTeamsForSeasonsActivity(ctx context.Context, input ExtractTeamsForSeasonsInput) ([]ExtractedTeam, error) {
	fs := cache.NewSimpleCache()
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return extractTeamsForSeasonsImpl(ctx, fs, redisClient, input.Seasons)
}

func extractTeamsForSeasonsImpl(ctx context.Context, fs cache.FileSystem, redisClient redis.Client, seasons []SeasonInfo) ([]ExtractedTeam, error) {
	teams := make(map[int64]ExtractedTeam)

	for _, season := range seasons {
		seasonTeams, err := extractTeamsForSeason(ctx, fs, redisClient, season)
		if err != nil {
			return nil, err
		}

		for id, team := range seasonTeams {
			teams[id] = team
		}

		log.Info().
			Int("season", season.StartYear).
			Int("unique_teams_so_far", len(teams)).
			Msg("Season team extraction complete")
	}

	result := make([]ExtractedTeam, 0, len(teams))
	for _, team := range teams {
		result = append(result, team)
	}

	log.Info().
		Int("total_unique_teams", len(result)).
		Msg("All seasons team extraction complete")

	return result, nil
}

func extractTeamsForSeason(ctx context.Context, fs cache.FileSystem, redisClient redis.Client, season SeasonInfo) (map[int64]ExtractedTeam, error) {
	teams := make(map[int64]ExtractedTeam)

	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	dayCount := 0
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dayTeams, err := extractTeamsForDay(ctx, fs, redisClient, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract teams for day")
			continue
		}

		for id, team := range dayTeams {
			teams[id] = team
		}

		dayCount++
		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.StartYear).
				Int("days_processed", dayCount).
				Int("unique_teams", len(teams)).
				Msg("Season team extraction progress")
		}
	}

	return teams, nil
}

func extractTeamsForDay(ctx context.Context, fs cache.FileSystem, redisClient redis.Client, day time.Time) (map[int64]ExtractedTeam, error) {
	teams := make(map[int64]ExtractedTeam)

	boxscoreFiles, err := getBoxscoreFilesForDay(ctx, fs, redisClient, day)
	if err != nil {
		return nil, err
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
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(content, &boxscore); err != nil {
			continue
		}

		homeTeam := extractTeamFromBoxscore(&boxscore.HomeTeam)
		awayTeam := extractTeamFromBoxscore(&boxscore.AwayTeam)

		teams[homeTeam.ID] = homeTeam
		teams[awayTeam.ID] = awayTeam
	}

	return teams, nil
}

func extractTeamFromBoxscore(bt *nhl.BoxscoreTeam) ExtractedTeam {
	return ExtractedTeam{
		ID:           int64(bt.ID),
		Abbreviation: bt.Abbrev,
		City:         bt.PlaceName.Default,
		Name:         bt.CommonName.Default,
	}
}

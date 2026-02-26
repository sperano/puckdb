package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

// BoxscoreExtractionResult contains players extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	Players []store.BoxscorePlayer `json:"players"`
}

// dayPlayerExtractor extracts players for a single day.
type dayPlayerExtractor func(ctx context.Context, day time.Time) ([]store.BoxscorePlayer, error)

// ExtractBoxscoreDataForSeasonActivity extracts player info from all boxscores for a season.
func ExtractBoxscoreDataForSeasonActivity(ctx context.Context, season SeasonInfo) (BoxscoreExtractionResult, error) {
	repos := store.NewDefaultRepos()
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	extractor := func(ctx context.Context, day time.Time) ([]store.BoxscorePlayer, error) {
		return extractPlayersForDay(ctx, repos, day)
	}
	return extractBoxscoreDataForSeasonImpl(ctx, extractor, redisClient, season)
}

func extractBoxscoreDataForSeasonImpl(
	ctx context.Context,
	extractForDay dayPlayerExtractor,
	redisClient cache.Client,
	season SeasonInfo,
) (BoxscoreExtractionResult, error) {
	// Map by player ID to deduplicate while preserving player info
	players := make(map[int64]store.BoxscorePlayer)

	end := effectiveEndDate(season.EndDate)

	// Calculate total days for progress tracking
	totalDays := countDays(season.StartDate, end)

	dayCount := 0
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return BoxscoreExtractionResult{}, ctx.Err()
		default:
		}

		dayPlayers, err := extractForDay(ctx, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract boxscore data for day")
			continue
		}

		for _, p := range dayPlayers {
			players[p.ID] = p
		}

		dayCount++

		// Fire-and-forget progress update to Redis (skip if no client)
		if redisClient != nil {
			workflowID := WorkflowIDExtractSeason(season.StartYear())
			_ = cache.SaveProgress(ctx, redisClient, workflowID, dayCount, totalDays)
		}

		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.StartYear()).
				Int("days_processed", dayCount).
				Int("unique_players", len(players)).
				Msg("Season extraction progress")
		}
	}

	// Convert map to slice
	playerSlice := make([]store.BoxscorePlayer, 0, len(players))
	for _, p := range players {
		playerSlice = append(playerSlice, p)
	}

	log.Info().
		Int("season", season.StartYear()).
		Int("days_processed", dayCount).
		Int("unique_players", len(playerSlice)).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		Players: playerSlice,
	}, nil
}

// extractPlayersForDay extracts player info from all boxscores for a single day.
func extractPlayersForDay(
	ctx context.Context,
	repos *store.Repos,
	day time.Time,
) ([]store.BoxscorePlayer, error) {
	// Get schedule for the day to find game IDs
	if !repos.Schedule.Exists(day) {
		return nil, nil
	}

	schedule, err := repos.Schedule.Get(day)
	if err != nil {
		return nil, err
	}

	var players []store.BoxscorePlayer

	for _, game := range schedule.Games {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if !repos.Boxscore.Exists(day, game.ID) {
			continue
		}

		boxscore, err := repos.Boxscore.Get(day, game.ID)
		if err != nil {
			continue
		}

		// Extract players from both teams
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.HomeTeam)...)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.AwayTeam)...)
	}

	return players, nil
}

// extractTeamPlayers extracts all players from a team's player stats.
func extractTeamPlayers(stats *nhl.TeamPlayerStats) []store.BoxscorePlayer {
	var players []store.BoxscorePlayer

	for _, s := range stats.Forwards {
		first, last := store.ParseCombinedName(s.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        s.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, s := range stats.Defense {
		first, last := store.ParseCombinedName(s.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        s.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, g := range stats.Goalies {
		first, last := store.ParseCombinedName(g.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        g.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(g.Position),
		})
	}

	return players
}

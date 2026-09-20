package nhl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/activity"
)

// BoxscoreActivities contains activities for extracting player data from boxscore files.
type BoxscoreActivities struct {
	Storage     store.Storage
	GobCache    *cache.GobCache
	RedisClient *redis.Client
}

// BoxscoreExtractionResult contains players extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	Players []store.BoxscorePlayer `json:"players"`
	Origins core.OriginCounts      `json:"origins"`
}

// ExtractAndSaveInput contains parameters for the ExtractAndSaveBoxscorePlayers activity.
type ExtractAndSaveInput struct {
	Season  nhlapi.SeasonInfo
	EndDate time.Time // Pre-computed by workflow using workflow.Now() for determinism
}

// workflowIDExtractSeason returns the workflow ID for a single season extraction.
// Used as Redis key prefix for progress tracking.
func workflowIDExtractSeason(startYear int) string {
	return fmt.Sprintf("extract-season-%d", startYear)
}

// ExtractBoxscoreInput contains parameters for the ExtractBoxscoreDataForSeason activity.
type ExtractBoxscoreInput struct {
	Season  nhlapi.SeasonInfo
	EndDate time.Time // Pre-computed by workflow using workflow.Now() for determinism
}

// ExtractBoxscoreDataForSeason extracts player info from all boxscores for a season.
func (a *BoxscoreActivities) ExtractBoxscoreDataForSeason(ctx context.Context, input ExtractBoxscoreInput) (BoxscoreExtractionResult, error) {
	startedAt := time.Now().UnixMilli()
	players := make(map[int64]store.BoxscorePlayer)
	origins := make(core.OriginCounts)

	season := input.Season
	end := input.EndDate
	totalDays := core.CountDays(season.StandingsStart.Time, end)

	dayCount := 0
	for day := season.StandingsStart.Time; !day.After(end); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return BoxscoreExtractionResult{}, ctx.Err()
		default:
		}

		dayPlayers, dayCounts, err := a.extractPlayersForDay(ctx, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract boxscore data for day")
			continue
		}

		for _, p := range dayPlayers {
			players[p.ID] = p
		}
		origins.Add(dayCounts)

		dayCount++

		activity.RecordHeartbeat(ctx, dayCount)

		if a.RedisClient != nil {
			workflowID := workflowIDExtractSeason(season.ID.StartYear())
			if err := shared.SaveActivityProgress(ctx, a.RedisClient, workflowID, startedAt, dayCount, totalDays); err != nil {
				log.Warn().Err(err).Str("workflow", workflowID).Msg("Failed to save activity progress")
			}
		}

		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.ID.StartYear()).
				Int("days_processed", dayCount).
				Int("unique_players", len(players)).
				Msg("Season extraction progress")
		}
	}

	// Extract players from playoff boxscores (catches playoff-only players)
	playoffPlayers, playoffOrigins, err := a.extractPlayoffPlayers(ctx, season.ID.StartYear())
	if err != nil {
		log.Warn().Err(err).Int("season", season.ID.StartYear()).Msg("Failed to extract playoff players")
	} else {
		for _, p := range playoffPlayers {
			players[p.ID] = p
		}
		origins.Add(playoffOrigins)
	}

	playerSlice := make([]store.BoxscorePlayer, 0, len(players))
	for _, p := range players {
		playerSlice = append(playerSlice, p)
	}

	log.Info().
		Int("season", season.ID.StartYear()).
		Int("days_processed", dayCount).
		Int("playoff_players_extracted", len(playoffPlayers)).
		Int("unique_players", len(playerSlice)).
		Str("origins", origins.Summary("reads")).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		Players: playerSlice,
		Origins: origins,
	}, nil
}

// ExtractAndSaveBoxscorePlayers extracts players from boxscores for a season and saves them to Redis.
func (a *BoxscoreActivities) ExtractAndSaveBoxscorePlayers(ctx context.Context, input ExtractAndSaveInput) (core.OriginCounts, error) {
	result, err := a.ExtractBoxscoreDataForSeason(ctx, ExtractBoxscoreInput(input))
	if err != nil {
		return nil, fmt.Errorf("extract boxscore data: %w", err)
	}

	ttl := time.Duration(viper.GetInt(config.FlagBoxscorePlayerCacheTTL)) * time.Minute
	if err := cache.SaveBoxscorePlayers(ctx, a.RedisClient, input.Season.ID, result.Players, ttl); err != nil {
		return nil, fmt.Errorf("save boxscore players to redis: %w", err)
	}

	return result.Origins, nil
}

// extractPlayoffPlayers extracts players from all playoff boxscores for a season.
// This catches players who only appeared in playoff games and would otherwise be
// missing from the players table when ImportPlayoffGames runs.
func (a *BoxscoreActivities) extractPlayoffPlayers(ctx context.Context, season int) ([]store.BoxscorePlayer, core.OriginCounts, error) {
	counts := make(core.OriginCounts)

	playoffGames, err := collectPlayoffGames(ctx, a.Storage, a.GobCache, season)
	if err != nil {
		return nil, counts, fmt.Errorf("collect playoff games: %w", err)
	}

	var players []store.BoxscorePlayer
	for _, pg := range playoffGames {
		select {
		case <-ctx.Done():
			return nil, counts, ctx.Err()
		default:
		}

		boxscoreRes := resource.Boxscore{Date: pg.Date, GameID: pg.ID}
		boxscore, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, boxscoreRes)
		if err != nil {
			continue
		}
		counts.Record(origin)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.HomeTeam)...)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.AwayTeam)...)
	}

	return players, counts, nil
}

// extractPlayersForDay extracts player info from all boxscores for a single day.
func (a *BoxscoreActivities) extractPlayersForDay(
	ctx context.Context,
	day time.Time,
) ([]store.BoxscorePlayer, core.OriginCounts, error) {
	counts := make(core.OriginCounts)

	scheduleRes := resource.DailySchedule{Date: day}
	schedule, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
	if err != nil {
		return nil, counts, nil // No schedule for this day
	}
	counts.Record(origin)

	var players []store.BoxscorePlayer

	for _, game := range schedule.Games {
		select {
		case <-ctx.Done():
			return nil, counts, ctx.Err()
		default:
		}
		boxscoreRes := resource.Boxscore{Date: day, GameID: game.ID}
		boxscore, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, boxscoreRes)
		if err != nil {
			continue
		}
		counts.Record(origin)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.HomeTeam)...)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.AwayTeam)...)
	}

	return players, counts, nil
}

// extractTeamPlayers extracts all players from a team's player stats.
func extractTeamPlayers(stats *nhlapi.TeamPlayerStats) []store.BoxscorePlayer {
	var players []store.BoxscorePlayer

	for _, s := range stats.Forwards {
		first, last := store.ParseCombinedName(s.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        s.PlayerID.Int64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, s := range stats.Defense {
		first, last := store.ParseCombinedName(s.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        s.PlayerID.Int64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, g := range stats.Goalies {
		first, last := store.ParseCombinedName(g.Name.String())
		players = append(players, store.BoxscorePlayer{
			ID:        g.PlayerID.Int64(),
			FirstName: first,
			LastName:  last,
			Position:  string(g.Position),
		})
	}

	return players
}

// addSkaterPlayer adds a skater to the players map.
func addSkaterPlayer(players map[int64]shared.PartialPlayer, s nhlapi.SkaterStats, teamID int64) {
	id := s.PlayerID.Int64()
	firstName, lastName := parseLocalizedName(s.Name)

	players[id] = shared.PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(s.Position),
		SweaterNumber:   s.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

// addGoaliePlayer adds a goalie to the players map.
func addGoaliePlayer(players map[int64]shared.PartialPlayer, g nhlapi.GoalieStats, teamID int64) {
	id := g.PlayerID.Int64()
	firstName, lastName := parseLocalizedName(g.Name)

	players[id] = shared.PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(g.Position),
		SweaterNumber:   g.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

// parseLocalizedName splits "FirstName LastName" from nhlapi.LocalizedString.
func parseLocalizedName(name nhlapi.LocalizedString) (first, last string) {
	parts := strings.SplitN(name.Default, " ", 2)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return name.Default, ""
}

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/activity"
)

// BoxscoreActivities contains activities for extracting player data from boxscore files.
type BoxscoreActivities struct {
	Storage     store.Storage
	GobCache    *cache.GobCache
	RedisClient cache.Client
}

// BoxscoreExtractionResult contains players extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	Players []store.BoxscorePlayer `json:"players"`
	Origins core.OriginCounts      `json:"origins"`
}

// ExtractAndSaveInput contains parameters for the ExtractAndSaveBoxscorePlayers activity.
type ExtractAndSaveInput struct {
	Season nhl.SeasonInfo
}

// ExtractBoxscoreDataForSeason extracts player info from all boxscores for a season.
func (a *BoxscoreActivities) ExtractBoxscoreDataForSeason(ctx context.Context, season nhl.SeasonInfo) (BoxscoreExtractionResult, error) {
	players := make(map[int64]store.BoxscorePlayer)
	origins := make(core.OriginCounts)

	end := effectiveEndDate(season.StandingsEnd.Time)
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

		// Fire-and-forget progress update to Redis (skip if no client)
		if a.RedisClient != nil {
			workflowID := WorkflowIDExtractSeason(season.ID.StartYear())
			_ = SaveActivityProgress(ctx, a.RedisClient, workflowID, dayCount, totalDays)
		}

		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.ID.StartYear()).
				Int("days_processed", dayCount).
				Int("unique_players", len(players)).
				Msg("Season extraction progress")
		}
	}

	playerSlice := make([]store.BoxscorePlayer, 0, len(players))
	for _, p := range players {
		playerSlice = append(playerSlice, p)
	}

	log.Info().
		Int("season", season.ID.StartYear()).
		Int("days_processed", dayCount).
		Int("unique_players", len(playerSlice)).
		Str("origins", origins.Summary("reads")).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		Players: playerSlice,
		Origins: origins,
	}, nil
}

// ExtractAndSaveBoxscorePlayers extracts players from boxscores for a season and saves them to Redis.
// Returns the origin counts from the extraction for upstream aggregation.
func (a *BoxscoreActivities) ExtractAndSaveBoxscorePlayers(ctx context.Context, input ExtractAndSaveInput) (core.OriginCounts, error) {
	result, err := a.ExtractBoxscoreDataForSeason(ctx, input.Season)
	if err != nil {
		return nil, fmt.Errorf("extract boxscore data: %w", err)
	}

	ttl := time.Duration(viper.GetInt(config.FlagBoxscorePlayerCacheTTL)) * time.Minute
	if err := cache.SaveBoxscorePlayers(ctx, a.RedisClient, input.Season.ID, result.Players, ttl); err != nil {
		return nil, fmt.Errorf("save boxscore players to redis: %w", err)
	}

	return result.Origins, nil
}

// extractPlayersForDay extracts player info from all boxscores for a single day,
// using the gob cache for schedule and boxscore reads.
func (a *BoxscoreActivities) extractPlayersForDay(
	ctx context.Context,
	day time.Time,
) ([]store.BoxscorePlayer, core.OriginCounts, error) {
	counts := make(core.OriginCounts)

	scheduleRes := resource.DailySchedule{Date: day}
	schedule, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
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
		boxscore, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, boxscoreRes)
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

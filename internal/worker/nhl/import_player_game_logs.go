package nhl

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

const regularSeasonGameType = 2

// ImportPlayerGameLogsBatchInput contains parameters for importing player game logs.
type ImportPlayerGameLogsBatchInput struct {
	Season    nhlapi.Season `json:"season"`
	PlayerIDs []int64       `json:"playerIDs"`
}

// ImportPlayerGameLogsBatchResult contains results of importing player game logs.
type ImportPlayerGameLogsBatchResult struct {
	PlayersProcessed int               `json:"playersProcessed"`
	GamesUpdated     int               `json:"gamesUpdated"`
	Origins          core.OriginCounts `json:"origins"`
}

// ImportPlayerGameLogsBatch imports game log stats (PPP, GWG, OT goals) for a batch of players.
// These stats are available in the player game log API but not in boxscores.
func (a *ImportActivities) ImportPlayerGameLogsBatch(ctx context.Context, input ImportPlayerGameLogsBatchInput) (*ImportPlayerGameLogsBatchResult, error) {
	defer metrics.TrackActivityDuration("ImportPlayerGameLogsBatch")()

	logger := activity.GetLogger(ctx)
	result := &ImportPlayerGameLogsBatchResult{Origins: core.OriginCounts{}}

	for _, playerID := range input.PlayerIDs {
		gameLogRes := resource.PlayerGameLog{
			PlayerID: nhlapi.NewPlayerID(playerID),
			Season:   input.Season,
			GameType: regularSeasonGameType,
		}

		if !a.Storage.Exists(ctx, gameLogRes.Path()) {
			continue
		}

		gameLog, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, gameLogRes)
		if err != nil {
			log.Debug().Err(err).Int64("playerID", playerID).Msg("Failed to read player game log")
			continue
		}
		result.Origins.Record(origin)

		updated, err := importPlayerGameLog(ctx, a.Queries, playerID, gameLog)
		if err != nil {
			return result, fmt.Errorf("import game log for player %d: %w", playerID, err)
		}
		result.GamesUpdated += updated
		result.PlayersProcessed++
	}

	logger.Info("Imported player game logs batch",
		"players", result.PlayersProcessed,
		"games", result.GamesUpdated)

	return result, nil
}

// importPlayerGameLog updates game stats for a single player from their game log.
func importPlayerGameLog(ctx context.Context, queries PlayerGameLogUpdater, playerID int64, gameLog *nhlapi.PlayerGameLog) (int, error) {
	updated := 0
	for _, entry := range gameLog.GameLog {
		ppp := int16(entry.PowerPlayPoints)
		var gwg, otg int16
		if entry.GameWinningGoals != nil {
			gwg = int16(*entry.GameWinningGoals)
		}
		if entry.OTGoals != nil {
			otg = int16(*entry.OTGoals)
		}

		if ppp == 0 && gwg == 0 && otg == 0 {
			continue
		}

		if err := queries.UpdateSkaterGameLogStats(ctx, sqlcdb.UpdateSkaterGameLogStatsParams{
			GameID:           int64(entry.GameID),
			PlayerID:         playerID,
			PowerPlayPoints:  ppp,
			GameWinningGoals: gwg,
			OtGoals:          otg,
		}); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

// CollectSeasonPlayerIDsInput contains parameters for the CollectSeasonPlayerIDs activity.
type CollectSeasonPlayerIDsInput struct {
	Season  nhlapi.SeasonInfo
	EndDate time.Time // Pre-computed by workflow using workflow.Now() for determinism
}

// CollectSeasonPlayerIDs reads all boxscores for a season and returns unique skater player IDs.
func (a *ImportActivities) CollectSeasonPlayerIDs(ctx context.Context, input CollectSeasonPlayerIDsInput) ([]int64, error) {
	defer metrics.TrackActivityDuration("CollectSeasonPlayerIDs")()

	seen := make(map[int64]struct{})

	season := input.Season
	endDate := input.EndDate
	for d := season.StandingsStart.Time; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		scheduleRes := resource.DailySchedule{Date: d}
		if !a.Storage.Exists(ctx, scheduleRes.Path()) {
			continue
		}

		schedule, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
		if err != nil {
			continue
		}

		for _, game := range schedule.Games {
			if shouldSkipGame(game) {
				continue
			}

			boxscoreRes := resource.Boxscore{Date: d, GameID: game.ID}
			if !a.Storage.Exists(ctx, boxscoreRes.Path()) {
				continue
			}

			boxscore, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, boxscoreRes)
			if err != nil {
				continue
			}

			collectPlayerIDs(boxscore, seen)
		}

		activity.RecordHeartbeat(ctx, d.Format(config.DateFormat))
	}

	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

// collectPlayerIDs extracts unique skater player IDs from a boxscore.
func collectPlayerIDs(b *nhlapi.Boxscore, seen map[int64]struct{}) {
	for _, teams := range []nhlapi.TeamPlayerStats{b.PlayerByGameStats.AwayTeam, b.PlayerByGameStats.HomeTeam} {
		for _, s := range teams.Forwards {
			seen[int64(s.PlayerID)] = struct{}{}
		}
		for _, s := range teams.Defense {
			seen[int64(s.PlayerID)] = struct{}{}
		}
	}
}

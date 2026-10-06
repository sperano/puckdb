package nhl

import (
	"context"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportClubStats imports cached club stats for all teams into the database.
func (a *SeasonsActivities) ImportClubStats(ctx context.Context, input FetchClubStatsInput) error {
	return a.importClubStats(ctx, a.ClubStatsQueries, input)
}

// importClubStats contains the core club stats import logic, accepting a narrow
// interface to allow testing without a full database connection.
func (a *SeasonsActivities) importClubStats(ctx context.Context, queries ClubStatsUpserter, input FetchClubStatsInput) error {
	logger := activity.GetLogger(ctx)

	season := nhlapi.NewSeason(input.Season)
	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	if len(teams) == 0 {
		logger.Warn("No teams found for season", "season", input.Season, "seasonID", season.ID())
	}

	var totalSkaters, totalGoalies int
	for _, team := range teams {
		for _, gameType := range gameTypesToFetch {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("import-clubstats:%s:%d", team.Abbrev, gameType))

			res := resource.ClubStatsResource{
				Season:     input.Season,
				TeamAbbrev: team.Abbrev,
				GameType:   gameType.Int(),
			}
			if !resource.Exists(ctx, a.Storage, res) {
				continue
			}

			stats, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
			if err != nil {
				return fmt.Errorf("read club stats cache for %s game type %d: %w", team.Abbrev, gameType, err)
			}

			if err := importClubSkaterStats(ctx, queries, stats, team, season.ID(), gameType); err != nil {
				return fmt.Errorf("import skater stats for %s game type %d: %w", team.Abbrev, gameType, err)
			}
			totalSkaters += len(stats.Skaters)

			if err := importClubGoalieStats(ctx, queries, stats, team, season.ID(), gameType); err != nil {
				return fmt.Errorf("import goalie stats for %s game type %d: %w", team.Abbrev, gameType, err)
			}
			totalGoalies += len(stats.Goalies)
		}
	}

	logger.Info("Imported club stats",
		"season", input.Season,
		"teams", len(teams),
		"skaters", totalSkaters,
		"goalies", totalGoalies)
	return nil
}

func importClubSkaterStats(
	ctx context.Context,
	queries ClubStatsUpserter,
	stats *nhlapi.ClubStats,
	team sqlcdb.GetSeasonTeamAbbrevsRow,
	season int,
	gameType nhlapi.GameType,
) error {
	if len(stats.Skaters) == 0 {
		return nil
	}

	params := make([]sqlcdb.UpsertClubSkaterStatsBatchParams, len(stats.Skaters))
	for i, s := range stats.Skaters {
		params[i] = sqlcdb.UpsertClubSkaterStatsBatchParams{
			Season:           int32(season),
			GameType:         sqlcdb.GameType(gameType.Label()),
			TeamID:           team.TeamID,
			PlayerID:         int64(s.PlayerID),
			GamesPlayed:      int32(s.GamesPlayed),
			Goals:            int32(s.Goals),
			Assists:          int32(s.Assists),
			Points:           int32(s.Points),
			PlusMinus:        int32(s.PlusMinus),
			PenaltyMinutes:   int32(s.PenaltyMinutes),
			PowerPlayGoals:   int32(s.PowerPlayGoals),
			ShorthandedGoals: int32(s.ShorthandedGoals),
			GameWinningGoals: int32(s.GameWinningGoals),
			OvertimeGoals:    int32(s.OvertimeGoals),
			Shots:            int32(s.Shots),
			ShootingPctg:     float32(s.ShootingPctg),
			AvgToiPerGame:    float32(s.AvgTimeOnIcePerGame),
			AvgShiftsPerGame: float32(s.AvgShiftsPerGame),
			FaceoffWinPctg:   float32(s.FaceoffWinPctg),
		}
	}

	return shared.ExecBatch(queries.UpsertClubSkaterStatsBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("player %d (%s)", params[i].PlayerID, team.Abbrev)
	})
}

func importClubGoalieStats(
	ctx context.Context,
	queries ClubStatsUpserter,
	stats *nhlapi.ClubStats,
	team sqlcdb.GetSeasonTeamAbbrevsRow,
	season int,
	gameType nhlapi.GameType,
) error {
	if len(stats.Goalies) == 0 {
		return nil
	}

	params := make([]sqlcdb.UpsertClubGoalieStatsBatchParams, len(stats.Goalies))
	for i, g := range stats.Goalies {
		params[i] = sqlcdb.UpsertClubGoalieStatsBatchParams{
			Season:              int32(season),
			GameType:            sqlcdb.GameType(gameType.Label()),
			TeamID:              team.TeamID,
			PlayerID:            int64(g.PlayerID),
			GamesPlayed:         int32(g.GamesPlayed),
			GamesStarted:        int32(g.GamesStarted),
			Wins:                int32(g.Wins),
			Losses:              int32(g.Losses),
			OvertimeLosses:      int32(g.OvertimeLosses),
			GoalsAgainstAverage: float32(g.GoalsAgainstAverage),
			SavePercentage:      float32(g.SavePercentage),
			ShotsAgainst:        int32(g.ShotsAgainst),
			Saves:               int32(g.Saves),
			GoalsAgainst:        int32(g.GoalsAgainst),
			Shutouts:            int32(g.Shutouts),
			Goals:               int32(g.Goals),
			Assists:             int32(g.Assists),
			Points:              int32(g.Points),
			PenaltyMinutes:      int32(g.PenaltyMinutes),
			TOISeconds:          g.TimeOnIce,
		}
	}

	return shared.ExecBatch(queries.UpsertClubGoalieStatsBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("player %d (%s)", params[i].PlayerID, team.Abbrev)
	})
}

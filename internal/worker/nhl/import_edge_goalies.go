package nhl

import (
	"context"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// importEdgeGoalieDetail maps one goalie's Edge detail onto the edge_goalie_* tables.
func importEdgeGoalieDetail(ctx context.Context, queries EdgeStatsUpserter, detail *nhlapi.EdgeGoalieDetail, playerID int64, season int32, gameType sqlcdb.GameType) error {
	stats := detail.Stats

	// Upsert main stats
	if err := queries.UpsertEdgeGoalieStats(ctx, sqlcdb.UpsertEdgeGoalieStatsParams{
		PlayerID:                 playerID,
		Season:                   season,
		GameType:                 gameType,
		GaaValue:                 pf32(stats.GoalsAgainstAvg.Value),
		GaaPercentile:            pf32(stats.GoalsAgainstAvg.Percentile),
		GaaLeagueAvg:             pf32(stats.GoalsAgainstAvg.LeagueAvg),
		GamesAbove900Value:       pf32(stats.GamesAbove900.Value),
		GamesAbove900Percentile:  pf32(stats.GamesAbove900.Percentile),
		GamesAbove900LeagueAvg:   pf32(stats.GamesAbove900.LeagueAvg),
		GoalDiffPer60Value:       pf32(stats.GoalDifferentialPer60.Value),
		GoalDiffPer60Percentile:  pf32(stats.GoalDifferentialPer60.Percentile),
		GoalDiffPer60LeagueAvg:   pf32(stats.GoalDifferentialPer60.LeagueAvg),
		GoalSupportAvgValue:      pf32(stats.GoalSupportAvg.Value),
		GoalSupportAvgPercentile: pf32(stats.GoalSupportAvg.Percentile),
		GoalSupportAvgLeagueAvg:  pf32(stats.GoalSupportAvg.LeagueAvg),
		PointPctgValue:           pf32(stats.PointPctg.Value),
		PointPctgPercentile:      pf32(stats.PointPctg.Percentile),
		PointPctgLeagueAvg:       pf32(stats.PointPctg.LeagueAvg),
	}); err != nil {
		return fmt.Errorf("upsert stats: %w", err)
	}

	// Upsert shot location summary (ON CONFLICT handles race conditions from parallel activities)
	for _, loc := range detail.ShotLocationSummary {
		if err := queries.UpsertEdgeGoalieShotLocationSummary(ctx, sqlcdb.UpsertEdgeGoalieShotLocationSummaryParams{
			PlayerID:               playerID,
			Season:                 season,
			GameType:               gameType,
			LocationCode:           loc.LocationCode,
			GoalsAgainst:           pi32(loc.GoalsAgainst),
			GoalsAgainstPercentile: pf32(loc.GoalsAgainstPercentile),
			GoalsAgainstLeagueAvg:  pf32(loc.GoalsAgainstLeagueAvg),
			Saves:                  pi32(loc.Saves),
			SavesPercentile:        pf32(loc.SavesPercentile),
			SavesLeagueAvg:         pf32(loc.SavesLeagueAvg),
			SavePctg:               pf32(loc.SavePctg),
			SavePctgPercentile:     pf32(loc.SavePctgPercentile),
			SavePctgLeagueAvg:      pf32(loc.SavePctgLeagueAvg),
		}); err != nil {
			return fmt.Errorf("upsert shot location summary %s: %w", loc.LocationCode, err)
		}
	}

	// Upsert shot locations (ON CONFLICT handles race conditions from parallel activities)
	for _, loc := range detail.ShotLocationDetails {
		if err := queries.UpsertEdgeGoalieShotLocation(ctx, sqlcdb.UpsertEdgeGoalieShotLocationParams{
			PlayerID:           playerID,
			Season:             season,
			GameType:           gameType,
			Area:               loc.Area,
			Saves:              pi32(loc.Saves),
			SavesPercentile:    pf32(loc.SavesPercentile),
			SavePctg:           pf32(loc.SavePctg),
			SavePctgPercentile: pf32(loc.SavePctgPercentile),
		}); err != nil {
			return fmt.Errorf("upsert shot location %s: %w", loc.Area, err)
		}
	}

	return nil
}

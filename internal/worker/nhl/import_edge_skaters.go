package nhl

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// importEdgeSkaterDetail maps one skater's Edge detail onto the edge_skater_* tables.
func importEdgeSkaterDetail(ctx context.Context, queries EdgeStatsUpserter, detail *nhlapi.EdgeSkaterDetail, playerID int64, season int32, gameType sqlcdb.GameType) error {
	// Upsert main stats
	if err := queries.UpsertEdgeSkaterStats(ctx, sqlcdb.UpsertEdgeSkaterStatsParams{
		PlayerID:                      playerID,
		Season:                        season,
		GameType:                      gameType,
		TopSpeedImperial:              pf32(detail.SkatingSpeed.SpeedMax.Imperial),
		TopSpeedMetric:                pf32(detail.SkatingSpeed.SpeedMax.Metric),
		TopSpeedPercentile:            pf32(detail.SkatingSpeed.SpeedMax.Percentile),
		TopSpeedLeagueAvgImperial:     pf32(detail.SkatingSpeed.SpeedMax.LeagueAvg.Imperial),
		TopSpeedLeagueAvgMetric:       pf32(detail.SkatingSpeed.SpeedMax.LeagueAvg.Metric),
		BurstsOver20:                  pi32(detail.SkatingSpeed.BurstsOver20.Value),
		BurstsOver20Percentile:        pf32(detail.SkatingSpeed.BurstsOver20.Percentile),
		BurstsOver20LeagueAvg:         pf32(detail.SkatingSpeed.BurstsOver20.LeagueAvg.Value),
		TotalDistanceImperial:         pf32(detail.TotalDistanceSkated.Imperial),
		TotalDistanceMetric:           pf32(detail.TotalDistanceSkated.Metric),
		TotalDistancePercentile:       pf32(detail.TotalDistanceSkated.Percentile),
		MaxGameDistanceImperial:       pf32(detail.DistanceMaxGame.Imperial),
		MaxGameDistanceMetric:         pf32(detail.DistanceMaxGame.Metric),
		MaxGameDistancePercentile:     pf32(detail.DistanceMaxGame.Percentile),
		TopShotSpeedImperial:          pf32(detail.TopShotSpeed.Imperial),
		TopShotSpeedMetric:            pf32(detail.TopShotSpeed.Metric),
		TopShotSpeedPercentile:        pf32(detail.TopShotSpeed.Percentile),
		TopShotSpeedLeagueAvgImperial: pf32(detail.TopShotSpeed.LeagueAvg.Imperial),
		TopShotSpeedLeagueAvgMetric:   pf32(detail.TopShotSpeed.LeagueAvg.Metric),
		OzPctg:                        pf32(detail.ZoneTimeDetails.OffensiveZonePctg),
		OzPercentile:                  pf32(detail.ZoneTimeDetails.OffensiveZonePercentile),
		OzLeagueAvg:                   pf32(detail.ZoneTimeDetails.OffensiveZoneLeagueAvg),
		NzPctg:                        pf32(detail.ZoneTimeDetails.NeutralZonePctg),
		NzPercentile:                  pf32(detail.ZoneTimeDetails.NeutralZonePercentile),
		NzLeagueAvg:                   pf32(detail.ZoneTimeDetails.NeutralZoneLeagueAvg),
		DzPctg:                        pf32(detail.ZoneTimeDetails.DefensiveZonePctg),
		DzPercentile:                  pf32(detail.ZoneTimeDetails.DefensiveZonePercentile),
		DzLeagueAvg:                   pf32(detail.ZoneTimeDetails.DefensiveZoneLeagueAvg),
		OzEvPctg:                      pf32(detail.ZoneTimeDetails.OffensiveZoneEvPctg),
		OzEvPercentile:                pf32(detail.ZoneTimeDetails.OffensiveZoneEvPercentile),
	}); err != nil {
		return fmt.Errorf("upsert stats: %w", err)
	}

	// Upsert shot locations (ON CONFLICT handles race conditions from parallel activities)
	for _, loc := range detail.SogDetails {
		if err := queries.UpsertEdgeSkaterShotLocation(ctx, sqlcdb.UpsertEdgeSkaterShotLocationParams{
			PlayerID:               playerID,
			Season:                 season,
			GameType:               gameType,
			Area:                   loc.Area,
			SOG:                    pi32(loc.Shots),
			Goals:                  pgtype.Int4{}, // SogAreaDetail doesn't have goals
			ShootingPctg:           pf32(loc.ShootingPctg),
			SogPercentile:          pf32(loc.ShotsPercentile),
			GoalsPercentile:        pgtype.Float4{},
			ShootingPctgPercentile: pgtype.Float4{},
		}); err != nil {
			return fmt.Errorf("upsert shot location %s: %w", loc.Area, err)
		}
	}

	// Upsert SOG summary (ON CONFLICT handles race conditions from parallel activities)
	for _, sog := range detail.SogSummary {
		if err := queries.UpsertEdgeSkaterSogSummary(ctx, sqlcdb.UpsertEdgeSkaterSogSummaryParams{
			PlayerID:               playerID,
			Season:                 season,
			GameType:               gameType,
			LocationCode:           sog.LocationCode,
			Shots:                  pi32(sog.Shots),
			ShotsPercentile:        pf32(sog.ShotsPercentile),
			ShotsLeagueAvg:         pf32(sog.ShotsLeagueAvg),
			Goals:                  pi32(sog.Goals),
			GoalsPercentile:        pf32(sog.GoalsPercentile),
			GoalsLeagueAvg:         pf32(sog.GoalsLeagueAvg),
			ShootingPctg:           pf32(sog.ShootingPctg),
			ShootingPctgPercentile: pf32(sog.ShootingPctgPercentile),
			ShootingPctgLeagueAvg:  pf32(sog.ShootingPctgLeagueAvg),
		}); err != nil {
			return fmt.Errorf("upsert sog summary %s: %w", sog.LocationCode, err)
		}
	}

	return nil
}

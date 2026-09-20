package nhl

import (
	"context"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// importEdgeTeamDetail maps one team's Edge detail onto the edge_team_stats, SOG summary and shot location tables.
func importEdgeTeamDetail(ctx context.Context, queries EdgeStatsUpserter, detail *nhlapi.EdgeTeamDetail, teamID int64, season int32, gameType sqlcdb.GameType) error {
	// Upsert main stats
	if err := queries.UpsertEdgeTeamStats(ctx, sqlcdb.UpsertEdgeTeamStatsParams{
		TeamID:                 teamID,
		Season:                 season,
		GameType:               gameType,
		ShotAttemptsOver90:     pi32(detail.ShotSpeed.ShotAttemptsOver90.Value),
		ShotAttemptsOver90Rank: pi32(detail.ShotSpeed.ShotAttemptsOver90.Rank),
		TopShotSpeedImperial:   pf32(detail.ShotSpeed.TopShotSpeed.Imperial),
		TopShotSpeedMetric:     pf32(detail.ShotSpeed.TopShotSpeed.Metric),
		TopShotSpeedRank:       pi32(detail.ShotSpeed.TopShotSpeed.Rank),
		BurstsOver22:           pi32(detail.SkatingSpeed.BurstsOver22.Value),
		BurstsOver22Rank:       pi32(detail.SkatingSpeed.BurstsOver22.Rank),
		BurstsOver20:           pi32(detail.SkatingSpeed.BurstsOver20.Value),
		BurstsOver20Rank:       pi32(detail.SkatingSpeed.BurstsOver20.Rank),
		SpeedMaxImperial:       pf32(detail.SkatingSpeed.SpeedMax.Imperial),
		SpeedMaxMetric:         pf32(detail.SkatingSpeed.SpeedMax.Metric),
		SpeedMaxRank:           pi32(detail.SkatingSpeed.SpeedMax.Rank),
		TotalDistance:          pi32(detail.DistanceSkated.Total.Value),
		TotalDistanceRank:      pi32(detail.DistanceSkated.Total.Rank),
		OzPctg:                 pf32(detail.ZoneTimeDetails.OffensiveZonePctg),
		OzRank:                 pi32(detail.ZoneTimeDetails.OffensiveZoneRank),
		OzLeagueAvg:            pf32(detail.ZoneTimeDetails.OffensiveZoneLeagueAvg),
		OzEvPctg:               pf32(detail.ZoneTimeDetails.OffensiveZoneEvPctg),
		OzEvRank:               pi32(detail.ZoneTimeDetails.OffensiveZoneEvRank),
		NzPctg:                 pf32(detail.ZoneTimeDetails.NeutralZonePctg),
		NzRank:                 pi32(detail.ZoneTimeDetails.NeutralZoneRank),
		NzLeagueAvg:            pf32(detail.ZoneTimeDetails.NeutralZoneLeagueAvg),
		DzPctg:                 pf32(detail.ZoneTimeDetails.DefensiveZonePctg),
		DzRank:                 pi32(detail.ZoneTimeDetails.DefensiveZoneRank),
		DzLeagueAvg:            pf32(detail.ZoneTimeDetails.DefensiveZoneLeagueAvg),
	}); err != nil {
		return fmt.Errorf("upsert stats: %w", err)
	}

	// Upsert SOG summary
	for _, sog := range detail.SogSummary {
		if err := queries.UpsertEdgeTeamSogSummary(ctx, sqlcdb.UpsertEdgeTeamSogSummaryParams{
			TeamID:                teamID,
			Season:                season,
			GameType:              gameType,
			LocationCode:          sog.LocationCode,
			Shots:                 pi32(sog.Shots),
			ShotsRank:             pi32(sog.ShotsRank),
			ShotsLeagueAvg:        pf32(sog.ShotsLeagueAvg),
			Goals:                 pi32(sog.Goals),
			GoalsRank:             pi32(sog.GoalsRank),
			GoalsLeagueAvg:        pf32(sog.GoalsLeagueAvg),
			ShootingPctg:          pf32(sog.ShootingPctg),
			ShootingPctgRank:      pi32(sog.ShootingPctgRank),
			ShootingPctgLeagueAvg: pf32(sog.ShootingPctgLeagueAvg),
		}); err != nil {
			return fmt.Errorf("upsert sog summary %s: %w", sog.LocationCode, err)
		}
	}

	// Upsert shot locations
	for _, loc := range detail.SogDetails {
		if err := queries.UpsertEdgeTeamShotLocation(ctx, sqlcdb.UpsertEdgeTeamShotLocationParams{
			TeamID:    teamID,
			Season:    season,
			GameType:  gameType,
			Area:      loc.Area,
			Shots:     pi32(loc.Shots),
			ShotsRank: pi32(loc.ShotsRank),
		}); err != nil {
			return fmt.Errorf("upsert shot location %s: %w", loc.Area, err)
		}
	}

	return nil
}

// importEdgeTeamZoneTime maps one team's zone time details onto the zone-time-by-strength and shot-differential tables.
func importEdgeTeamZoneTime(ctx context.Context, queries EdgeStatsUpserter, detail *nhlapi.EdgeTeamZoneTimeDetails, teamID int64, season int32, gameType sqlcdb.GameType) error {
	// Upsert zone time by strength
	for _, zt := range detail.ZoneTimeDetails {
		if err := queries.UpsertEdgeTeamZoneTimeByStrength(ctx, sqlcdb.UpsertEdgeTeamZoneTimeByStrengthParams{
			TeamID:       teamID,
			Season:       season,
			GameType:     gameType,
			StrengthCode: zt.StrengthCode,
			OzPctg:       pf32(zt.OffensiveZonePctg),
			OzRank:       pi32(zt.OffensiveZoneRank),
			NzPctg:       pf32(zt.NeutralZonePctg),
			NzRank:       pi32(zt.NeutralZoneRank),
			DzPctg:       pf32(zt.DefensiveZonePctg),
			DzRank:       pi32(zt.DefensiveZoneRank),
		}); err != nil {
			return fmt.Errorf("upsert zone time by strength %s: %w", zt.StrengthCode, err)
		}
	}

	// Import shot differential (aggregate stats)
	if detail.ShotDifferential != nil {
		sd := detail.ShotDifferential
		if err := queries.UpsertEdgeTeamShotDifferential(ctx, sqlcdb.UpsertEdgeTeamShotDifferentialParams{
			TeamID:                      teamID,
			Season:                      season,
			GameType:                    gameType,
			ShotAttemptDifferential:     pf32(sd.ShotAttemptDifferential),
			ShotAttemptDifferentialRank: pi32(sd.ShotAttemptDifferentialRank),
			SogDifferential:             pf32(sd.SOGDifferential),
			SogDifferentialRank:         pi32(sd.SOGDifferentialRank),
		}); err != nil {
			return fmt.Errorf("upsert shot differential: %w", err)
		}
	}

	return nil
}

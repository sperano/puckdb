package nhl

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportEdgeTeamInput contains parameters for a single-team Edge import activity.
type ImportEdgeTeamInput struct {
	Season     int
	GameType   int
	TeamID     int64
	TeamAbbrev string
}

// EdgeStatsUpserter defines the interface for importing Edge stats to the database.
type EdgeStatsUpserter interface {
	// Skater (upsert via ON CONFLICT to handle parallel activities for traded players)
	UpsertEdgeSkaterStats(ctx context.Context, arg sqlcdb.UpsertEdgeSkaterStatsParams) error
	InsertEdgeSkaterShotLocation(ctx context.Context, arg sqlcdb.InsertEdgeSkaterShotLocationParams) error
	InsertEdgeSkaterSogSummary(ctx context.Context, arg sqlcdb.InsertEdgeSkaterSogSummaryParams) error

	// Goalie (upsert via ON CONFLICT to handle parallel activities for traded goalies)
	UpsertEdgeGoalieStats(ctx context.Context, arg sqlcdb.UpsertEdgeGoalieStatsParams) error
	InsertEdgeGoalieShotLocationSummary(ctx context.Context, arg sqlcdb.InsertEdgeGoalieShotLocationSummaryParams) error
	InsertEdgeGoalieShotLocation(ctx context.Context, arg sqlcdb.InsertEdgeGoalieShotLocationParams) error

	// Team (all upserts for consistency)
	UpsertEdgeTeamStats(ctx context.Context, arg sqlcdb.UpsertEdgeTeamStatsParams) error
	InsertEdgeTeamSogSummary(ctx context.Context, arg sqlcdb.InsertEdgeTeamSogSummaryParams) error
	InsertEdgeTeamShotLocation(ctx context.Context, arg sqlcdb.InsertEdgeTeamShotLocationParams) error
	InsertEdgeTeamZoneTimeByStrength(ctx context.Context, arg sqlcdb.InsertEdgeTeamZoneTimeByStrengthParams) error
	UpsertEdgeTeamShotDifferential(ctx context.Context, arg sqlcdb.UpsertEdgeTeamShotDifferentialParams) error

	// Shared
	GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
}

// ImportEdgeSkaters reads cached Edge skater detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeSkaters(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeSkaters(ctx, a.EdgeQueries, input)
}

func (a *SeasonsActivities) importEdgeSkaters(ctx context.Context, queries EdgeStatsUpserter, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var imported int
	for _, team := range teams {
		roster, err := a.loadSeasonRoster(ctx, team.Abbrev, season)
		if err != nil {
			continue
		}

		skaters := append(roster.Forwards, roster.Defensemen...)
		for _, player := range skaters {
			playerID := player.ID
			detailRes := resource.EdgeSkaterDetail{PlayerID: playerID, Season: season, GameType: gameType}
			if !a.Storage.Exists(detailRes.Path()) {
				continue
			}

			activity.RecordHeartbeat(ctx, fmt.Sprintf("import-edge-skater:%s:%d", team.Abbrev, playerID))

			detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
			if err != nil {
				logger.Warn("Failed to read edge skater detail from cache", "player", playerID, "err", err)
				continue
			}

			if err := importEdgeSkaterDetail(ctx, queries, detail, int64(playerID), int32(season.ID()), gtLabel); err != nil {
				return fmt.Errorf("import edge skater %d: %w", playerID, err)
			}
			imported++
		}
	}

	logger.Info("Imported edge skater stats", "season", input.Season, "skaters", imported)
	return nil
}

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
		if err := queries.InsertEdgeSkaterShotLocation(ctx, sqlcdb.InsertEdgeSkaterShotLocationParams{
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
		if err := queries.InsertEdgeSkaterSogSummary(ctx, sqlcdb.InsertEdgeSkaterSogSummaryParams{
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

// ImportEdgeGoalies reads cached Edge goalie detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeGoalies(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeGoalies(ctx, a.EdgeQueries, input)
}

func (a *SeasonsActivities) importEdgeGoalies(ctx context.Context, queries EdgeStatsUpserter, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var imported int
	for _, team := range teams {
		roster, err := a.loadSeasonRoster(ctx, team.Abbrev, season)
		if err != nil {
			continue
		}

		for _, goalie := range roster.Goalies {
			goalieID := goalie.ID
			detailRes := resource.EdgeGoalieDetail{GoalieID: goalieID, Season: season, GameType: gameType}
			if !a.Storage.Exists(detailRes.Path()) {
				continue
			}

			activity.RecordHeartbeat(ctx, fmt.Sprintf("import-edge-goalie:%s:%d", team.Abbrev, goalieID))

			detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
			if err != nil {
				logger.Warn("Failed to read edge goalie detail from cache", "goalie", goalieID, "err", err)
				continue
			}

			if err := importEdgeGoalieDetail(ctx, queries, detail, int64(goalieID), int32(season.ID()), gtLabel); err != nil {
				return fmt.Errorf("import edge goalie %d: %w", goalieID, err)
			}
			imported++
		}
	}

	logger.Info("Imported edge goalie stats", "season", input.Season, "goalies", imported)
	return nil
}

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
		if err := queries.InsertEdgeGoalieShotLocationSummary(ctx, sqlcdb.InsertEdgeGoalieShotLocationSummaryParams{
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
		if err := queries.InsertEdgeGoalieShotLocation(ctx, sqlcdb.InsertEdgeGoalieShotLocationParams{
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

// ImportEdgeTeams reads cached Edge team detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeTeams(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeTeams(ctx, a.EdgeQueries, input)
}

func (a *SeasonsActivities) importEdgeTeams(ctx context.Context, queries EdgeStatsUpserter, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var imported int
	for _, team := range teams {
		teamID := nhlapi.TeamID(team.TeamID)
		detailRes := resource.EdgeTeamDetail{TeamID: teamID, Season: season, GameType: gameType}
		if !a.Storage.Exists(detailRes.Path()) {
			continue
		}

		activity.RecordHeartbeat(ctx, fmt.Sprintf("import-edge-team:%s", team.Abbrev))

		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
		if err != nil {
			logger.Warn("Failed to read edge team detail from cache", "team", team.Abbrev, "err", err)
			continue
		}

		if err := importEdgeTeamDetail(ctx, queries, detail, int64(teamID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge team %s: %w", team.Abbrev, err)
		}
		imported++
	}

	logger.Info("Imported edge team stats", "season", input.Season, "teams", imported)
	return nil
}

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
		if err := queries.InsertEdgeTeamSogSummary(ctx, sqlcdb.InsertEdgeTeamSogSummaryParams{
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
		if err := queries.InsertEdgeTeamShotLocation(ctx, sqlcdb.InsertEdgeTeamShotLocationParams{
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

// ImportEdgeTeamZoneTimeDetails reads cached Edge team zone time details and imports
// zone-time-by-strength and shot-differential data to the database.
func (a *SeasonsActivities) ImportEdgeTeamZoneTimeDetails(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeTeamZoneTimeDetails(ctx, a.EdgeQueries, input)
}

func (a *SeasonsActivities) importEdgeTeamZoneTimeDetails(ctx context.Context, queries EdgeStatsUpserter, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var imported int
	for _, team := range teams {
		teamID := nhlapi.TeamID(team.TeamID)
		detailRes := resource.EdgeTeamZoneTimeDetails{TeamID: teamID, Season: season, GameType: gameType}
		if !a.Storage.Exists(detailRes.Path()) {
			continue
		}

		activity.RecordHeartbeat(ctx, fmt.Sprintf("import-edge-team-zt:%s", team.Abbrev))

		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
		if err != nil {
			logger.Warn("Failed to read edge team zone time from cache", "team", team.Abbrev, "err", err)
			continue
		}

		if err := importEdgeTeamZoneTime(ctx, queries, detail, int64(teamID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge team zone time %s: %w", team.Abbrev, err)
		}
		imported++
	}

	logger.Info("Imported edge team zone time details", "season", input.Season, "teams", imported)
	return nil
}

func importEdgeTeamZoneTime(ctx context.Context, queries EdgeStatsUpserter, detail *nhlapi.EdgeTeamZoneTimeDetails, teamID int64, season int32, gameType sqlcdb.GameType) error {
	// Upsert zone time by strength
	for _, zt := range detail.ZoneTimeDetails {
		if err := queries.InsertEdgeTeamZoneTimeByStrength(ctx, sqlcdb.InsertEdgeTeamZoneTimeByStrengthParams{
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
			TeamID:                       teamID,
			Season:                       season,
			GameType:                     gameType,
			ShotAttemptDifferential:      pf32(sd.ShotAttemptDifferential),
			ShotAttemptDifferentialRank:  pi32(sd.ShotAttemptDifferentialRank),
			SogDifferential:              pf32(sd.SOGDifferential),
			SogDifferentialRank:          pi32(sd.SOGDifferentialRank),
		}); err != nil {
			return fmt.Errorf("upsert shot differential: %w", err)
		}
	}

	return nil
}

// pf32 creates a valid pgtype.Float4 from a float64.
func pf32(v float64) pgtype.Float4 {
	return pgtype.Float4{Float32: float32(v), Valid: true}
}

// pi32 creates a valid pgtype.Int4 from an int.
func pi32(v int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(v), Valid: true}
}

// ===== Per-team import activities (for fine-grained progress tracking) =====

// ImportEdgeTeam imports Edge team stats for a single team (detail + zone time).
func (a *SeasonsActivities) ImportEdgeTeam(ctx context.Context, input ImportEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())
	teamID := nhlapi.TeamID(input.TeamID)

	// Import team detail
	detailRes := resource.EdgeTeamDetail{TeamID: teamID, Season: season, GameType: gameType}
	if a.Storage.Exists(detailRes.Path()) {
		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
		if err != nil {
			logger.Warn("Failed to read edge team detail from cache", "team", input.TeamAbbrev, "err", err)
		} else if err := importEdgeTeamDetail(ctx, a.EdgeQueries, detail, int64(teamID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge team %s: %w", input.TeamAbbrev, err)
		}
	}

	// Import team zone time details
	ztRes := resource.EdgeTeamZoneTimeDetails{TeamID: teamID, Season: season, GameType: gameType}
	if a.Storage.Exists(ztRes.Path()) {
		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, ztRes)
		if err != nil {
			logger.Warn("Failed to read edge team zone time from cache", "team", input.TeamAbbrev, "err", err)
		} else if err := importEdgeTeamZoneTime(ctx, a.EdgeQueries, detail, int64(teamID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge team zone time %s: %w", input.TeamAbbrev, err)
		}
	}

	return nil
}

// ImportEdgeTeamSkaters imports Edge skater stats for a single team's roster.
func (a *SeasonsActivities) ImportEdgeTeamSkaters(ctx context.Context, input ImportEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	roster, err := a.loadSeasonRoster(ctx, input.TeamAbbrev, season)
	if err != nil {
		logger.Debug("No roster found for team", "team", input.TeamAbbrev, "season", input.Season)
		return nil // No roster = nothing to import
	}

	var imported int
	skaters := append(roster.Forwards, roster.Defensemen...)
	for _, player := range skaters {
		playerID := player.ID
		detailRes := resource.EdgeSkaterDetail{PlayerID: playerID, Season: season, GameType: gameType}
		if !a.Storage.Exists(detailRes.Path()) {
			continue
		}

		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
		if err != nil {
			logger.Warn("Failed to read edge skater detail from cache", "player", playerID, "err", err)
			continue
		}

		if err := importEdgeSkaterDetail(ctx, a.EdgeQueries, detail, int64(playerID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge skater %d: %w", playerID, err)
		}
		imported++
	}

	logger.Debug("Imported edge skaters for team", "team", input.TeamAbbrev, "count", imported)
	return nil
}

// ImportEdgeTeamGoalies imports Edge goalie stats for a single team's roster.
func (a *SeasonsActivities) ImportEdgeTeamGoalies(ctx context.Context, input ImportEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	gtLabel := sqlcdb.GameType(gameType.Label())

	roster, err := a.loadSeasonRoster(ctx, input.TeamAbbrev, season)
	if err != nil {
		logger.Debug("No roster found for team", "team", input.TeamAbbrev, "season", input.Season)
		return nil // No roster = nothing to import
	}

	var imported int
	for _, goalie := range roster.Goalies {
		goalieID := goalie.ID
		detailRes := resource.EdgeGoalieDetail{GoalieID: goalieID, Season: season, GameType: gameType}
		if !a.Storage.Exists(detailRes.Path()) {
			continue
		}

		detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, detailRes)
		if err != nil {
			logger.Warn("Failed to read edge goalie detail from cache", "goalie", goalieID, "err", err)
			continue
		}

		if err := importEdgeGoalieDetail(ctx, a.EdgeQueries, detail, int64(goalieID), int32(season.ID()), gtLabel); err != nil {
			return fmt.Errorf("import edge goalie %d: %w", goalieID, err)
		}
		imported++
	}

	logger.Debug("Imported edge goalies for team", "team", input.TeamAbbrev, "count", imported)
	return nil
}

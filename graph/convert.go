package graph

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/sqlcdb"
)

// pgtype → Go conversion helpers for GraphQL resolvers.

// ptr returns a pointer to v. Use for "addressable literal" cases where
// taking the address of a value is awkward in expression position. Distinct
// from the XPtr family below, which converts pgtype values to Go pointers.
func ptr[T any](v T) *T { return &v }

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func int4Ptr(i pgtype.Int4) *int {
	if !i.Valid {
		return nil
	}
	v := int(i.Int32)
	return &v
}

func int8Ptr(i pgtype.Int8) *int64 {
	if !i.Valid {
		return nil
	}
	return &i.Int64
}

func float4Ptr(f pgtype.Float4) *float64 {
	if !f.Valid {
		return nil
	}
	v := float64(f.Float32)
	return &v
}

func boolPtr(b pgtype.Bool) *bool {
	if !b.Valid {
		return nil
	}
	return &b.Bool
}

func dateString(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

func dateStringPtr(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format(time.DateOnly)
	return &s
}

func timestampPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func nullPositionPtr(p sqlcdb.NullPlayerPosition) *string {
	if !p.Valid {
		return nil
	}
	s := string(p.PlayerPosition)
	return &s
}

func nullHandSidePtr(h sqlcdb.NullHandSide) *string {
	if !h.Valid {
		return nil
	}
	s := string(h.HandSide)
	return &s
}

func nullGoalieDecisionPtr(d sqlcdb.NullGoalieDecision) *string {
	if !d.Valid {
		return nil
	}
	s := string(d.GoalieDecision)
	return &s
}

func parseDate(s string) pgtype.Date {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

func optionalInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func optionalInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func optionalText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func optionalBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}

func optionalDate(v *string) pgtype.Date {
	if v == nil {
		return pgtype.Date{}
	}
	return parseDate(*v)
}

func optionalGameType(v *string) sqlcdb.NullGameType {
	if v == nil {
		return sqlcdb.NullGameType{}
	}
	return sqlcdb.NullGameType{GameType: sqlcdb.GameType(*v), Valid: true}
}

func optionalGameState(v *string) sqlcdb.NullGameState {
	if v == nil {
		return sqlcdb.NullGameState{}
	}
	return sqlcdb.NullGameState{GameState: sqlcdb.GameState(*v), Valid: true}
}

// --- Errors ---

var errDatabaseNotConfigured = fmt.Errorf("database not configured")

// --- Conversion functions ---

func convertSeason(s sqlcdb.Season) *model.Season {
	return &model.Season{
		ID:             int(s.ID),
		StandingsStart: dateStringPtr(s.StandingsStart),
		StandingsEnd:   dateStringPtr(s.StandingsEnd),
	}
}

func convertSeasonTeam(t sqlcdb.SeasonTeam) *model.Team {
	return &model.Team{
		TeamID:           t.TeamID,
		Season:           int(t.Season),
		FranchiseID:      int8Ptr(t.FranchiseID),
		FullName:         t.FullName,
		Abbrev:           t.Abbrev,
		LogoURL:          textPtr(t.LogoUrl),
		DivisionName:     t.DivisionName.String,
		DivisionAbbrev:   t.DivisionAbbrev.String,
		ConferenceName:   textPtr(t.ConferenceName),
		ConferenceAbbrev: textPtr(t.ConferenceAbbrev),
	}
}

func convertPlayer(p sqlcdb.Player) *model.Player {
	return &model.Player{
		ID:                 p.ID,
		FirstName:          p.FirstName,
		LastName:           p.LastName,
		TeamID:             int8Ptr(p.TeamID),
		Position:           nullPositionPtr(p.Position),
		ShootsCatches:      nullHandSidePtr(p.ShootsCatches),
		HeightInches:       int4Ptr(p.HeightInches),
		WeightPounds:       int4Ptr(p.WeightPounds),
		BirthDate:          dateStringPtr(p.BirthDate),
		BirthCity:          textPtr(p.BirthCity),
		BirthStateProvince: textPtr(p.BirthStateProvince),
		BirthCountry:       textPtr(p.BirthCountry),
		SweaterNumber:      int4Ptr(p.SweaterNumber),
		IsActive:           p.IsActive,
		HeadshotURL:        p.HeadshotURL,
		HeroImageURL:       textPtr(p.HeroImageURL),
		PlayerSlug:         textPtr(p.PlayerSlug),
		DraftYear:          int4Ptr(p.DraftYear),
		DraftTeamAbbrev:    textPtr(p.DraftTeamAbbrev),
		DraftRound:         int4Ptr(p.DraftRound),
		DraftPickInRound:   int4Ptr(p.DraftPickInRound),
		DraftOverallPick:   int4Ptr(p.DraftOverallPick),
	}
}

func convertGetGameRow(g sqlcdb.GetGameRow) *model.Game {
	return &model.Game{
		ID:                g.ID,
		Season:            int(g.Season),
		GameType:          string(g.GameType),
		GameDate:          dateString(g.GameDate),
		Venue:             g.Venue,
		VenueLocation:     g.VenueLocation,
		StartTimeUtc:      timestampPtr(g.StartTimeUTC),
		GameState:         string(g.GameState),
		GameScheduleState: string(g.GameScheduleState),
		PeriodNumber:      int(g.PeriodNumber),
		PeriodType:        string(g.PeriodType),
		HomeTeamID:        g.HomeTeamID,
		HomeTeamScore:     int(g.HomeTeamScore),
		HomeTeamSog:       int(g.HomeTeamSog),
		HomeTeamName:      g.HomeTeamName,
		HomeTeamAbbrev:    g.HomeTeamAbbrev,
		AwayTeamID:        g.AwayTeamID,
		AwayTeamScore:     int(g.AwayTeamScore),
		AwayTeamSog:       int(g.AwayTeamSog),
		AwayTeamName:      g.AwayTeamName,
		AwayTeamAbbrev:    g.AwayTeamAbbrev,
	}
}

// convertGamesByDateRow and convertListGamesRow reuse convertGetGameRow's
// mapping via a direct struct conversion: GetGameRow, GetGamesByDateRow, and
// ListGamesRow are three sqlc-generated row types with identical field sets
// (only their names differ, and struct tags are ignored for conversion
// purposes), so `sqlcdb.GetGameRow(g)` is a legal, zero-cost Go conversion.
// If a future migration/query change makes the field sets diverge, this
// conversion fails to compile — a loud, safe failure rather than a silent
// field-mapping bug.
func convertGamesByDateRow(g sqlcdb.GetGamesByDateRow) *model.Game {
	return convertGetGameRow(sqlcdb.GetGameRow(g))
}

func convertListGamesRow(g sqlcdb.ListGamesRow) *model.Game {
	return convertGetGameRow(sqlcdb.GetGameRow(g))
}

func convertStandingsSnapshot(s sqlcdb.StandingsSnapshot) *model.StandingsEntry {
	return &model.StandingsEntry{
		Season:           int(s.Season),
		Date:             dateString(s.Date),
		TeamID:           s.TeamID,
		TeamAbbrev:       s.TeamAbbrev,
		Wins:             int(s.Wins),
		Losses:           int(s.Losses),
		OtLosses:         int(s.OtLosses),
		Points:           int(s.Points),
		DivisionAbbrev:   s.DivisionAbbrev,
		DivisionName:     s.DivisionName,
		ConferenceAbbrev: textPtr(s.ConferenceAbbrev),
		ConferenceName:   textPtr(s.ConferenceName),
	}
}

func convertGameSkaterStats(s sqlcdb.GetGameSkaterStatsByGameRow) *model.GameSkaterStats {
	return &model.GameSkaterStats{
		GameID:             s.GameID,
		PlayerID:           s.PlayerID,
		TeamID:             s.TeamID,
		IsHome:             s.IsHome,
		SweaterNumber:      int(s.SweaterNumber),
		Position:           string(s.Position),
		FirstName:          s.FirstName,
		LastName:           s.LastName,
		TeamName:           s.TeamName,
		TeamAbbrev:         s.TeamAbbrev,
		Goals:              int(s.Goals),
		Assists:            int(s.Assists),
		Points:             int(s.Points),
		PlusMinus:          int(s.PlusMinus),
		ShotsOnGoal:        int(s.ShotsOnGoal),
		ToiSeconds:         int(s.TOISeconds),
		Shifts:             int(s.Shifts),
		FaceoffWinningPctg: float4Ptr(s.FaceoffWinningPctg),
		Hits:               int(s.Hits),
		BlockedShots:       int(s.BlockedShots),
		PenaltyMinutes:     int(s.PenaltyMinutes),
		Giveaways:          int(s.Giveaways),
		Takeaways:          int(s.Takeaways),
		PowerPlayGoals:     int(s.PowerPlayGoals),
	}
}

func convertGameGoalieStats(g sqlcdb.GetGameGoalieStatsByGameRow) *model.GameGoalieStats {
	return &model.GameGoalieStats{
		GameID:        g.GameID,
		PlayerID:      g.PlayerID,
		TeamID:        g.TeamID,
		IsHome:        g.IsHome,
		SweaterNumber: int(g.SweaterNumber),
		FirstName:     g.FirstName,
		LastName:      g.LastName,
		TeamName:      g.TeamName,
		TeamAbbrev:    g.TeamAbbrev,
		Decision:      nullGoalieDecisionPtr(g.Decision),
		Starter:       boolPtr(g.Starter),
		ShotsAgainst:  int(g.ShotsAgainst),
		Saves:         int(g.Saves),
		SavePctg:      float4Ptr(g.SavePctg),
		GoalsAgainst:  int(g.GoalsAgainst),
		ToiSeconds:    int(g.TOISeconds),
	}
}

func convertSkaterGameLogEntry(s sqlcdb.GetSkaterStatsByPlayerAndSeasonRow) *model.SkaterGameLogEntry {
	return &model.SkaterGameLogEntry{
		GameID:             s.GameID,
		GameDate:           dateString(s.GameDate),
		GameType:           string(s.GameType),
		TeamAbbrev:         s.TeamAbbrev,
		Goals:              int(s.Goals),
		Assists:            int(s.Assists),
		Points:             int(s.Points),
		PlusMinus:          int(s.PlusMinus),
		ShotsOnGoal:        int(s.ShotsOnGoal),
		ToiSeconds:         int(s.TOISeconds),
		Shifts:             int(s.Shifts),
		FaceoffWinningPctg: float4Ptr(s.FaceoffWinningPctg),
		Hits:               int(s.Hits),
		BlockedShots:       int(s.BlockedShots),
		PenaltyMinutes:     int(s.PenaltyMinutes),
		PowerPlayGoals:     int(s.PowerPlayGoals),
	}
}

func convertGoalieGameLogEntry(g sqlcdb.GetGoalieStatsByPlayerAndSeasonRow) *model.GoalieGameLogEntry {
	return &model.GoalieGameLogEntry{
		GameID:       g.GameID,
		GameDate:     dateString(g.GameDate),
		GameType:     string(g.GameType),
		TeamAbbrev:   g.TeamAbbrev,
		Decision:     nullGoalieDecisionPtr(g.Decision),
		Starter:      boolPtr(g.Starter),
		ShotsAgainst: int(g.ShotsAgainst),
		Saves:        int(g.Saves),
		SavePctg:     float4Ptr(g.SavePctg),
		GoalsAgainst: int(g.GoalsAgainst),
		ToiSeconds:   int(g.TOISeconds),
	}
}

func convertPlayerSeasonTotal(t sqlcdb.PlayerSeasonTotal) *model.PlayerSeasonTotal {
	return &model.PlayerSeasonTotal{
		PlayerID:     t.PlayerID,
		Season:       int(t.Season),
		GameType:     string(t.GameType),
		LeagueAbbrev: t.LeagueAbbrev,
		TeamName:     t.TeamName,
		TeamID:       int8Ptr(t.TeamID),
		GamesPlayed:  int(t.GamesPlayed),
		Goals:        int4Ptr(t.Goals),
		Assists:      int4Ptr(t.Assists),
		Points:       int4Ptr(t.Points),
		PlusMinus:    int4Ptr(t.PlusMinus),
		Pim:          int4Ptr(t.PIM),
	}
}

// --- Edge conversion functions ---

const defaultGameTypeInt = 2 // regular season

func resolveGameType(gameType *int) sqlcdb.GameType {
	gt := defaultGameTypeInt
	if gameType != nil {
		gt = *gameType
	}
	switch gt {
	case 1:
		return sqlcdb.GameTypePreseason
	case 2:
		return sqlcdb.GameTypeRegularSeason
	case 3:
		return sqlcdb.GameTypePlayoffs
	case 4:
		return sqlcdb.GameTypeAllStar
	default:
		return sqlcdb.GameTypeRegularSeason
	}
}

func convertEdgeSkaterStats(s sqlcdb.EdgeSkaterStat, locs []sqlcdb.EdgeSkaterShotLocation, sog []sqlcdb.EdgeSkaterSogSummary) *model.EdgeSkaterStats {
	shotLocs := make([]*model.EdgeShotLocation, len(locs))
	for i, l := range locs {
		shotLocs[i] = &model.EdgeShotLocation{
			Area:                   l.Area,
			Sog:                    int4Ptr(l.SOG),
			Goals:                  int4Ptr(l.Goals),
			ShootingPctg:           float4Ptr(l.ShootingPctg),
			SogPercentile:          float4Ptr(l.SogPercentile),
			GoalsPercentile:        float4Ptr(l.GoalsPercentile),
			ShootingPctgPercentile: float4Ptr(l.ShootingPctgPercentile),
		}
	}
	sogSummary := make([]*model.EdgeSogSummary, len(sog))
	for i, ss := range sog {
		sogSummary[i] = &model.EdgeSogSummary{
			LocationCode:           ss.LocationCode,
			Shots:                  int4Ptr(ss.Shots),
			ShotsPercentile:        float4Ptr(ss.ShotsPercentile),
			ShotsLeagueAvg:         float4Ptr(ss.ShotsLeagueAvg),
			Goals:                  int4Ptr(ss.Goals),
			GoalsPercentile:        float4Ptr(ss.GoalsPercentile),
			GoalsLeagueAvg:         float4Ptr(ss.GoalsLeagueAvg),
			ShootingPctg:           float4Ptr(ss.ShootingPctg),
			ShootingPctgPercentile: float4Ptr(ss.ShootingPctgPercentile),
			ShootingPctgLeagueAvg:  float4Ptr(ss.ShootingPctgLeagueAvg),
		}
	}
	return &model.EdgeSkaterStats{
		PlayerID:                      int(s.PlayerID),
		Season:                        int(s.Season),
		GameType:                      string(s.GameType),
		TopSpeedImperial:              float4Ptr(s.TopSpeedImperial),
		TopSpeedMetric:                float4Ptr(s.TopSpeedMetric),
		TopSpeedPercentile:            float4Ptr(s.TopSpeedPercentile),
		TopSpeedLeagueAvgImperial:     float4Ptr(s.TopSpeedLeagueAvgImperial),
		TopSpeedLeagueAvgMetric:       float4Ptr(s.TopSpeedLeagueAvgMetric),
		BurstsOver20:                  int4Ptr(s.BurstsOver20),
		BurstsOver20Percentile:        float4Ptr(s.BurstsOver20Percentile),
		BurstsOver20LeagueAvg:         float4Ptr(s.BurstsOver20LeagueAvg),
		TotalDistanceImperial:         float4Ptr(s.TotalDistanceImperial),
		TotalDistanceMetric:           float4Ptr(s.TotalDistanceMetric),
		TotalDistancePercentile:       float4Ptr(s.TotalDistancePercentile),
		MaxGameDistanceImperial:       float4Ptr(s.MaxGameDistanceImperial),
		MaxGameDistanceMetric:         float4Ptr(s.MaxGameDistanceMetric),
		MaxGameDistancePercentile:     float4Ptr(s.MaxGameDistancePercentile),
		TopShotSpeedImperial:          float4Ptr(s.TopShotSpeedImperial),
		TopShotSpeedMetric:            float4Ptr(s.TopShotSpeedMetric),
		TopShotSpeedPercentile:        float4Ptr(s.TopShotSpeedPercentile),
		TopShotSpeedLeagueAvgImperial: float4Ptr(s.TopShotSpeedLeagueAvgImperial),
		TopShotSpeedLeagueAvgMetric:   float4Ptr(s.TopShotSpeedLeagueAvgMetric),
		OzPctg:                        float4Ptr(s.OzPctg),
		OzPercentile:                  float4Ptr(s.OzPercentile),
		OzLeagueAvg:                   float4Ptr(s.OzLeagueAvg),
		NzPctg:                        float4Ptr(s.NzPctg),
		NzPercentile:                  float4Ptr(s.NzPercentile),
		NzLeagueAvg:                   float4Ptr(s.NzLeagueAvg),
		DzPctg:                        float4Ptr(s.DzPctg),
		DzPercentile:                  float4Ptr(s.DzPercentile),
		DzLeagueAvg:                   float4Ptr(s.DzLeagueAvg),
		OzEvPctg:                      float4Ptr(s.OzEvPctg),
		OzEvPercentile:                float4Ptr(s.OzEvPercentile),
		ShotLocations:                 shotLocs,
		SogSummary:                    sogSummary,
	}
}

func convertEdgeGoalieStats(s sqlcdb.EdgeGoalieStat, locSummary []sqlcdb.EdgeGoalieShotLocationSummary, locs []sqlcdb.EdgeGoalieShotLocation) *model.EdgeGoalieStats {
	shotLocSummary := make([]*model.EdgeGoalieShotLocationSummary, len(locSummary))
	for i, ls := range locSummary {
		shotLocSummary[i] = &model.EdgeGoalieShotLocationSummary{
			LocationCode:           ls.LocationCode,
			GoalsAgainst:           int4Ptr(ls.GoalsAgainst),
			GoalsAgainstPercentile: float4Ptr(ls.GoalsAgainstPercentile),
			GoalsAgainstLeagueAvg:  float4Ptr(ls.GoalsAgainstLeagueAvg),
			Saves:                  int4Ptr(ls.Saves),
			SavesPercentile:        float4Ptr(ls.SavesPercentile),
			SavesLeagueAvg:         float4Ptr(ls.SavesLeagueAvg),
			SavePctg:               float4Ptr(ls.SavePctg),
			SavePctgPercentile:     float4Ptr(ls.SavePctgPercentile),
			SavePctgLeagueAvg:      float4Ptr(ls.SavePctgLeagueAvg),
		}
	}
	shotLocs := make([]*model.EdgeGoalieShotLocation, len(locs))
	for i, l := range locs {
		shotLocs[i] = &model.EdgeGoalieShotLocation{
			Area:               l.Area,
			Saves:              int4Ptr(l.Saves),
			SavesPercentile:    float4Ptr(l.SavesPercentile),
			SavePctg:           float4Ptr(l.SavePctg),
			SavePctgPercentile: float4Ptr(l.SavePctgPercentile),
		}
	}
	return &model.EdgeGoalieStats{
		PlayerID:                 int(s.PlayerID),
		Season:                   int(s.Season),
		GameType:                 string(s.GameType),
		GaaValue:                 float4Ptr(s.GaaValue),
		GaaPercentile:            float4Ptr(s.GaaPercentile),
		GaaLeagueAvg:             float4Ptr(s.GaaLeagueAvg),
		GamesAbove900:            float4Ptr(s.GamesAbove900Value),
		GamesAbove900Percentile:  float4Ptr(s.GamesAbove900Percentile),
		GamesAbove900LeagueAvg:   float4Ptr(s.GamesAbove900LeagueAvg),
		GoalDiffPer60:            float4Ptr(s.GoalDiffPer60Value),
		GoalDiffPer60Percentile:  float4Ptr(s.GoalDiffPer60Percentile),
		GoalDiffPer60LeagueAvg:   float4Ptr(s.GoalDiffPer60LeagueAvg),
		GoalSupportAvg:           float4Ptr(s.GoalSupportAvgValue),
		GoalSupportAvgPercentile: float4Ptr(s.GoalSupportAvgPercentile),
		GoalSupportAvgLeagueAvg:  float4Ptr(s.GoalSupportAvgLeagueAvg),
		PointPctg:                float4Ptr(s.PointPctgValue),
		PointPctgPercentile:      float4Ptr(s.PointPctgPercentile),
		PointPctgLeagueAvg:       float4Ptr(s.PointPctgLeagueAvg),
		ShotLocationSummary:      shotLocSummary,
		ShotLocations:            shotLocs,
	}
}

func convertEdgeTeamStats(s sqlcdb.EdgeTeamStat, sog []sqlcdb.EdgeTeamSogSummary, locs []sqlcdb.EdgeTeamShotLocation, zt []sqlcdb.EdgeTeamZoneTimeByStrength, sd *sqlcdb.EdgeTeamShotDifferential) *model.EdgeTeamStats {
	sogSummary := make([]*model.EdgeTeamSogSummary, len(sog))
	for i, ss := range sog {
		sogSummary[i] = &model.EdgeTeamSogSummary{
			LocationCode:          ss.LocationCode,
			Shots:                 int4Ptr(ss.Shots),
			ShotsRank:             int4Ptr(ss.ShotsRank),
			ShotsLeagueAvg:        float4Ptr(ss.ShotsLeagueAvg),
			Goals:                 int4Ptr(ss.Goals),
			GoalsRank:             int4Ptr(ss.GoalsRank),
			GoalsLeagueAvg:        float4Ptr(ss.GoalsLeagueAvg),
			ShootingPctg:          float4Ptr(ss.ShootingPctg),
			ShootingPctgRank:      int4Ptr(ss.ShootingPctgRank),
			ShootingPctgLeagueAvg: float4Ptr(ss.ShootingPctgLeagueAvg),
		}
	}
	shotLocs := make([]*model.EdgeTeamShotLocation, len(locs))
	for i, l := range locs {
		shotLocs[i] = &model.EdgeTeamShotLocation{
			Area:      l.Area,
			Shots:     int4Ptr(l.Shots),
			ShotsRank: int4Ptr(l.ShotsRank),
		}
	}
	zoneTime := make([]*model.EdgeTeamZoneTimeByStrength, len(zt))
	for i, z := range zt {
		zoneTime[i] = &model.EdgeTeamZoneTimeByStrength{
			StrengthCode: z.StrengthCode,
			OzPctg:       float4Ptr(z.OzPctg),
			OzRank:       int4Ptr(z.OzRank),
			NzPctg:       float4Ptr(z.NzPctg),
			NzRank:       int4Ptr(z.NzRank),
			DzPctg:       float4Ptr(z.DzPctg),
			DzRank:       int4Ptr(z.DzRank),
		}
	}
	var shotDiff *model.EdgeTeamShotDifferential
	if sd != nil {
		shotDiff = &model.EdgeTeamShotDifferential{
			ShotAttemptDifferential:     float4Ptr(sd.ShotAttemptDifferential),
			ShotAttemptDifferentialRank: int4Ptr(sd.ShotAttemptDifferentialRank),
			SogDifferential:             float4Ptr(sd.SogDifferential),
			SogDifferentialRank:         int4Ptr(sd.SogDifferentialRank),
		}
	}
	return &model.EdgeTeamStats{
		TeamID:                 int(s.TeamID),
		Season:                 int(s.Season),
		GameType:               string(s.GameType),
		ShotAttemptsOver90:     int4Ptr(s.ShotAttemptsOver90),
		ShotAttemptsOver90Rank: int4Ptr(s.ShotAttemptsOver90Rank),
		TopShotSpeedImperial:   float4Ptr(s.TopShotSpeedImperial),
		TopShotSpeedMetric:     float4Ptr(s.TopShotSpeedMetric),
		TopShotSpeedRank:       int4Ptr(s.TopShotSpeedRank),
		SpeedMaxImperial:       float4Ptr(s.SpeedMaxImperial),
		SpeedMaxMetric:         float4Ptr(s.SpeedMaxMetric),
		SpeedMaxRank:           int4Ptr(s.SpeedMaxRank),
		BurstsOver22:           int4Ptr(s.BurstsOver22),
		BurstsOver22Rank:       int4Ptr(s.BurstsOver22Rank),
		BurstsOver20:           int4Ptr(s.BurstsOver20),
		BurstsOver20Rank:       int4Ptr(s.BurstsOver20Rank),
		TotalDistance:          int4Ptr(s.TotalDistance),
		TotalDistanceRank:      int4Ptr(s.TotalDistanceRank),
		OzPctg:                 float4Ptr(s.OzPctg),
		OzRank:                 int4Ptr(s.OzRank),
		OzLeagueAvg:            float4Ptr(s.OzLeagueAvg),
		OzEvPctg:               float4Ptr(s.OzEvPctg),
		OzEvRank:               int4Ptr(s.OzEvRank),
		NzPctg:                 float4Ptr(s.NzPctg),
		NzRank:                 int4Ptr(s.NzRank),
		NzLeagueAvg:            float4Ptr(s.NzLeagueAvg),
		DzPctg:                 float4Ptr(s.DzPctg),
		DzRank:                 int4Ptr(s.DzRank),
		DzLeagueAvg:            float4Ptr(s.DzLeagueAvg),
		SogSummary:             sogSummary,
		ShotLocations:          shotLocs,
		ZoneTimeByStrength:     zoneTime,
		ShotDifferential:       shotDiff,
	}
}

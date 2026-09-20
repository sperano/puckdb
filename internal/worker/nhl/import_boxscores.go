package nhl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportBoxscoresForDateInput contains the parameters for importing boxscores for a single date.
type ImportBoxscoresForDateInput struct {
	shared.DateSeasonInput
}

// ImportBoxscoresForDateResult contains the results of importing boxscores for a single date.
type ImportBoxscoresForDateResult struct {
	GamesImported   int               `json:"gamesImported"`
	SkatersImported int               `json:"skatersImported"`
	GoaliesImported int               `json:"goaliesImported"`
	GamesSkipped    int               `json:"gamesSkipped"`
	Origins         core.OriginCounts `json:"origins"`
}

// ImportBoxscoresForDate imports all boxscores for a single date from the cache into the database.
func (a *ImportActivities) ImportBoxscoresForDate(ctx context.Context, input ImportBoxscoresForDateInput) (ImportBoxscoresForDateResult, error) {
	logger := activity.GetLogger(ctx)

	result, err := a.importBoxscoresForDate(ctx, a.Queries, input)
	if err != nil {
		return result, err
	}

	logger.Info("Imported boxscores for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesImported,
		"skaters", result.SkatersImported,
		"goalies", result.GoaliesImported,
		"skipped", result.GamesSkipped)

	return result, nil
}

// importBoxscoresForDate contains the core boxscore import logic.
// Separated from ImportBoxscoresForDate to allow testing with a narrower queries interface.
func (a *ImportActivities) importBoxscoresForDate(ctx context.Context, queries BoxscoreUpserter, input ImportBoxscoresForDateInput) (ImportBoxscoresForDateResult, error) {
	result := ImportBoxscoresForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(ctx, scheduleRes.Path()) {
		log.Debug().Str("date", input.Date.Format(config.DateFormat)).Msg("No daily schedule file for date")
		return result, nil
	}

	schedule, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
	if err != nil {
		return result, fmt.Errorf("read daily schedule: %w", err)
	}
	result.Origins.Record(origin)

	for _, game := range schedule.Games {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("boxscores:game:%s", game.ID))

		if shouldSkipGame(game) {
			result.GamesSkipped++
			continue
		}

		gameResult, err := importSingleGame(ctx, a.Storage, a.GobCache, queries, game.ID, input.Date, input.Season)
		if err != nil {
			return result, err
		}
		result.Origins.Add(gameResult.Origins)
		result.GamesImported++
		result.SkatersImported += gameResult.SkatersImported
		result.GoaliesImported += gameResult.GoaliesImported
	}

	return result, nil
}

// importSingleGameResult contains the results of importing a single game.
type importSingleGameResult struct {
	Origins         core.OriginCounts
	SkatersImported int
	GoaliesImported int
}

// importSingleGame reads a cached boxscore for a single game and upserts game,
// skater stats, goalie stats, and broadcast data into the database.
func importSingleGame(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, queries BoxscoreUpserter, gameID nhlapi.GameID, date time.Time, season int) (importSingleGameResult, error) {
	result := importSingleGameResult{Origins: core.OriginCounts{}}

	boxscoreRes := resource.Boxscore{Date: date, GameID: gameID}
	if !storage.Exists(ctx, boxscoreRes.Path()) {
		return result, fmt.Errorf("boxscore file missing for game %s", gameID.String())
	}

	boxscore, origin, err := gobCache.ReadParsedCached(ctx, storage, boxscoreRes)
	if err != nil {
		return result, fmt.Errorf("read boxscore for game %s: %w", gameID.String(), err)
	}
	result.Origins.Record(origin)

	gameParams := boxscoreToGameParams(boxscore, season)
	if err := queries.UpsertGame(ctx, gameParams); err != nil {
		return result, fmt.Errorf("upsert game %d: %w", gameID, err)
	}

	skaterCount, err := upsertSkaterStats(ctx, queries, boxscore)
	if err != nil {
		return result, fmt.Errorf("upsert skater stats for game %d: %w", gameID, err)
	}
	result.SkatersImported = skaterCount

	goalieCount, err := upsertGoalieStats(ctx, queries, boxscore)
	if err != nil {
		return result, fmt.Errorf("upsert goalie stats for game %d: %w", gameID, err)
	}
	result.GoaliesImported = goalieCount

	if len(boxscore.TVBroadcasts) > 0 {
		broadcastParams := make([]sqlcdb.UpsertGameBroadcastBatchParams, len(boxscore.TVBroadcasts))
		for i, b := range boxscore.TVBroadcasts {
			broadcastParams[i] = sqlcdb.UpsertGameBroadcastBatchParams{
				GameID:         int64(boxscore.ID),
				BroadcastID:    b.ID,
				Market:         b.Market,
				CountryCode:    b.CountryCode,
				Network:        b.Network,
				SequenceNumber: int32(b.SequenceNumber),
			}
		}
		if err := shared.ExecBatch(queries.UpsertGameBroadcastBatch(ctx, broadcastParams), func(i int) string {
			return fmt.Sprintf("broadcast game %d id %d", boxscore.ID, broadcastParams[i].BroadcastID)
		}); err != nil {
			return result, err
		}
	}

	return result, nil
}

// boxscoreToGameParams converts an NHL API Boxscore to sqlcdb.UpsertGameParams.
func boxscoreToGameParams(b *nhlapi.Boxscore, season int) sqlcdb.UpsertGameParams {
	gameDate := shared.ParseDateToPgDate(b.GameDate)
	startTimeUTC := shared.ParseTimestamptz(time.RFC3339, b.StartTimeUTC)
	seasonID := int32(nhlapi.NewSeason(season).ID())

	return sqlcdb.UpsertGameParams{
		ID:       int64(b.ID),
		Season:   seasonID,
		GameType: sqlcdb.GameType(b.GameType.Label()),
		GameDate: gameDate,

		Venue:         b.Venue.Default,
		VenueLocation: b.VenueLocation.Default,

		StartTimeUTC:     startTimeUTC,
		EasternUTCOffset: b.EasternUTCOffset,
		VenueUTCOffset:   b.VenueUTCOffset,

		GameState:         sqlcdb.GameState(b.GameState),
		GameScheduleState: sqlcdb.GameScheduleState(b.GameScheduleState),

		PeriodNumber:         int16(b.PeriodDescriptor.Number),
		PeriodType:           sqlcdb.PeriodType(b.PeriodDescriptor.PeriodType),
		MaxRegulationPeriods: int16(b.PeriodDescriptor.MaxRegulationPeriods),

		ClockTimeRemaining:    b.Clock.TimeRemaining,
		ClockSecondsRemaining: int32(b.Clock.SecondsRemaining),
		ClockRunning:          b.Clock.Running,
		ClockInIntermission:   b.Clock.InIntermission,

		HomeTeamID:    int64(b.HomeTeam.ID),
		HomeTeamScore: int32(b.HomeTeam.Score),
		HomeTeamSog:   int32(b.HomeTeam.SOG),

		AwayTeamID:    int64(b.AwayTeam.ID),
		AwayTeamScore: int32(b.AwayTeam.Score),
		AwayTeamSog:   int32(b.AwayTeam.SOG),

		LimitedScoring: b.LimitedScoring,
	}
}

// skaterWithMeta holds a skater and metadata for error reporting.
type skaterWithMeta struct {
	skater *nhlapi.SkaterStats
	params sqlcdb.UpsertGameSkaterStatsBatchParams
}

func upsertSkaterStats(ctx context.Context, queries BoxscoreUpserter, b *nhlapi.Boxscore) (int, error) {
	var skaters []skaterWithMeta

	collectSkaters := func(stats []nhlapi.SkaterStats, teamID int64, isHome bool) {
		for i := range stats {
			skaters = append(skaters, skaterWithMeta{
				skater: &stats[i],
				params: skaterToBatchParams(b.ID, teamID, isHome, &stats[i]),
			})
		}
	}

	collectSkaters(b.PlayerByGameStats.AwayTeam.Forwards, int64(b.AwayTeam.ID), false)
	collectSkaters(b.PlayerByGameStats.AwayTeam.Defense, int64(b.AwayTeam.ID), false)
	collectSkaters(b.PlayerByGameStats.HomeTeam.Forwards, int64(b.HomeTeam.ID), true)
	collectSkaters(b.PlayerByGameStats.HomeTeam.Defense, int64(b.HomeTeam.ID), true)

	if len(skaters) == 0 {
		return 0, nil
	}

	params := make([]sqlcdb.UpsertGameSkaterStatsBatchParams, len(skaters))
	for i, s := range skaters {
		params[i] = s.params
	}

	results := queries.UpsertGameSkaterStatsBatch(ctx, params)
	if err := shared.ExecBatch(results, func(i int) string {
		s := skaters[i].skater
		return fmt.Sprintf("player %d (%s, %s)", s.PlayerID, s.Name, s.Position)
	}); err != nil {
		return 0, err
	}

	return len(skaters), nil
}

func skaterToBatchParams(gameID nhlapi.GameID, teamID int64, isHome bool, s *nhlapi.SkaterStats) sqlcdb.UpsertGameSkaterStatsBatchParams {
	var faceoffPctg pgtype.Float4
	if s.FaceoffWinningPctg > 0 {
		faceoffPctg = pgtype.Float4{Float32: float32(s.FaceoffWinningPctg), Valid: true}
	}

	return sqlcdb.UpsertGameSkaterStatsBatchParams{
		GameID:        int64(gameID),
		PlayerID:      int64(s.PlayerID),
		TeamID:        teamID,
		IsHome:        isHome,
		SweaterNumber: int16(s.SweaterNumber),
		Position:      sqlcdb.PlayerPosition(s.Position),

		Goals:       int16(s.Goals),
		Assists:     int16(s.Assists),
		Points:      int16(s.Points),
		PlusMinus:   int16(s.PlusMinus),
		ShotsOnGoal: int16(s.SOG),

		TOISeconds:         int32(parseTOI(s.TOI)),
		Shifts:             int16(s.Shifts),
		FaceoffWinningPctg: faceoffPctg,

		Hits:           int16(s.Hits),
		BlockedShots:   int16(s.BlockedShots),
		PenaltyMinutes: int16(s.PIM),

		Giveaways: int16(s.Giveaways),
		Takeaways: int16(s.Takeaways),

		PowerPlayGoals: int16(s.PowerPlayGoals),
	}
}

// goalieWithMeta holds a goalie and metadata for error reporting.
type goalieWithMeta struct {
	goalie *nhlapi.GoalieStats
	params sqlcdb.UpsertGameGoalieStatsBatchParams
}

func upsertGoalieStats(ctx context.Context, queries BoxscoreUpserter, b *nhlapi.Boxscore) (int, error) {
	var goalies []goalieWithMeta

	collectGoalies := func(stats []nhlapi.GoalieStats, teamID int64, isHome bool) {
		for i := range stats {
			goalies = append(goalies, goalieWithMeta{
				goalie: &stats[i],
				params: goalieToBatchParams(b.ID, teamID, isHome, &stats[i]),
			})
		}
	}

	collectGoalies(b.PlayerByGameStats.AwayTeam.Goalies, int64(b.AwayTeam.ID), false)
	collectGoalies(b.PlayerByGameStats.HomeTeam.Goalies, int64(b.HomeTeam.ID), true)

	if len(goalies) == 0 {
		return 0, nil
	}

	params := make([]sqlcdb.UpsertGameGoalieStatsBatchParams, len(goalies))
	for i, g := range goalies {
		params[i] = g.params
	}

	results := queries.UpsertGameGoalieStatsBatch(ctx, params)
	if err := shared.ExecBatch(results, func(i int) string {
		g := goalies[i].goalie
		return fmt.Sprintf("player %d (%s, %s)", g.PlayerID, g.Name, g.Position)
	}); err != nil {
		return 0, err
	}

	return len(goalies), nil
}

func goalieToBatchParams(gameID nhlapi.GameID, teamID int64, isHome bool, g *nhlapi.GoalieStats) sqlcdb.UpsertGameGoalieStatsBatchParams {
	var decision sqlcdb.NullGoalieDecision
	if g.Decision != nil {
		decision = sqlcdb.NullGoalieDecision{GoalieDecision: sqlcdb.GoalieDecision(*g.Decision), Valid: true}
	}

	var starter pgtype.Bool
	if g.Starter != nil {
		starter = pgtype.Bool{Bool: *g.Starter, Valid: true}
	}

	var savePctg pgtype.Float4
	if g.SavePctg != nil {
		savePctg = pgtype.Float4{Float32: float32(*g.SavePctg), Valid: true}
	}

	var pim pgtype.Int2
	if g.PIM != nil {
		pim = pgtype.Int2{Int16: int16(*g.PIM), Valid: true}
	}

	return sqlcdb.UpsertGameGoalieStatsBatchParams{
		GameID:        int64(gameID),
		PlayerID:      int64(g.PlayerID),
		TeamID:        teamID,
		IsHome:        isHome,
		SweaterNumber: int16(g.SweaterNumber),

		Decision: decision,
		Starter:  starter,

		ShotsAgainst: int32(g.ShotsAgainst),
		Saves:        int32(g.Saves),
		SavePctg:     savePctg,

		GoalsAgainst:             int16(g.GoalsAgainst),
		EvenStrengthGoalsAgainst: int16(g.EvenStrengthGoalsAgainst),
		PowerPlayGoalsAgainst:    int16(g.PowerPlayGoalsAgainst),
		ShorthandedGoalsAgainst:  int16(g.ShorthandedGoalsAgainst),

		EvenStrengthShotsAgainst: pgtype.Text{String: g.EvenStrengthShotsAgainst, Valid: g.EvenStrengthShotsAgainst != ""},
		PowerPlayShotsAgainst:    pgtype.Text{String: g.PowerPlayShotsAgainst, Valid: g.PowerPlayShotsAgainst != ""},
		ShorthandedShotsAgainst:  pgtype.Text{String: g.ShorthandedShotsAgainst, Valid: g.ShorthandedShotsAgainst != ""},

		TOISeconds:     int32(parseTOI(g.TOI)),
		PenaltyMinutes: pim,
	}
}

// parseTOI converts a time-on-ice string "MM:SS" to total seconds.
func parseTOI(toi string) int {
	if toi == "" {
		return 0
	}

	parts := strings.Split(toi, ":")
	if len(parts) != 2 {
		return 0
	}

	minutes, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}

	seconds, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}

	return minutes*60 + seconds
}

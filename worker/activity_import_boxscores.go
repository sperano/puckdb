package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportBoxscoresForDateInput contains the parameters for importing boxscores for a single date.
type ImportBoxscoresForDateInput struct {
	Date   time.Time `json:"date"`
	Season int       `json:"season"`
}

// ImportBoxscoresForDateResult contains the results of importing boxscores for a single date.
type ImportBoxscoresForDateResult struct {
	GamesImported   int `json:"gamesImported"`
	SkatersImported int `json:"skatersImported"`
	GoaliesImported int `json:"goaliesImported"`
	GamesSkipped    int `json:"gamesSkipped"` // Games not yet final
}

// ImportBoxscoresForDateActivity imports all boxscores for a single date from the cache into the database.
func ImportBoxscoresForDateActivity(ctx context.Context, input ImportBoxscoresForDateInput) (ImportBoxscoresForDateResult, error) {
	logger := activity.GetLogger(ctx)
	fs := cache.NewSimpleCache()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return ImportBoxscoresForDateResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return importBoxscoresForDateImpl(ctx, fs, queries, input, logger)
}

// BoxscoreUpserter is the interface for database operations needed by boxscore import.
type BoxscoreUpserter interface {
	UpsertGame(ctx context.Context, arg sqlcdb.UpsertGameParams) error
	UpsertGameSkaterStats(ctx context.Context, arg sqlcdb.UpsertGameSkaterStatsParams) error
	UpsertGameGoalieStats(ctx context.Context, arg sqlcdb.UpsertGameGoalieStatsParams) error
}

type activityLogger interface {
	Info(msg string, keyvals ...interface{})
	Warn(msg string, keyvals ...interface{})
	Debug(msg string, keyvals ...interface{})
}

func importBoxscoresForDateImpl(
	ctx context.Context,
	fs cache.FileSystem,
	queries BoxscoreUpserter,
	input ImportBoxscoresForDateInput,
	logger activityLogger,
) (ImportBoxscoresForDateResult, error) {
	result := ImportBoxscoresForDateResult{}

	// Read the daily schedule to get game IDs
	scheduleFile := cache.DailyScheduleFile{Date: input.Date}
	if !fs.Exists(scheduleFile) {
		logger.Debug("No daily schedule file for date", "date", input.Date.Format("2006-01-02"))
		return result, nil
	}

	scheduleData, err := fs.Read(scheduleFile)
	if err != nil {
		return result, fmt.Errorf("read daily schedule: %w", err)
	}

	var schedule nhl.DailySchedule
	if err := json.Unmarshal(scheduleData, &schedule); err != nil {
		return result, fmt.Errorf("parse daily schedule: %w", err)
	}

	// Process each game
	for _, game := range schedule.Games {
		// Skip games that aren't final
		if !game.GameState.IsFinal() {
			result.GamesSkipped++
			continue
		}

		// Read the boxscore file
		boxscoreFile := cache.BoxscoreFile{Date: input.Date, GameID: game.ID}
		if !fs.Exists(boxscoreFile) {
			logger.Warn("Boxscore file missing for final game", "gameID", game.ID)
			result.GamesSkipped++
			continue
		}

		boxscoreData, err := fs.Read(boxscoreFile)
		if err != nil {
			logger.Warn("Failed to read boxscore file", "gameID", game.ID, "error", err)
			result.GamesSkipped++
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(boxscoreData, &boxscore); err != nil {
			logger.Warn("Failed to parse boxscore", "gameID", game.ID, "error", err)
			result.GamesSkipped++
			continue
		}

		// Upsert the game
		gameParams := boxscoreToGameParams(&boxscore, input.Season)
		if err := queries.UpsertGame(ctx, gameParams); err != nil {
			return result, fmt.Errorf("upsert game %d: %w", game.ID, err)
		}
		result.GamesImported++

		// Upsert skater stats
		skaterCount, err := upsertSkaterStats(ctx, queries, &boxscore)
		if err != nil {
			return result, fmt.Errorf("upsert skater stats for game %d: %w", game.ID, err)
		}
		result.SkatersImported += skaterCount

		// Upsert goalie stats
		goalieCount, err := upsertGoalieStats(ctx, queries, &boxscore)
		if err != nil {
			return result, fmt.Errorf("upsert goalie stats for game %d: %w", game.ID, err)
		}
		result.GoaliesImported += goalieCount
	}

	logger.Info("Imported boxscores for date",
		"date", input.Date.Format("2006-01-02"),
		"games", result.GamesImported,
		"skaters", result.SkatersImported,
		"goalies", result.GoaliesImported,
		"skipped", result.GamesSkipped)

	return result, nil
}

// boxscoreToGameParams converts an NHL API Boxscore to sqlcdb.UpsertGameParams.
func boxscoreToGameParams(b *nhl.Boxscore, season int) sqlcdb.UpsertGameParams {
	// Parse game date
	var gameDate pgtype.Date
	if t, err := time.Parse("2006-01-02", b.GameDate); err == nil {
		gameDate = pgtype.Date{Time: t, Valid: true}
	}

	// Parse start time
	var startTimeUTC pgtype.Timestamptz
	if t, err := time.Parse(time.RFC3339, b.StartTimeUTC); err == nil {
		startTimeUTC = pgtype.Timestamptz{Time: t, Valid: true}
	}

	return sqlcdb.UpsertGameParams{
		ID:       int64(b.ID),
		Season:   int32(season),
		GameType: int16(b.GameType),
		GameDate: gameDate,

		Venue:         b.Venue.Default,
		VenueLocation: b.VenueLocation.Default,

		StartTimeUTC:     startTimeUTC,
		EasternUTCOffset: b.EasternUTCOffset,
		VenueUTCOffset:   b.VenueUTCOffset,

		GameState:         string(b.GameState),
		GameScheduleState: string(b.GameScheduleState),

		PeriodNumber:         int16(b.PeriodDescriptor.Number),
		PeriodType:           string(b.PeriodDescriptor.PeriodType),
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

func upsertSkaterStats(ctx context.Context, queries BoxscoreUpserter, b *nhl.Boxscore) (int, error) {
	count := 0

	// Process away team skaters
	for _, skater := range b.PlayerByGameStats.AwayTeam.Forwards {
		params := skaterToParams(b.ID, int64(b.AwayTeam.ID), false, &skater)
		if err := queries.UpsertGameSkaterStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}
	for _, skater := range b.PlayerByGameStats.AwayTeam.Defense {
		params := skaterToParams(b.ID, int64(b.AwayTeam.ID), false, &skater)
		if err := queries.UpsertGameSkaterStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}

	// Process home team skaters
	for _, skater := range b.PlayerByGameStats.HomeTeam.Forwards {
		params := skaterToParams(b.ID, int64(b.HomeTeam.ID), true, &skater)
		if err := queries.UpsertGameSkaterStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}
	for _, skater := range b.PlayerByGameStats.HomeTeam.Defense {
		params := skaterToParams(b.ID, int64(b.HomeTeam.ID), true, &skater)
		if err := queries.UpsertGameSkaterStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func skaterToParams(gameID nhl.GameID, teamID int64, isHome bool, s *nhl.SkaterStats) sqlcdb.UpsertGameSkaterStatsParams {
	var faceoffPctg pgtype.Float4
	if s.FaceoffWinningPctg > 0 {
		faceoffPctg = pgtype.Float4{Float32: float32(s.FaceoffWinningPctg), Valid: true}
	}

	return sqlcdb.UpsertGameSkaterStatsParams{
		GameID:        int64(gameID),
		PlayerID:      int64(s.PlayerID),
		TeamID:        teamID,
		IsHome:        isHome,
		SweaterNumber: int16(s.SweaterNumber),
		Position:      string(s.Position),

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

func upsertGoalieStats(ctx context.Context, queries BoxscoreUpserter, b *nhl.Boxscore) (int, error) {
	count := 0

	// Process away team goalies
	for _, goalie := range b.PlayerByGameStats.AwayTeam.Goalies {
		params := goalieToParams(b.ID, int64(b.AwayTeam.ID), false, &goalie)
		if err := queries.UpsertGameGoalieStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}

	// Process home team goalies
	for _, goalie := range b.PlayerByGameStats.HomeTeam.Goalies {
		params := goalieToParams(b.ID, int64(b.HomeTeam.ID), true, &goalie)
		if err := queries.UpsertGameGoalieStats(ctx, params); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func goalieToParams(gameID nhl.GameID, teamID int64, isHome bool, g *nhl.GoalieStats) sqlcdb.UpsertGameGoalieStatsParams {
	var decision pgtype.Text
	if g.Decision != nil {
		decision = pgtype.Text{String: string(*g.Decision), Valid: true}
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

	return sqlcdb.UpsertGameGoalieStatsParams{
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

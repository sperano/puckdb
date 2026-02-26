package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// ImportGameStoryForDateInput specifies which date's game story data to import.
type ImportGameStoryForDateInput struct {
	Season int       // Season ID (e.g., 20232024)
	Date   time.Time // Date to import
}

// ImportGameStoryForDateResult contains import statistics.
type ImportGameStoryForDateResult struct {
	GamesProcessed     int
	ThreeStarsImported int
	HighlightsImported int
	ShootoutsImported  int
	Errors             []string
}

// ImportGameStoryForDateActivity imports game story data (three stars, highlights, shootouts)
// for games played on a specific date.
func ImportGameStoryForDateActivity(ctx context.Context, input ImportGameStoryForDateInput) (*ImportGameStoryForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportGameStoryForDateActivity", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	repos := store.NewDefaultRepos()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := importGameStoryForDateImpl(ctx, repos, queries, input)
	if err != nil {
		return result, err
	}

	logger.Debug("Imported game story data for date",
		"date", input.Date.Format("2006-01-02"),
		"games", result.GamesProcessed,
		"threeStars", result.ThreeStarsImported,
		"highlights", result.HighlightsImported,
		"shootouts", result.ShootoutsImported,
		"errors", len(result.Errors))

	return result, nil
}

// GameStoryUpdater is the interface for database operations needed by game story import.
type GameStoryUpdater interface {
	UpsertGameThreeStar(ctx context.Context, arg sqlcdb.UpsertGameThreeStarParams) error
	UpsertGoalHighlight(ctx context.Context, arg sqlcdb.UpsertGoalHighlightParams) error
	UpsertShootoutAttempt(ctx context.Context, arg sqlcdb.UpsertShootoutAttemptParams) error
	GetTeamIDByAbbrev(ctx context.Context, arg sqlcdb.GetTeamIDByAbbrevParams) (int64, error)
}

func importGameStoryForDateImpl(
	ctx context.Context,
	repos *store.Repos,
	queries GameStoryUpdater,
	input ImportGameStoryForDateInput,
) (*ImportGameStoryForDateResult, error) {
	result := &ImportGameStoryForDateResult{}

	// Read daily schedule to get game IDs
	if !repos.Schedule.Exists(input.Date) {
		return result, nil // No games on this date
	}

	schedule, err := repos.Schedule.Get(input.Date)
	if err != nil {
		return result, fmt.Errorf("read schedule: %w", err)
	}

	if len(schedule.Games) == 0 {
		return result, nil
	}

	// Process each game's story
	for _, game := range schedule.Games {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		stats, errs := processGameStory(ctx, repos, queries, game.ID, input.Season, input.Date)
		result.GamesProcessed++
		result.ThreeStarsImported += stats.threeStars
		result.HighlightsImported += stats.highlights
		result.ShootoutsImported += stats.shootouts
		result.Errors = append(result.Errors, errs...)
	}

	return result, nil
}

type gameStoryStats struct {
	threeStars int
	highlights int
	shootouts  int
}

func processGameStory(
	ctx context.Context,
	repos *store.Repos,
	queries GameStoryUpdater,
	gameID nhl.GameID,
	season int,
	date time.Time,
) (gameStoryStats, []string) {
	var stats gameStoryStats
	var errors []string

	// Read game story file
	if !repos.GameStory.Exists(date, gameID) {
		return stats, errors // No game story for this game
	}

	story, err := repos.GameStory.Get(date, gameID)
	if err != nil {
		errors = append(errors, fmt.Sprintf("game %d: read error: %v", gameID, err))
		return stats, errors
	}

	gid := int64(gameID)

	// Import three stars
	if story.Summary.ThreeStars != nil {
		for _, star := range *story.Summary.ThreeStars {
			if err := queries.UpsertGameThreeStar(ctx, sqlcdb.UpsertGameThreeStarParams{
				GameID:   gid,
				Star:     int16(star.Star),
				PlayerID: int64(star.PlayerID),
			}); err != nil {
				errors = append(errors, fmt.Sprintf("game %d: three star %d: %v", gameID, star.Star, err))
				continue
			}
			stats.threeStars++
		}
	}

	// Import goal highlights from scoring summary
	for _, period := range story.Summary.Scoring {
		periodNum := int16(period.PeriodDescriptor.Number)
		for _, goal := range period.Goals {
			params := sqlcdb.UpsertGoalHighlightParams{
				GameID:       gid,
				EventID:      goal.EventID,
				PlayerID:     int64(goal.PlayerID),
				Period:       periodNum,
				TimeInPeriod: goal.TimeInPeriod,
			}

			if goal.GoalsToDate != nil {
				params.GoalsToDate = pgtype.Int2{Int16: int16(*goal.GoalsToDate), Valid: true}
			}
			if goal.HighlightClip != nil {
				params.HighlightClipID = pgtype.Int8{Int64: *goal.HighlightClip, Valid: true}
			}
			if goal.HighlightClipSharingURL != nil {
				params.HighlightClipUrl = pgtype.Text{String: *goal.HighlightClipSharingURL, Valid: true}
			}
			if goal.DiscreteClip != nil {
				params.DiscreteClipID = pgtype.Int8{Int64: *goal.DiscreteClip, Valid: true}
			}

			if err := queries.UpsertGoalHighlight(ctx, params); err != nil {
				errors = append(errors, fmt.Sprintf("game %d: goal %d: %v", gameID, goal.EventID, err))
				continue
			}
			stats.highlights++
		}
	}

	// Import shootout attempts
	if story.Summary.Shootout != nil {
		for _, attempt := range *story.Summary.Shootout {
			// Look up team_id from abbreviation
			teamID, err := queries.GetTeamIDByAbbrev(ctx, sqlcdb.GetTeamIDByAbbrevParams{
				Abbrev:   attempt.TeamAbbrev.Default,
				SeasonID: int32(season),
			})
			if err != nil {
				errors = append(errors, fmt.Sprintf("game %d: shootout %d: team lookup %s: %v",
					gameID, attempt.Sequence, attempt.TeamAbbrev.Default, err))
				continue
			}

			if err := queries.UpsertShootoutAttempt(ctx, sqlcdb.UpsertShootoutAttemptParams{
				GameID:     gid,
				Sequence:   int16(attempt.Sequence),
				PlayerID:   int64(attempt.PlayerID),
				TeamID:     teamID,
				ShotType:   attempt.ShotType,
				Result:     attempt.Result,
				GameWinner: attempt.GameWinner,
			}); err != nil {
				errors = append(errors, fmt.Sprintf("game %d: shootout %d: %v", gameID, attempt.Sequence, err))
				continue
			}
			stats.shootouts++
		}
	}

	return stats, errors
}

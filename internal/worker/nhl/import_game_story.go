package nhl

import (
	"context"
	"fmt"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
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
	Origins            core.OriginCounts
}

// ImportGameStoryForDate imports game story data (three stars, highlights, shootouts)
// for games played on a specific date.
func (a *ImportActivities) ImportGameStoryForDate(ctx context.Context, input ImportGameStoryForDateInput) (*ImportGameStoryForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportGameStoryForDate")()

	logger := activity.GetLogger(ctx)

	result := &ImportGameStoryForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !resource.Exists(ctx, a.Storage, scheduleRes) {
		return result, nil
	}

	schedule, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
	if err != nil {
		return result, fmt.Errorf("read schedule: %w", err)
	}
	result.Origins.Record(origin)

	if len(schedule.Games) == 0 {
		return result, nil
	}

	for _, game := range schedule.Games {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("game-stories:game:%d", game.ID))

		stats, errs := a.processGameStory(ctx, game.ID, input.Season, input.Date, result.Origins)
		result.GamesProcessed++
		result.ThreeStarsImported += stats.threeStars
		result.HighlightsImported += stats.highlights
		result.ShootoutsImported += stats.shootouts
		result.Errors = append(result.Errors, errs...)
	}

	logger.Debug("Imported game story data for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesProcessed,
		"threeStars", result.ThreeStarsImported,
		"highlights", result.HighlightsImported,
		"shootouts", result.ShootoutsImported,
		"errors", len(result.Errors))

	return result, nil
}

type gameStoryStats struct {
	threeStars int
	highlights int
	shootouts  int
}

func (a *ImportActivities) processGameStory(
	ctx context.Context,
	gameID nhlapi.GameID,
	season int,
	date time.Time,
	origins core.OriginCounts,
) (gameStoryStats, []string) {
	var stats gameStoryStats
	var errors []string

	storyRes := resource.GameStory{Date: date, GameID: gameID}
	if !resource.Exists(ctx, a.Storage, storyRes) {
		return stats, errors
	}

	story, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, storyRes)
	if err != nil {
		errors = append(errors, fmt.Sprintf("game %d: read error: %v", gameID, err))
		return stats, errors
	}
	origins.Record(origin)

	gid := int64(gameID)

	if story.Summary.ThreeStars != nil {
		for _, star := range story.Summary.ThreeStars {
			if err := a.Queries.UpsertGameThreeStar(ctx, sqlcdb.UpsertGameThreeStarParams{
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

			params.GoalsToDate = shared.PtrToInt2(goal.GoalsToDate)
			params.HighlightClipID = shared.PtrToInt8(goal.HighlightClip)
			params.HighlightClipUrl = shared.PtrToText(goal.HighlightClipSharingURL)
			params.DiscreteClipID = shared.PtrToInt8(goal.DiscreteClip)

			if err := a.Queries.UpsertGoalHighlight(ctx, params); err != nil {
				errors = append(errors, fmt.Sprintf("game %d: goal %d: %v", gameID, goal.EventID, err))
				continue
			}
			stats.highlights++
		}
	}

	if story.Summary.Shootout != nil {
		for _, attempt := range story.Summary.Shootout {
			teamID, err := a.Queries.GetTeamIDByAbbrev(ctx, sqlcdb.GetTeamIDByAbbrevParams{
				Abbrev: attempt.TeamAbbrev.Default,
				Season: int32(season),
			})
			if err != nil {
				errors = append(errors, fmt.Sprintf("game %d: shootout %d: team lookup %s: %v",
					gameID, attempt.Sequence, attempt.TeamAbbrev.Default, err))
				continue
			}

			if err := a.Queries.UpsertShootoutAttempt(ctx, sqlcdb.UpsertShootoutAttemptParams{
				GameID:     gid,
				Sequence:   int16(attempt.Sequence),
				PlayerID:   int64(attempt.PlayerID),
				TeamID:     teamID,
				ShotType:   attempt.ShotType,
				Result:     sqlcdb.ShootoutResult(attempt.Result),
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

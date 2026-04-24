package nhl

import (
	"context"
	"fmt"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)


// ImportSeasonSeriesForDateInput specifies which date's season series data to import.
type ImportSeasonSeriesForDateInput struct {
	Date time.Time
}

// ImportSeasonSeriesForDateResult contains import statistics.
type ImportSeasonSeriesForDateResult struct {
	GamesProcessed    int
	OfficialsImported int
	CoachesImported   int
	ScratchesImported int
	Errors            []string
	Origins           core.OriginCounts
}

// ImportSeasonSeriesForDate imports season series data (officials, coaches, scratches)
// for games played on a specific date.
func (a *ImportActivities) ImportSeasonSeriesForDate(ctx context.Context, input ImportSeasonSeriesForDateInput) (*ImportSeasonSeriesForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportSeasonSeriesForDate")()

	logger := activity.GetLogger(ctx)
	result := &ImportSeasonSeriesForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(ctx, scheduleRes.Path()) {
		return result, nil
	}

	schedule, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
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
		activity.RecordHeartbeat(ctx, fmt.Sprintf("season-series:game:%d", game.ID))

		stats, errs := a.processSeasonSeries(ctx, game.ID, input.Date, result.Origins)
		result.GamesProcessed++
		result.OfficialsImported += stats.officials
		result.CoachesImported += stats.coaches
		result.ScratchesImported += stats.scratches
		result.Errors = append(result.Errors, errs...)
	}

	logger.Debug("Imported season series data for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesProcessed,
		"officials", result.OfficialsImported,
		"coaches", result.CoachesImported,
		"scratches", result.ScratchesImported,
		"errors", len(result.Errors))

	return result, nil
}

type seasonSeriesStats struct {
	officials int
	coaches   int
	scratches int
}

func (a *ImportActivities) processSeasonSeries(
	ctx context.Context,
	gameID nhlapi.GameID,
	date time.Time,
	origins core.OriginCounts,
) (seasonSeriesStats, []string) {
	var stats seasonSeriesStats
	var errors []string

	seriesRes := resource.SeasonSeries{Date: date, GameID: gameID}
	if !a.Storage.Exists(ctx, seriesRes.Path()) {
		return stats, errors
	}

	matchup, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, seriesRes)
	if err != nil {
		errors = append(errors, fmt.Sprintf("game %d: read error: %v", gameID, err))
		return stats, errors
	}
	origins.Record(origin)

	gid := int64(gameID)

	teams, err := a.Queries.GetGameTeamIDs(ctx, gid)
	if err != nil {
		errors = append(errors, fmt.Sprintf("game %d: get team IDs: %v", gameID, err))
		return stats, errors
	}

	for i, ref := range matchup.GameInfo.Referees {
		if err := a.Queries.UpsertGameOfficial(ctx, sqlcdb.UpsertGameOfficialParams{
			GameID:   gid,
			Role:     sqlcdb.OfficialRoleReferee,
			Sequence: int16(i + 1),
			Name:     ref.Default,
		}); err != nil {
			errors = append(errors, fmt.Sprintf("game %d: referee %d: %v", gameID, i+1, err))
			continue
		}
		stats.officials++
	}

	for i, linesman := range matchup.GameInfo.Linesmen {
		if err := a.Queries.UpsertGameOfficial(ctx, sqlcdb.UpsertGameOfficialParams{
			GameID:   gid,
			Role:     sqlcdb.OfficialRoleLinesman,
			Sequence: int16(i + 1),
			Name:     linesman.Default,
		}); err != nil {
			errors = append(errors, fmt.Sprintf("game %d: linesman %d: %v", gameID, i+1, err))
			continue
		}
		stats.officials++
	}

	for _, side := range []struct {
		info   nhlapi.TeamGameInfo
		teamID int64
	}{
		{matchup.GameInfo.HomeTeam, teams.HomeTeamID},
		{matchup.GameInfo.AwayTeam, teams.AwayTeamID},
	} {
		if err := a.Queries.UpsertGameCoach(ctx, sqlcdb.UpsertGameCoachParams{
			GameID:    gid,
			TeamID:    side.teamID,
			HeadCoach: side.info.HeadCoach.Default,
		}); err != nil {
			errors = append(errors, fmt.Sprintf("game %d: coach team %d: %v", gameID, side.teamID, err))
			continue
		}
		stats.coaches++
	}

	for _, side := range []struct {
		info   nhlapi.TeamGameInfo
		teamID int64
	}{
		{matchup.GameInfo.HomeTeam, teams.HomeTeamID},
		{matchup.GameInfo.AwayTeam, teams.AwayTeamID},
	} {
		for _, scratch := range side.info.Scratches {
			if err := a.Queries.UpsertGameScratch(ctx, sqlcdb.UpsertGameScratchParams{
				GameID:   gid,
				TeamID:   side.teamID,
				PlayerID: int64(scratch.ID),
			}); err != nil {
				errors = append(errors, fmt.Sprintf("game %d: scratch player %d: %v", gameID, scratch.ID, err))
				continue
			}
			stats.scratches++
		}
	}

	return stats, errors
}

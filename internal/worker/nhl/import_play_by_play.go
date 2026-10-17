package nhl

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportPlayByPlayForDateInput contains the parameters for importing play-by-play data for a date.
type ImportPlayByPlayForDateInput struct {
	shared.DateSeasonInput
}

// ImportPlayByPlayForDateResult contains the results of importing play-by-play data.
type ImportPlayByPlayForDateResult struct {
	GamesProcessed int               `json:"gamesProcessed"`
	EventsImported int               `json:"eventsImported"`
	Origins        core.OriginCounts `json:"origins"`
}

// ImportPlayByPlayForDate imports play-by-play data for all games on a given date.
func (a *ImportActivities) ImportPlayByPlayForDate(ctx context.Context, input ImportPlayByPlayForDateInput) (ImportPlayByPlayForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportPlayByPlayForDate")()

	logger := activity.GetLogger(ctx)

	result := ImportPlayByPlayForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !resource.Exists(ctx, a.Storage, scheduleRes) {
		return result, nil
	}

	schedule, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
	if err != nil {
		return result, fmt.Errorf("read daily schedule: %w", err)
	}
	result.Origins.Record(origin)

	for _, game := range schedule.Games {
		if shouldSkipGame(game) {
			continue
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("play-by-play:game:%s", game.ID))

		pbpRes := resource.PlayByPlay{Date: input.Date, GameID: game.ID}
		if !resource.Exists(ctx, a.Storage, pbpRes) {
			return result, fmt.Errorf("play-by-play file missing for game %s", game.ID.String())
		}

		pbp, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, pbpRes)
		if err != nil {
			return result, fmt.Errorf("read play-by-play for game %s: %w", game.ID.String(), err)
		}
		result.Origins.Record(origin)

		if len(pbp.Plays) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertPlayEventBatchParams, len(pbp.Plays))
		for i, play := range pbp.Plays {
			params[i] = playEventToParams(game.ID, play)
		}

		if err := shared.ExecBatch(a.Queries.UpsertPlayEventBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("play event %d for game %s", pbp.Plays[i].EventID, game.ID.String())
		}); err != nil {
			return result, err
		}

		result.GamesProcessed++
		result.EventsImported += len(pbp.Plays)
	}

	logger.Debug("Imported play-by-play for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesProcessed,
		"events", result.EventsImported)

	return result, nil
}

func playEventToParams(gameID nhlapi.GameID, play nhlapi.PlayEvent) sqlcdb.UpsertPlayEventBatchParams {
	p := sqlcdb.UpsertPlayEventBatchParams{
		GameID:        int64(gameID),
		EventID:       play.EventID,
		Period:        int32(play.PeriodDescriptor.Number),
		PeriodType:    sqlcdb.PeriodType(play.PeriodDescriptor.PeriodType),
		TimeInPeriod:  play.TimeInPeriod,
		TimeRemaining: play.TimeRemaining,
		TypeDescKey:   sqlcdb.PlayEventType(play.TypeDescKey),
		SortOrder:     int32(play.SortOrder),
	}

	if play.SituationCode != "" {
		if code, err := strconv.Atoi(play.SituationCode); err == nil {
			p.SituationCode = pgtype.Int4{Int32: int32(code), Valid: true}
		}
	}
	if play.HomeTeamDefendingSide != "" {
		p.HomeTeamDefendingSide = sqlcdb.NullIceSide{IceSide: sqlcdb.IceSide(play.HomeTeamDefendingSide), Valid: true}
	}

	if d := play.Details; d != nil {
		p.XCoord = pgconv.PtrToInt4(d.XCoord)
		p.YCoord = pgconv.PtrToInt4(d.YCoord)
		if d.ZoneCode != nil {
			p.ZoneCode = sqlcdb.NullZoneCode{ZoneCode: sqlcdb.ZoneCode(*d.ZoneCode), Valid: true}
		}
		p.EventOwnerTeamID = pgconv.PtrToInt8(d.EventOwnerTeamID)
		p.ShotType = pgconv.PtrToText(d.ShotType)
		p.ShootingPlayerID = pgconv.PtrToInt8(d.ShootingPlayerID)
		p.GoalieInNetID = pgconv.PtrToInt8(d.GoalieInNetID)
		p.BlockingPlayerID = pgconv.PtrToInt8(d.BlockingPlayerID)
		p.ScoringPlayerID = pgconv.PtrToInt8(d.ScoringPlayerID)
		p.ScoringPlayerTotal = pgconv.PtrToInt4(d.ScoringPlayerTotal)
		p.Assist1PlayerID = pgconv.PtrToInt8(d.Assist1PlayerID)
		p.Assist1PlayerTotal = pgconv.PtrToInt4(d.Assist1PlayerTotal)
		p.Assist2PlayerID = pgconv.PtrToInt8(d.Assist2PlayerID)
		p.Assist2PlayerTotal = pgconv.PtrToInt4(d.Assist2PlayerTotal)
		p.AwayScore = pgconv.PtrToInt4(d.AwayScore)
		p.HomeScore = pgconv.PtrToInt4(d.HomeScore)
		p.HighlightClipID = pgconv.PtrToInt8(d.HighlightClip)
		p.HighlightClipUrl = pgconv.PtrToText(d.HighlightClipSharingURL)
		p.DiscreteClipID = pgconv.PtrToInt8(d.DiscreteClip)
		p.PenaltyTypeCode = pgconv.PtrToText(d.TypeCode)
		p.PenaltyDescKey = pgconv.PtrToText(d.DescKey)
		p.PenaltyDuration = pgconv.PtrToInt4(d.Duration)
		p.CommittedByPlayerID = pgconv.PtrToInt8(d.CommittedByPlayerID)
		p.DrawnByPlayerID = pgconv.PtrToInt8(d.DrawnByPlayerID)
		p.HittingPlayerID = pgconv.PtrToInt8(d.HittingPlayerID)
		p.HitteePlayerID = pgconv.PtrToInt8(d.HitteePlayerID)
		p.WinningPlayerID = pgconv.PtrToInt8(d.WinningPlayerID)
		p.LosingPlayerID = pgconv.PtrToInt8(d.LosingPlayerID)
		p.PlayerID = pgconv.PtrToInt8(d.PlayerID)
		p.Reason = pgconv.PtrToText(d.Reason)
		p.AwaySog = pgconv.PtrToInt4(d.AwaySOG)
		p.HomeSog = pgconv.PtrToInt4(d.HomeSOG)
	}

	return p
}

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// PlayByPlayUpserter is the interface for database operations needed by play-by-play import.
type PlayByPlayUpserter interface {
	UpsertPlayEventBatch(ctx context.Context, arg []sqlcdb.UpsertPlayEventBatchParams) *sqlcdb.UpsertPlayEventBatchBatchResults
}

// ImportPlayByPlayForDateInput contains the parameters for importing play-by-play data for a date.
type ImportPlayByPlayForDateInput struct {
	DateSeasonInput
}

// ImportPlayByPlayForDateResult contains the results of importing play-by-play data.
type ImportPlayByPlayForDateResult struct {
	GamesProcessed int              `json:"gamesProcessed"`
	EventsImported int              `json:"eventsImported"`
	Origins        core.OriginCounts `json:"origins"`
}

// ImportPlayByPlayForDate imports play-by-play data for all games on a given date.
func (a *SeasonsActivities) ImportPlayByPlayForDate(ctx context.Context, input ImportPlayByPlayForDateInput) (ImportPlayByPlayForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportPlayByPlayForDate", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)

	result := ImportPlayByPlayForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(scheduleRes.Path()) {
		return result, nil
	}

	schedule, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
	if err != nil {
		return result, fmt.Errorf("read daily schedule: %w", err)
	}
	result.Origins.Record(origin)

	for _, game := range schedule.Games {
		if shouldSkipGame(game) {
			continue
		}

		pbpRes := resource.PlayByPlay{Date: input.Date, GameID: game.ID}
		if !a.Storage.Exists(pbpRes.Path()) {
			return result, fmt.Errorf("play-by-play file missing for game %s", game.ID.String())
		}

		pbp, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, pbpRes)
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

		var firstErr error
		var errIdx int
		results := a.ImportQueries.UpsertPlayEventBatch(ctx, params)
		results.Exec(func(i int, err error) {
			if err != nil && firstErr == nil {
				firstErr = err
				errIdx = i
			}
		})

		if firstErr != nil {
			return result, fmt.Errorf("upsert play event %d for game %s: %w",
				pbp.Plays[errIdx].EventID, game.ID.String(), firstErr)
		}

		result.GamesProcessed++
		result.EventsImported += len(pbp.Plays)
	}

	logger.Debug("Imported play-by-play for date",
		"date", input.Date.Format("2006-01-02"),
		"games", result.GamesProcessed,
		"events", result.EventsImported)

	return result, nil
}

func playEventToParams(gameID nhl.GameID, play nhl.PlayEvent) sqlcdb.UpsertPlayEventBatchParams {
	p := sqlcdb.UpsertPlayEventBatchParams{
		GameID:        int64(gameID),
		EventID:       play.EventID,
		Period:        int32(play.PeriodDescriptor.Number),
		PeriodType:    string(play.PeriodDescriptor.PeriodType),
		TimeInPeriod:  play.TimeInPeriod,
		TimeRemaining: play.TimeRemaining,
		TypeCode:      int32(play.TypeCode),
		TypeDescKey:   string(play.TypeDescKey),
		SortOrder:     int32(play.SortOrder),
	}

	if play.SituationCode != "" {
		p.SituationCode = pgtype.Text{String: play.SituationCode, Valid: true}
	}
	if play.HomeTeamDefendingSide != "" {
		p.HomeTeamDefendingSide = pgtype.Text{String: string(play.HomeTeamDefendingSide), Valid: true}
	}

	if d := play.Details; d != nil {
		p.XCoord = ptrToInt4(d.XCoord)
		p.YCoord = ptrToInt4(d.YCoord)
		p.ZoneCode = ptrToText(d.ZoneCode)
		p.EventOwnerTeamID = ptrToInt8(d.EventOwnerTeamID)
		p.ShotType = ptrToText(d.ShotType)
		p.ShootingPlayerID = ptrToInt8(d.ShootingPlayerID)
		p.GoalieInNetID = ptrToInt8(d.GoalieInNetID)
		p.BlockingPlayerID = ptrToInt8(d.BlockingPlayerID)
		p.ScoringPlayerID = ptrToInt8(d.ScoringPlayerID)
		p.ScoringPlayerTotal = ptrToInt4(d.ScoringPlayerTotal)
		p.Assist1PlayerID = ptrToInt8(d.Assist1PlayerID)
		p.Assist1PlayerTotal = ptrToInt4(d.Assist1PlayerTotal)
		p.Assist2PlayerID = ptrToInt8(d.Assist2PlayerID)
		p.Assist2PlayerTotal = ptrToInt4(d.Assist2PlayerTotal)
		p.AwayScore = ptrToInt4(d.AwayScore)
		p.HomeScore = ptrToInt4(d.HomeScore)
		p.HighlightClipID = ptrToInt8(d.HighlightClip)
		p.HighlightClipUrl = ptrToText(d.HighlightClipSharingURL)
		p.DiscreteClipID = ptrToInt8(d.DiscreteClip)
		p.PenaltyTypeCode = ptrToText(d.TypeCode)
		p.PenaltyDescKey = ptrToText(d.DescKey)
		p.PenaltyDuration = ptrToInt4(d.Duration)
		p.CommittedByPlayerID = ptrToInt8(d.CommittedByPlayerID)
		p.DrawnByPlayerID = ptrToInt8(d.DrawnByPlayerID)
		p.HittingPlayerID = ptrToInt8(d.HittingPlayerID)
		p.HitteePlayerID = ptrToInt8(d.HitteePlayerID)
		p.WinningPlayerID = ptrToInt8(d.WinningPlayerID)
		p.LosingPlayerID = ptrToInt8(d.LosingPlayerID)
		p.PlayerID = ptrToInt8(d.PlayerID)
		p.Reason = ptrToText(d.Reason)
		p.AwaySog = ptrToInt4(d.AwaySOG)
		p.HomeSog = ptrToInt4(d.HomeSOG)
	}

	return p
}

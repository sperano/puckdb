package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ShiftChartUpserter is the interface for database operations needed by shift chart import.
type ShiftChartUpserter interface {
	UpsertShiftBatch(ctx context.Context, arg []sqlcdb.UpsertShiftBatchParams) *sqlcdb.UpsertShiftBatchBatchResults
}

// ImportShiftChartForDateInput contains the parameters for importing shift chart data for a date.
type ImportShiftChartForDateInput struct {
	DateSeasonInput
}

// ImportShiftChartForDateResult contains the results of importing shift chart data.
type ImportShiftChartForDateResult struct {
	GamesProcessed int              `json:"gamesProcessed"`
	ShiftsImported int              `json:"shiftsImported"`
	Origins        core.OriginCounts `json:"origins"`
}

// ImportShiftChartForDate imports shift chart data for all games on a given date.
func (a *SeasonsActivities) ImportShiftChartForDate(ctx context.Context, input ImportShiftChartForDateInput) (ImportShiftChartForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportShiftChartForDate", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)

	result := ImportShiftChartForDateResult{Origins: core.OriginCounts{}}

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

		scRes := resource.ShiftChart{Date: input.Date, GameID: game.ID}
		if !a.Storage.Exists(scRes.Path()) {
			return result, fmt.Errorf("shift chart file missing for game %s", game.ID.String())
		}

		sc, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scRes)
		if err != nil {
			return result, fmt.Errorf("read shift chart for game %s: %w", game.ID.String(), err)
		}
		result.Origins.Record(origin)

		if len(sc.Data) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertShiftBatchParams, len(sc.Data))
		for i, shift := range sc.Data {
			params[i] = shiftEntryToParams(shift)
		}

		var firstErr error
		var errIdx int
		results := a.ImportQueries.UpsertShiftBatch(ctx, params)
		results.Exec(func(i int, err error) {
			if err != nil && firstErr == nil {
				firstErr = err
				errIdx = i
			}
		})

		if firstErr != nil {
			return result, fmt.Errorf("upsert shift %d for game %s: %w",
				sc.Data[errIdx].ID, game.ID.String(), firstErr)
		}

		result.GamesProcessed++
		result.ShiftsImported += len(sc.Data)
	}

	logger.Debug("Imported shift chart for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesProcessed,
		"shifts", result.ShiftsImported)

	return result, nil
}

func shiftEntryToParams(s nhl.ShiftEntry) sqlcdb.UpsertShiftBatchParams {
	p := sqlcdb.UpsertShiftBatchParams{
		ID:          s.ID,
		GameID:      int64(s.GameID),
		PlayerID:    int64(s.PlayerID),
		TeamID:      int64(s.TeamID),
		Period:      int32(s.Period),
		StartTime:   s.StartTime,
		EndTime:     s.EndTime,
		Duration:    s.Duration,
		ShiftNumber: int32(s.ShiftNumber),
		TypeCode:    int32(s.TypeCode),
		DetailCode:  int32(s.DetailCode),
		EventNumber: s.EventNumber,
	}

	p.EventDescription = ptrToText(s.EventDescription)

	return p
}

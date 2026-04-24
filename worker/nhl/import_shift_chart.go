package nhl

import (
	"context"
	"fmt"
	"strconv"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportShiftChartForDateInput contains the parameters for importing shift chart data for a date.
type ImportShiftChartForDateInput struct {
	shared.DateSeasonInput
}

// ImportShiftChartForDateResult contains the results of importing shift chart data.
type ImportShiftChartForDateResult struct {
	GamesProcessed int               `json:"gamesProcessed"`
	ShiftsImported int               `json:"shiftsImported"`
	Origins        core.OriginCounts `json:"origins"`
}

// ImportShiftChartForDate imports shift chart data for all games on a given date.
func (a *ImportActivities) ImportShiftChartForDate(ctx context.Context, input ImportShiftChartForDateInput) (ImportShiftChartForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportShiftChartForDate")()

	logger := activity.GetLogger(ctx)

	result := ImportShiftChartForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(ctx, scheduleRes.Path()) {
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
		activity.RecordHeartbeat(ctx, fmt.Sprintf("shift-charts:game:%s", game.ID))

		scRes := resource.ShiftChart{Date: input.Date, GameID: game.ID}
		if !a.Storage.Exists(ctx, scRes.Path()) {
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

		if err := shared.ExecBatch(a.Queries.UpsertShiftBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("shift %d for game %s", sc.Data[i].ID, game.ID.String())
		}); err != nil {
			return result, err
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

func shiftEntryToParams(s nhlapi.ShiftEntry) sqlcdb.UpsertShiftBatchParams {
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
		TypeCode:    sqlcdb.ShiftType(strconv.Itoa(s.TypeCode)),
		DetailCode:  sqlcdb.ShiftDetail(strconv.Itoa(s.DetailCode)),
		EventNumber: s.EventNumber,
	}

	p.EventDescription = shared.PtrToText(s.EventDescription)

	return p
}

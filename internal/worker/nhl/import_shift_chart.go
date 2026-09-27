package nhl

import (
	"context"
	"fmt"
	"strconv"
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

// ImportShiftChartForDateInput contains the parameters for importing shift chart data for a date.
type ImportShiftChartForDateInput struct {
	shared.DateSeasonInput
}

// ImportShiftChartForDateResult contains the results of importing shift chart data.
type ImportShiftChartForDateResult struct {
	GamesProcessed   int               `json:"gamesProcessed"`
	ShiftsImported   int               `json:"shiftsImported"`
	SegmentsImported int64             `json:"segmentsImported"`
	Origins          core.OriginCounts `json:"origins"`
}

// ImportShiftChartForDate imports shift chart data for all games on a given
// date. After a game's shifts are upserted, its even_strength_segments rows
// are rebuilt from the stored shifts and the game's box-score rows, so box
// scores must be imported first (ImportDay does). Every step is idempotent,
// so a retried activity converges on the same rows.
func (a *ImportActivities) ImportShiftChartForDate(ctx context.Context, input ImportShiftChartForDateInput) (ImportShiftChartForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportShiftChartForDate")()

	logger := activity.GetLogger(ctx)

	result := ImportShiftChartForDateResult{Origins: core.OriginCounts{}}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(ctx, scheduleRes.Path()) {
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
		activity.RecordHeartbeat(ctx, fmt.Sprintf("shift-charts:game:%s", game.ID))

		if err := a.importGameShiftChart(ctx, input.Date, game.ID, &result); err != nil {
			return result, err
		}
	}

	logger.Debug("Imported shift chart for date",
		"date", input.Date.Format(config.DateFormat),
		"games", result.GamesProcessed,
		"shifts", result.ShiftsImported,
		"segments", result.SegmentsImported)

	return result, nil
}

// importGameShiftChart upserts one game's shifts and rebuilds its
// even-strength segments, adding to result. A game without shifts is
// skipped.
func (a *ImportActivities) importGameShiftChart(ctx context.Context, date time.Time, gameID nhlapi.GameID, result *ImportShiftChartForDateResult) error {
	scRes := resource.ShiftChart{Date: date, GameID: gameID}
	if !a.Storage.Exists(ctx, scRes.Path()) {
		return fmt.Errorf("shift chart file missing for game %s", gameID.String())
	}

	sc, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scRes)
	if err != nil {
		return fmt.Errorf("read shift chart for game %s: %w", gameID.String(), err)
	}
	result.Origins.Record(origin)

	if len(sc.Data) == 0 {
		return nil
	}

	params := make([]sqlcdb.UpsertShiftBatchParams, len(sc.Data))
	for i, shift := range sc.Data {
		params[i] = shiftEntryToParams(shift)
	}

	if err := shared.ExecBatch(a.Queries.UpsertShiftBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("shift %d for game %s", sc.Data[i].ID, gameID.String())
	}); err != nil {
		return err
	}

	segments, err := a.rebuildEvenStrengthSegments(ctx, gameID)
	if err != nil {
		return err
	}

	result.GamesProcessed++
	result.ShiftsImported += len(sc.Data)
	result.SegmentsImported += segments
	return nil
}

// rebuildEvenStrengthSegments replaces a game's even_strength_segments rows
// in one transaction, so readers never see a half-built game and a retry
// after a failure at any point rebuilds the same rows.
func (a *ImportActivities) rebuildEvenStrengthSegments(ctx context.Context, gameID nhlapi.GameID) (int64, error) {
	id := int64(gameID)
	var inserted int64
	err := a.Tx.InTx(ctx, func(q EvenStrengthSegmentRebuilder) error {
		if err := q.DeleteEvenStrengthSegmentsForGame(ctx, id); err != nil {
			return fmt.Errorf("delete: %w", err)
		}
		n, err := q.InsertEvenStrengthSegmentsForGame(ctx, id)
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
		inserted = n
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("rebuild even-strength segments for game %s: %w", gameID.String(), err)
	}
	return inserted, nil
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

package nhl

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/activity"
)

// ImportDayInput contains parameters for importing all data for a single day.
type ImportDayInput struct {
	Date      time.Time        `json:"date"`
	Season    int              `json:"season"`   // start year (e.g., 2023)
	SeasonID  int              `json:"seasonID"` // full ID (e.g., 20232024)
	TeamIDs   []yahoo.TeamInfo `json:"teamIDs"`
	TotalDays int              `json:"totalDays"` // total days in season for progress tracking
}

// ImportDay chains per-day import operations: boxscores, game stories, and Yahoo data.
// Player game logs are handled separately as a per-season batched activity.
func (a *ImportActivities) ImportDay(ctx context.Context, input ImportDayInput) (core.OriginCounts, error) {
	defer metrics.TrackActivityDuration("ImportDay")()

	logger := activity.GetLogger(ctx)
	counts := core.OriginCounts{}
	activity.RecordHeartbeat(ctx, "boxscores")

	boxscoreInput := ImportBoxscoresForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	boxResult, err := a.ImportBoxscoresForDate(ctx, boxscoreInput)
	if err != nil {
		return counts, fmt.Errorf("import boxscores for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(boxResult.Origins)
	activity.RecordHeartbeat(ctx, "game-stories")

	gameStoryInput := ImportGameStoryForDateInput{Season: input.SeasonID, Date: input.Date}
	gsResult, err := a.ImportGameStoryForDate(ctx, gameStoryInput)
	if err != nil {
		return counts, fmt.Errorf("import game story for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(gsResult.Origins)
	activity.RecordHeartbeat(ctx, "play-by-play")

	pbpInput := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	pbpResult, err := a.ImportPlayByPlayForDate(ctx, pbpInput)
	if err != nil {
		return counts, fmt.Errorf("import play-by-play for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(pbpResult.Origins)
	activity.RecordHeartbeat(ctx, "shift-charts")

	scInput := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	scResult, err := a.ImportShiftChartForDate(ctx, scInput)
	if err != nil {
		return counts, fmt.Errorf("import shift chart for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(scResult.Origins)
	activity.RecordHeartbeat(ctx, "season-series")

	ssInput := ImportSeasonSeriesForDateInput{Date: input.Date}
	ssResult, err := a.ImportSeasonSeriesForDate(ctx, ssInput)
	if err != nil {
		return counts, fmt.Errorf("import season series for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(ssResult.Origins)
	activity.RecordHeartbeat(ctx, "standings")

	standingsInput := shared.DateSeasonInput{Date: input.Date, Season: input.Season}
	if err := a.ImportStandingsForDate(ctx, standingsInput); err != nil {
		return counts, fmt.Errorf("import standings for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	activity.RecordHeartbeat(ctx, "yahoo")

	if len(input.TeamIDs) > 0 {
		yahooInput := yahoo.ImportYahooDataForDateInput{Season: input.Season, Teams: input.TeamIDs, Date: input.Date}
		yahooResult, err := a.Yahoo.ImportYahooDataForDate(ctx, yahooInput)
		if err != nil {
			return counts, fmt.Errorf("import Yahoo data for %s: %w", input.Date.Format(config.DateFormat), err)
		}
		counts.Add(yahooResult.Origins)
	}

	logger.Debug("Imported day data", "date", input.Date.Format(config.DateFormat))
	return counts, nil
}

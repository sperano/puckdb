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
	result, err := a.importDay(ctx, input)
	return result.Origins, err
}

// dayImportResult is what one importDay run did.
type dayImportResult struct {
	Origins core.OriginCounts
	// SkippedRows counts game-story and season-series rows that failed and
	// were skipped instead of failing the day (a scratched player not yet in
	// players, a team not yet in season_teams), plus Yahoo files that exist
	// but could not be read. They may succeed on a later run with the same
	// files.
	SkippedRows int
}

// importDay is ImportDay without its activity metric, shared with
// ImportDayIfChanged.
func (a *ImportActivities) importDay(ctx context.Context, input ImportDayInput) (dayImportResult, error) {
	logger := activity.GetLogger(ctx)
	counts := core.OriginCounts{}
	result := dayImportResult{Origins: counts}
	activity.RecordHeartbeat(ctx, "boxscores")

	boxscoreInput := ImportBoxscoresForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	boxResult, err := a.ImportBoxscoresForDate(ctx, boxscoreInput)
	if err != nil {
		return result, fmt.Errorf("import boxscores for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(boxResult.Origins)
	activity.RecordHeartbeat(ctx, "game-stories")

	gameStoryInput := ImportGameStoryForDateInput{Season: input.SeasonID, Date: input.Date}
	gsResult, err := a.ImportGameStoryForDate(ctx, gameStoryInput)
	if err != nil {
		return result, fmt.Errorf("import game story for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(gsResult.Origins)
	result.SkippedRows += len(gsResult.Errors)
	activity.RecordHeartbeat(ctx, "play-by-play")

	pbpInput := ImportPlayByPlayForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	pbpResult, err := a.ImportPlayByPlayForDate(ctx, pbpInput)
	if err != nil {
		return result, fmt.Errorf("import play-by-play for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(pbpResult.Origins)
	activity.RecordHeartbeat(ctx, "shift-charts")

	scInput := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: input.Date, Season: input.Season}}
	scResult, err := a.ImportShiftChartForDate(ctx, scInput)
	if err != nil {
		return result, fmt.Errorf("import shift chart for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(scResult.Origins)
	activity.RecordHeartbeat(ctx, "season-series")

	ssInput := ImportSeasonSeriesForDateInput{Date: input.Date}
	ssResult, err := a.ImportSeasonSeriesForDate(ctx, ssInput)
	if err != nil {
		return result, fmt.Errorf("import season series for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	counts.Add(ssResult.Origins)
	result.SkippedRows += len(ssResult.Errors)
	activity.RecordHeartbeat(ctx, "standings")

	standingsInput := shared.DateSeasonInput{Date: input.Date, Season: input.Season}
	if err := a.ImportStandingsForDate(ctx, standingsInput); err != nil {
		return result, fmt.Errorf("import standings for %s: %w", input.Date.Format(config.DateFormat), err)
	}
	activity.RecordHeartbeat(ctx, "yahoo")

	if len(input.TeamIDs) > 0 {
		yahooInput := yahoo.ImportYahooDataForDateInput{Season: input.Season, Teams: input.TeamIDs, Date: input.Date}
		yahooResult, err := a.Yahoo.ImportYahooDataForDate(ctx, yahooInput)
		if err != nil {
			return result, fmt.Errorf("import Yahoo data for %s: %w", input.Date.Format(config.DateFormat), err)
		}
		counts.Add(yahooResult.Origins)
		result.SkippedRows += yahooResult.ReadFailures
	}

	logger.Debug("Imported day data", "date", input.Date.Format(config.DateFormat))
	return result, nil
}

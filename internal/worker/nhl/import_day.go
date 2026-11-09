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

// ImportDay chains per-day import operations in order: boxscores, game
// stories, play-by-play, shift charts, season series, standings, and Yahoo
// data (when the day has Yahoo teams configured). Player game logs are
// handled separately as a per-season batched activity.
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
// ImportDayIfChanged. It runs dayImportSteps in order and stops at the first
// failure; the result then holds what the steps before it imported.
func (a *ImportActivities) importDay(ctx context.Context, input ImportDayInput) (dayImportResult, error) {
	result := dayImportResult{Origins: core.OriginCounts{}}
	for _, step := range a.dayImportSteps(input) {
		activity.RecordHeartbeat(ctx, step.heartbeat)
		stepResult, err := step.run(ctx)
		if err != nil {
			return result, fmt.Errorf("import %s for %s: %w", step.what, input.Date.Format(config.DateFormat), err)
		}
		result.Origins.Add(stepResult.Origins)
		result.SkippedRows += stepResult.SkippedRows
	}
	activity.GetLogger(ctx).Debug("Imported day data", "date", input.Date.Format(config.DateFormat))
	return result, nil
}

// dayImportStep is one importer of a day: the heartbeat recorded before it
// runs, the data it names in an error, and the import itself.
type dayImportStep struct {
	heartbeat string
	what      string
	run       func(context.Context) (dayImportResult, error)
}

// dayImportSteps lists a day's importers in the order they must run (game
// rows before the rows that reference them). The Yahoo step imports nothing
// when the day has no Yahoo teams configured.
func (a *ImportActivities) dayImportSteps(input ImportDayInput) []dayImportStep {
	dateSeason := shared.DateSeasonInput{Date: input.Date, Season: input.Season}
	return []dayImportStep{
		{heartbeat: "boxscores", what: "boxscores", run: func(ctx context.Context) (dayImportResult, error) {
			r, err := a.ImportBoxscoresForDate(ctx, ImportBoxscoresForDateInput{dateSeason})
			return dayImportResult{Origins: r.Origins}, err
		}},
		{heartbeat: "game-stories", what: "game story", run: func(ctx context.Context) (dayImportResult, error) {
			r, err := a.ImportGameStoryForDate(ctx, ImportGameStoryForDateInput{Season: input.SeasonID, Date: input.Date})
			if err != nil {
				return dayImportResult{}, err
			}
			return dayImportResult{Origins: r.Origins, SkippedRows: len(r.Errors)}, nil
		}},
		{heartbeat: "play-by-play", what: "play-by-play", run: func(ctx context.Context) (dayImportResult, error) {
			r, err := a.ImportPlayByPlayForDate(ctx, ImportPlayByPlayForDateInput{dateSeason})
			return dayImportResult{Origins: r.Origins}, err
		}},
		{heartbeat: "shift-charts", what: "shift chart", run: func(ctx context.Context) (dayImportResult, error) {
			r, err := a.ImportShiftChartForDate(ctx, ImportShiftChartForDateInput{dateSeason})
			return dayImportResult{Origins: r.Origins}, err
		}},
		{heartbeat: "season-series", what: "season series", run: func(ctx context.Context) (dayImportResult, error) {
			r, err := a.ImportSeasonSeriesForDate(ctx, ImportSeasonSeriesForDateInput{Date: input.Date})
			if err != nil {
				return dayImportResult{}, err
			}
			return dayImportResult{Origins: r.Origins, SkippedRows: len(r.Errors)}, nil
		}},
		{heartbeat: "standings", what: "standings", run: func(ctx context.Context) (dayImportResult, error) {
			return dayImportResult{}, a.ImportStandingsForDate(ctx, dateSeason)
		}},
		{heartbeat: "yahoo", what: "Yahoo data", run: func(ctx context.Context) (dayImportResult, error) {
			if len(input.TeamIDs) == 0 {
				return dayImportResult{}, nil
			}
			r, err := a.Yahoo.ImportYahooDataForDate(ctx, yahooDateInput(input))
			return dayImportResult{Origins: r.Origins, SkippedRows: r.ReadFailures}, err
		}},
	}
}

// yahooDateInput is the Yahoo part of a day import, shared by the import and
// its fingerprint (dayInputs) so both name the same files.
func yahooDateInput(input ImportDayInput) yahoo.ImportYahooDataForDateInput {
	return yahoo.ImportYahooDataForDateInput{Season: input.Season, Teams: input.TeamIDs, Date: input.Date}
}

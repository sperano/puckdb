package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/metrics"
	"go.temporal.io/sdk/activity"
)

// ImportDayInput contains parameters for importing all data for a single day.
type ImportDayInput struct {
	Date      time.Time  `json:"date"`
	Season    int        `json:"season"`    // start year (e.g., 2023)
	SeasonID  int        `json:"seasonID"`  // full ID (e.g., 20232024)
	TeamIDs   []TeamInfo `json:"teamIDs"`
	TotalDays int        `json:"totalDays"` // total days in season for progress tracking
}

// ImportDay chains per-day import operations: boxscores, game stories, and Yahoo data.
// Player game logs are handled separately as a per-season batched activity.
func (a *SeasonsActivities) ImportDay(ctx context.Context, input ImportDayInput) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportDay", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "boxscores")

	// Import boxscores
	boxscoreInput := ImportBoxscoresForDateInput{Date: input.Date, Season: input.Season}
	if _, err := a.ImportBoxscoresForDate(ctx, boxscoreInput); err != nil {
		return fmt.Errorf("import boxscores for %s: %w", input.Date.Format("2006-01-02"), err)
	}
	activity.RecordHeartbeat(ctx, "game-stories")

	// Import game stories
	gameStoryInput := ImportGameStoryForDateInput{Season: input.SeasonID, Date: input.Date}
	if _, err := a.ImportGameStoryForDate(ctx, gameStoryInput); err != nil {
		return fmt.Errorf("import game story for %s: %w", input.Date.Format("2006-01-02"), err)
	}
	activity.RecordHeartbeat(ctx, "play-by-play")

	// Import play-by-play
	pbpInput := ImportPlayByPlayForDateInput{Date: input.Date, Season: input.Season}
	if _, err := a.ImportPlayByPlayForDate(ctx, pbpInput); err != nil {
		return fmt.Errorf("import play-by-play for %s: %w", input.Date.Format("2006-01-02"), err)
	}
	activity.RecordHeartbeat(ctx, "shift-charts")

	// Import shift charts
	scInput := ImportShiftChartForDateInput{Date: input.Date, Season: input.Season}
	if _, err := a.ImportShiftChartForDate(ctx, scInput); err != nil {
		return fmt.Errorf("import shift chart for %s: %w", input.Date.Format("2006-01-02"), err)
	}
	activity.RecordHeartbeat(ctx, "yahoo")

	// Import Yahoo data if teams configured
	if len(input.TeamIDs) > 0 {
		yahooInput := ImportYahooDataForDateInput{Season: input.Season, Teams: input.TeamIDs, Date: input.Date}
		if _, err := a.ImportYahooDataForDate(ctx, yahooInput); err != nil {
			return fmt.Errorf("import Yahoo data for %s: %w", input.Date.Format("2006-01-02"), err)
		}
	}

	logger.Debug("Imported day data", "date", input.Date.Format("2006-01-02"))
	return nil
}

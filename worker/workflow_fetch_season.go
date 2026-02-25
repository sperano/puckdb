package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// Group index for FetchSeasonWorkflow progress
const GroupFetchSeasonData = 0

// NewFetchSeasonProgressReport creates the progress structure for a single season.
// Uses countDaysInSeason for consistency with ExtractBoxscorePlayers progress.
func NewFetchSeasonProgressReport(season SeasonInfo) *ProgressReport {
	total := countDaysInSeason(season)
	return &ProgressReport{
		Total: total,
		Groups: []ProgressGroup{
			{Header: fmt.Sprintf("Fetching %s...", season.Label()), Bars: []ProgressBar{{Total: total}}},
		},
	}
}

// FetchSeasonWorkflow fetches all data for a single season.
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func FetchSeasonWorkflow(ctx workflow.Context, season SeasonInfo) error {
	logger := workflow.GetLogger(ctx)

	logger.Info("FetchSeasonWorkflow started",
		"startYear", season.StartYear(),
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat))

	// Set up progress tracking
	tracker := NewReportTracker(NewFetchSeasonProgressReport(season))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonData)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Look up Yahoo config and download league/team data
	// Note: Yahoo downloads are not tracked in progress - only days are tracked for consistency
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.StartYear()]; inYahoo {
			// Build team list and fetch leagues
			for _, league := range yahooCfg.Leagues {
				if err := workflow.ExecuteActivity(ctx, FetchLeagueActivity, season.StartYear(), league.LeagueID).Get(ctx, nil); err != nil {
					return err
				}

				for _, teamid := range league.TeamIDs {
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}

			// Fetch all teams in one batched activity
			if len(teamIDs) > 0 {
				input := FetchTeamsInput{Season: season.StartYear(), Teams: teamIDs}
				if err := workflow.ExecuteActivity(ctx, FetchTeamsActivity, input).Get(ctx, nil); err != nil {
					return err
				}
			}
		}
	}

	// Calculate days to process (up to today)
	endDate := effectiveEndDate(season.EndDate)
	numDays := countDays(season.StartDate, endDate)
	concurrency := getDayConcurrency()

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", concurrency)

	// Process days in parallel using RunWorkerPool
	// FetchDayActivity needs longer timeout due to rate-limited Yahoo downloads
	dayCtx := workflow.WithActivityOptions(ctx, fetchDayActivityOptions())
	startDate := season.StartDate
	startYear := season.StartYear()
	err = tracker.RunWorkerPool(ctx, GroupFetchSeasonData, 0, numDays, concurrency, func(_ workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		dayInput := FetchDayInput{
			Day:       day,
			StartYear: startYear,
			TeamIDs:   teamIDs,
			DayIndex:  i,
			TotalDays: numDays,
		}
		return workflow.ExecuteActivity(dayCtx, FetchDayActivity, dayInput)
	}, nil)
	if err != nil {
		return err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonData, fmt.Sprintf("Fetched %s", season.Label()))

	logger.Info("FetchSeasonWorkflow completed",
		"startYear", season.StartYear())
	return nil
}

// WorkflowIDFetchSeason returns the workflow ID for a single season fetch.
func WorkflowIDFetchSeason(startYear int) string {
	return fmt.Sprintf("fetch-season-%d", startYear)
}

// FetchDayInput contains parameters for FetchDayActivity.
// TeamIDs are pre-computed by the parent workflow.
type FetchDayInput struct {
	Day       time.Time
	StartYear int
	TeamIDs   []TeamInfo // Teams to fetch Yahoo data for (empty if no Yahoo config)
	DayIndex  int        // 0-based index for progress tracking
	TotalDays int        // Total days in season for progress tracking
}


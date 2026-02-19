package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// FetchSeasonInput contains parameters for fetching a single season.
type FetchSeasonInput struct {
	Season SeasonInfo
}

// FetchSeasonWorkflow fetches all data for a single season.
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func FetchSeasonWorkflow(ctx workflow.Context, input *FetchSeasonInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("FetchSeasonWorkflow started",
		"startYear", season.StartYear,
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat))

	// Set up progress tracking
	total := countDownloadTasksForSeason(season)
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Look up Yahoo config and download league/team data
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.StartYear]; inYahoo {
			// Build team list and fetch leagues
			for _, league := range yahooCfg.Leagues {
				if err := workflow.ExecuteActivity(ctx, FetchLeagueActivity, season.StartYear, league.LeagueID).Get(ctx, nil); err != nil {
					return err
				}
				tracker.Increment()

				for _, teamid := range league.TeamIDs {
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}

			// Fetch all teams in one batched activity
			if len(teamIDs) > 0 {
				input := FetchTeamsInput{Season: season.StartYear, Teams: teamIDs}
				if err := workflow.ExecuteActivity(ctx, FetchTeamsActivity, input).Get(ctx, nil); err != nil {
					return err
				}
				tracker.Increment()
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
	startDate := season.StartDate
	startYear := season.StartYear
	err = tracker.RunWorkerPool(ctx, numDays, concurrency, func(ctx workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		dayInput := &FetchDayInput{
			Day:       day,
			StartYear: startYear,
			TeamIDs:   teamIDs,
		}
		return workflow.ExecuteActivity(ctx, FetchDayActivity, dayInput)
	})
	if err != nil {
		return err
	}

	logger.Info("FetchSeasonWorkflow completed",
		"startYear", season.StartYear,
		"completed", tracker.progress.Completed)
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
}


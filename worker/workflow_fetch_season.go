package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
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
			for _, league := range yahooCfg.Leagues {
				// Fetch league
				if err := workflow.ExecuteActivity(ctx, FetchLeagueActivity, season.StartYear, league.LeagueID).Get(ctx, nil); err != nil {
					return err
				}
				tracker.Increment()

				// Fetch teams
				for _, teamid := range league.TeamIDs {
					if err := workflow.ExecuteActivity(ctx, FetchTeamActivity, season.StartYear, league.LeagueID, teamid).Get(ctx, nil); err != nil {
						return err
					}
					tracker.Increment()
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}
		}
	}

	// Determine end date (don't download future days)
	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	// Calculate number of days to process
	numDays := int(end.Sub(season.StartDate).Hours()/config.HoursPerDay) + 1
	if numDays < 0 {
		numDays = 0
	}

	// Get day concurrency from config
	dayConcurrency := viper.GetInt(config.FlagDayConcurrency)
	if dayConcurrency <= 0 {
		dayConcurrency = 20
	}

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", dayConcurrency)

	// Process days in parallel using RunWorkerPool
	startDate := season.StartDate
	startYear := season.StartYear
	err = tracker.RunWorkerPool(ctx, numDays, dayConcurrency, func(ctx workflow.Context, i int) workflow.Future {
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

// WorkflowIDFetchDay returns the workflow ID for a single day fetch.
func WorkflowIDFetchDay(startYear int, day time.Time) string {
	return fmt.Sprintf("fetch-day-%d-%s", startYear, day.Format(config.DateFormat))
}

// FetchDayWorkflowInput contains parameters for the FetchDayWorkflow.
// The workflow looks up Yahoo config itself to determine which teams to fetch.
type FetchDayWorkflowInput struct {
	Day       time.Time
	StartYear int
}

// FetchDayInput contains parameters for FetchDayActivity.
// TeamIDs are pre-computed by the parent workflow.
type FetchDayInput struct {
	Day       time.Time
	StartYear int
	TeamIDs   []TeamInfo // Teams to fetch Yahoo data for (empty if no Yahoo config)
}

// TeamInfo identifies a team for Yahoo downloads.
type TeamInfo struct {
	LeagueID int
	TeamID   int
}

// FetchDayWorkflow fetches all data for a single day.
// This includes NHL boxscores and Yahoo rosters/summaries for all configured teams.
// It looks up the Yahoo config to determine which teams to fetch.
func FetchDayWorkflow(ctx workflow.Context, input *FetchDayWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	// Look up Yahoo config for this season
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[input.StartYear]; inYahoo {
			for _, league := range yahooCfg.Leagues {
				for _, teamid := range league.TeamIDs {
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}
		}
	}

	logger.Info("FetchDayWorkflow started",
		"day", input.Day.Format(config.DateFormat),
		"startYear", input.StartYear,
		"numTeams", len(teamIDs))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Fetch daily schedule (boxscores)
	if err := workflow.ExecuteActivity(ctx, FetchDailyScheduleActivity, input.Day).Get(ctx, nil); err != nil {
		return err
	}

	// Fetch Yahoo rosters and summaries for each team
	for _, team := range teamIDs {
		if err := workflow.ExecuteActivity(ctx, FetchRosterForTeamOnDayActivity,
			input.StartYear, team.LeagueID, team.TeamID, input.Day).Get(ctx, nil); err != nil {
			return err
		}
		if err := workflow.ExecuteActivity(ctx, FetchTeamSummaryForTeamOnDayActivity,
			input.StartYear, team.LeagueID, team.TeamID, input.Day).Get(ctx, nil); err != nil {
			return err
		}
	}

	return nil
}


package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// DownloadSeasonInput contains parameters for downloading a single season.
type DownloadSeasonInput struct {
	Season SeasonInfo
}

// DownloadSeasonWorkflow downloads all data for a single season.
// This isolates the workflow history for each season, preventing the parent
// workflow from exceeding Temporal's history limit.
func DownloadSeasonWorkflow(ctx workflow.Context, input *DownloadSeasonInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("DownloadSeasonWorkflow started",
		"startYear", season.StartYear,
		"startDate", season.StartDate,
		"endDate", season.EndDate)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	total := countDownloadTasksForSeason(season)
	tracker := NewProgressTracker(total)
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	futures := collectDownloadFuturesForSeasonImpl(ctx, season)

	return tracker.WaitAll(ctx, futures)
}

// WorkflowIDDownloadSeason returns the workflow ID for a single season download.
func WorkflowIDDownloadSeason(startYear int) string {
	return fmt.Sprintf("download-season-%d", startYear)
}

// WorkflowIDDownloadDay returns the workflow ID for a single day download.
func WorkflowIDDownloadDay(startYear int, day time.Time) string {
	return fmt.Sprintf("download-day-%d-%s", startYear, day.Format("2006-01-02"))
}

// DownloadDayInput contains parameters for downloading all data for a single day.
type DownloadDayInput struct {
	Day       time.Time
	StartYear int
	TeamIDs   []TeamInfo // Teams to download Yahoo data for (empty if no Yahoo config)
}

// TeamInfo identifies a team for Yahoo downloads.
type TeamInfo struct {
	LeagueID int
	TeamID   int
}

// DownloadDayWorkflow downloads all data for a single day.
// This includes NHL boxscores and Yahoo rosters/summaries for all configured teams.
func DownloadDayWorkflow(ctx workflow.Context, input *DownloadDayInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("DownloadDayWorkflow started",
		"day", input.Day.Format("2006-01-02"),
		"startYear", input.StartYear,
		"numTeams", len(input.TeamIDs))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Download daily schedule (boxscores)
	if err := workflow.ExecuteActivity(ctx, DownloadDailySchedule, input.Day).Get(ctx, nil); err != nil {
		return err
	}

	// Download Yahoo rosters and summaries for each team
	for _, team := range input.TeamIDs {
		if err := workflow.ExecuteActivity(ctx, DownloadRosterForTeamOnDay,
			input.StartYear, team.LeagueID, team.TeamID, input.Day).Get(ctx, nil); err != nil {
			return err
		}
		if err := workflow.ExecuteActivity(ctx, DownloadTeamSummaryForTeamOnDay,
			input.StartYear, team.LeagueID, team.TeamID, input.Day).Get(ctx, nil); err != nil {
			return err
		}
	}

	return nil
}

// collectDownloadFuturesForSeasonImpl collects all download futures for a single season.
// This is the implementation used by DownloadSeasonWorkflow.
func collectDownloadFuturesForSeasonImpl(ctx workflow.Context, season SeasonInfo) []workflow.Future {
	futures := make([]workflow.Future, 0, countDownloadTasksForSeason(season))

	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	// Check Yahoo config for this season
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.StartYear]; inYahoo {
			// Add league and team downloads (one-time per season)
			for _, league := range yahooCfg.Leagues {
				ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
				future := workflow.ExecuteActivity(ctxa, DownloadLeague, season.StartYear, league.LeagueID)
				futures = append(futures, future)
				for _, teamid := range league.TeamIDs {
					future := workflow.ExecuteActivity(ctxa, DownloadTeam, season.StartYear, league.LeagueID, teamid)
					futures = append(futures, future)
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}
		}
	}

	// Spawn per-day child workflows for granular progress tracking
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctxo := withChildOptions(ctx, WorkflowIDDownloadDay(season.StartYear, day))
		input := &DownloadDayInput{
			Day:       day,
			StartYear: season.StartYear,
			TeamIDs:   teamIDs,
		}
		futures = append(futures, workflow.ExecuteChildWorkflow(ctxo, DownloadDayWorkflow, input))
	}

	return futures
}

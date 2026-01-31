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
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func DownloadSeasonWorkflow(ctx workflow.Context, input *DownloadSeasonInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("DownloadSeasonWorkflow started",
		"startYear", season.StartYear,
		"startDate", season.StartDate.Format("2006-01-02"),
		"endDate", season.EndDate.Format("2006-01-02"))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Look up Yahoo config and download league/team data
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.StartYear]; inYahoo {
			for _, league := range yahooCfg.Leagues {
				// Download league
				if err := workflow.ExecuteActivity(ctx, DownloadLeague, season.StartYear, league.LeagueID).Get(ctx, nil); err != nil {
					return err
				}
				// Download teams
				for _, teamid := range league.TeamIDs {
					if err := workflow.ExecuteActivity(ctx, DownloadTeam, season.StartYear, league.LeagueID, teamid).Get(ctx, nil); err != nil {
						return err
					}
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

	// Process all days using DownloadDayActivity
	daysProcessed := 0
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		dayInput := &DownloadDayInput{
			Day:       day,
			StartYear: season.StartYear,
			TeamIDs:   teamIDs,
		}
		if err := workflow.ExecuteActivity(ctx, DownloadDayActivity, dayInput).Get(ctx, nil); err != nil {
			return err
		}
		daysProcessed++
	}

	logger.Info("DownloadSeasonWorkflow completed",
		"startYear", season.StartYear,
		"daysProcessed", daysProcessed)
	return nil
}

// WorkflowIDDownloadSeason returns the workflow ID for a single season download.
func WorkflowIDDownloadSeason(startYear int) string {
	return fmt.Sprintf("download-season-%d", startYear)
}

// WorkflowIDDownloadDay returns the workflow ID for a single day download.
func WorkflowIDDownloadDay(startYear int, day time.Time) string {
	return fmt.Sprintf("download-day-%d-%s", startYear, day.Format("2006-01-02"))
}

// DownloadDayWorkflowInput contains parameters for the DownloadDayWorkflow.
// The workflow looks up Yahoo config itself to determine which teams to download.
type DownloadDayWorkflowInput struct {
	Day       time.Time
	StartYear int
}

// DownloadDayInput contains parameters for DownloadDayActivity.
// TeamIDs are pre-computed by the parent workflow.
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
// It looks up the Yahoo config to determine which teams to download.
func DownloadDayWorkflow(ctx workflow.Context, input *DownloadDayWorkflowInput) error {
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

	logger.Info("DownloadDayWorkflow started",
		"day", input.Day.Format("2006-01-02"),
		"startYear", input.StartYear,
		"numTeams", len(teamIDs))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Download daily schedule (boxscores)
	if err := workflow.ExecuteActivity(ctx, DownloadDailySchedule, input.Day).Get(ctx, nil); err != nil {
		return err
	}

	// Download Yahoo rosters and summaries for each team
	for _, team := range teamIDs {
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


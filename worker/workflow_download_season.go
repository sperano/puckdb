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

// collectDownloadFuturesForSeasonImpl collects all download futures for a single season.
// This is the implementation used by DownloadSeasonWorkflow.
func collectDownloadFuturesForSeasonImpl(ctx workflow.Context, season SeasonInfo) []workflow.Future {
	futures := make([]workflow.Future, 0, countDownloadTasksForSeason(season))

	// Spawn per-day activities for granular progress tracking
	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
		futures = append(futures, workflow.ExecuteActivity(ctxa, DownloadDailySchedule, day))
	}

	// Only process Yahoo activities if the season is in Yahoo config
	yahooConfig, err := config.GetSeasonsConfig()
	if err != nil {
		return futures
	}
	yahooCfg, inYahoo := yahooConfig[season.StartYear]
	if !inYahoo {
		return futures
	}

	// Add league and team downloads (Yahoo)
	for _, league := range yahooCfg.Leagues {
		ctxa := workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctxa, DownloadLeague, season.StartYear, league.LeagueID)
		futures = append(futures, future)
		for _, teamid := range league.TeamIDs {
			future := workflow.ExecuteActivity(ctxa, DownloadTeam, season.StartYear, league.LeagueID, teamid)
			futures = append(futures, future)
			ctxo := withChildOptions(ctx, WorkflowIDDownloadRostersForTeam(season.StartYear, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, DownloadRosterForTeamWorkflow,
				season.StartDate, season.EndDate, season.StartYear, league.LeagueID, teamid)
			futures = append(futures, future)
			ctxo = withChildOptions(ctx, WorkflowIDDownloadTeamSummariesForTeam(season.StartYear, league, teamid))
			future = workflow.ExecuteChildWorkflow(ctxo, DownloadTeamSummariesForTeamWorkflow,
				season.StartDate, season.EndDate, season.StartYear, league.LeagueID, teamid)
			futures = append(futures, future)
		}
	}

	return futures
}

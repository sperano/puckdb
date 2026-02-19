package worker

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonInput contains parameters for importing a single season.
type ImportSeasonInput struct {
	Season SeasonInfo
}

// ImportSeasonWorkflow imports all boxscores for a single season.
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func ImportSeasonWorkflow(ctx workflow.Context, input *ImportSeasonInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("ImportSeasonWorkflow started",
		"startYear", season.StartYear,
		"startDate", season.StartDate.Format(config.DateFormat),
		"endDate", season.EndDate.Format(config.DateFormat))

	tracker := NewProgressTracker(countDaysInSeason(season))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Import Yahoo leagues and teams (returns team IDs for per-day processing)
	teamIDs, err := importYahooLeaguesAndTeams(ctx, season.StartYear)
	if err != nil {
		return err
	}

	// Process each day's data in parallel
	if err := processDaysInParallel(ctx, tracker, season, teamIDs); err != nil {
		return err
	}

	logger.Info("ImportSeasonWorkflow completed",
		"startYear", season.StartYear,
		"completed", tracker.progress.Completed)
	return nil
}

// importYahooLeaguesAndTeams imports Yahoo league and team metadata.
// Returns the list of team IDs to process for per-day Yahoo data import.
func importYahooLeaguesAndTeams(ctx workflow.Context, startYear int) ([]TeamInfo, error) {
	logger := workflow.GetLogger(ctx)
	var teamIDs []TeamInfo

	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err != nil {
		return nil, nil // No Yahoo config, continue without Yahoo data
	}

	yahooCfg, hasYahoo := yahooConfig[startYear]
	if !hasYahoo {
		return nil, nil // Season not in Yahoo config
	}

	logger.Info("Importing Yahoo data for season", "startYear", startYear)

	// Import each league and collect team IDs
	for _, league := range yahooCfg.Leagues {
		if err := importYahooLeague(ctx, startYear, league.LeagueID); err != nil {
			return nil, err
		}

		for _, teamID := range league.TeamIDs {
			teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamID})
		}
	}

	// Import all teams in one batched activity
	if len(teamIDs) > 0 {
		if err := importYahooTeams(ctx, startYear, teamIDs); err != nil {
			return nil, err
		}
	}

	return teamIDs, nil
}

// importYahooLeague imports a single Yahoo fantasy league.
func importYahooLeague(ctx workflow.Context, season, leagueID int) error {
	input := ImportYahooLeagueInput{
		Season:   season,
		LeagueID: leagueID,
	}
	return workflow.ExecuteActivity(ctx, ImportYahooLeagueActivity, input).Get(ctx, nil)
}

// importYahooTeams imports Yahoo team metadata in a single batch.
func importYahooTeams(ctx workflow.Context, season int, teams []TeamInfo) error {
	input := ImportYahooTeamsInput{
		Season: season,
		Teams:  teams,
	}
	return workflow.ExecuteActivity(ctx, ImportYahooTeamsActivity, input).Get(ctx, nil)
}

// processDaysInParallel processes boxscores and Yahoo data for each day in the season.
func processDaysInParallel(ctx workflow.Context, tracker *ProgressTracker, season SeasonInfo, teamIDs []TeamInfo) error {
	logger := workflow.GetLogger(ctx)

	endDate := effectiveEndDate(season.EndDate)
	numDays := countDays(season.StartDate, endDate)
	concurrency := getDayConcurrency()

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", concurrency)

	return tracker.RunWorkerPool(ctx, numDays, concurrency, func(ctx workflow.Context, i int) workflow.Future {
		day := season.StartDate.AddDate(0, 0, i)
		return importDayData(ctx, season.StartYear, day, teamIDs)
	})
}

// importDayData chains boxscore and Yahoo data imports for a single day.
func importDayData(ctx workflow.Context, season int, day time.Time, teamIDs []TeamInfo) workflow.Future {
	future, settable := workflow.NewFuture(ctx)

	workflow.Go(ctx, func(ctx workflow.Context) {
		// First: import boxscores
		if err := importBoxscoresForDate(ctx, season, day); err != nil {
			settable.SetError(err)
			return
		}

		// Second: import Yahoo team data if teams configured
		if len(teamIDs) > 0 {
			if err := importYahooDataForDate(ctx, season, day, teamIDs); err != nil {
				settable.SetError(err)
				return
			}
		}

		settable.Set(nil, nil)
	})

	return future
}

// importBoxscoresForDate imports boxscores for a single date.
func importBoxscoresForDate(ctx workflow.Context, season int, day time.Time) error {
	input := ImportBoxscoresForDateInput{
		Date:   day,
		Season: season,
	}
	return workflow.ExecuteActivity(ctx, ImportBoxscoresForDateActivity, input).Get(ctx, nil)
}

// importYahooDataForDate imports Yahoo team summaries and rosters for a single date.
func importYahooDataForDate(ctx workflow.Context, season int, day time.Time, teams []TeamInfo) error {
	input := ImportYahooDataForDateInput{
		Season: season,
		Teams:  teams,
		Date:   day,
	}
	return workflow.ExecuteActivity(ctx, ImportYahooDataForDateActivity, input).Get(ctx, nil)
}

// WorkflowIDImportSeason returns the workflow ID for a single season import.
func WorkflowIDImportSeason(startYear int) string {
	return fmt.Sprintf("import-season-%d", startYear)
}

package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"go.temporal.io/sdk/workflow"
)

// GroupImportDays is the single progress group for ImportSeasonWorkflow.
const GroupImportDays = 0

// NewImportSeasonProgressReport creates the progress structure for a single season import.
func NewImportSeasonProgressReport(season nhl.SeasonInfo) *ProgressReport {
	total, _ := countDaysInSeason(season)
	return &ProgressReport{
		Total: total,
		Groups: []ProgressGroup{
			{Header: fmt.Sprintf("Importing games for %s...", season.Label()), Bars: []ProgressBar{{Total: total}}},
		},
	}
}

// ImportSeasonWorkflow imports day-level data for a single season.
// Per-day boxscores, game stories, and Yahoo data via ImportDay.
// Player game log imports are handled separately by ImportSeasonPlayerLogsWorkflow.
func ImportSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ImportSeasonWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	tracker := NewReportTracker(NewImportSeasonProgressReport(season))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Yahoo setup (not tracked in progress)
	teamIDs, err := importYahooLeaguesAndTeams(ctx, season.ID.StartYear())
	if err != nil {
		return nil, err
	}

	// --- Import days ---
	tracker.StartGroup(ctx, GroupImportDays)

	endDate := effectiveEndDate(season.StandingsEnd.Time)
	numDays := countDays(season.StandingsStart.Time, endDate)
	dayConcurrency := getDayConcurrency()
	startDate := season.StandingsStart.Time
	startYear := season.ID.StartYear()
	seasonID := season.ID.ToInt()

	var sa *SeasonsActivities
	err = tracker.RunWorkerPool(ctx, GroupImportDays, 0, numDays, dayConcurrency,
		func(_ workflow.Context, i int) workflow.Future {
			day := startDate.AddDate(0, 0, i)
			input := ImportDayInput{
				Date:      day,
				Season:    startYear,
				SeasonID:  seasonID,
				TeamIDs:   teamIDs,
				TotalDays: numDays,
			}
			return workflow.ExecuteActivity(ctx, sa.ImportDay, input)
		}, nil)
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupImportDays,
		fmt.Sprintf("Imported %d days for %s in %s.", numDays, season.Label(), tracker.GetElapsed(ctx, GroupImportDays)))

	logger.Info("ImportSeasonWorkflow completed",
		"startYear", season.ID.StartYear())

	return nil, nil
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

	var sa *SeasonsActivities

	// Import each league and collect team IDs
	for _, league := range yahooCfg.Leagues {
		input := ImportYahooLeagueInput{Season: startYear, LeagueID: league.LeagueID}
		if err := workflow.ExecuteActivity(ctx, sa.ImportYahooLeague, input).Get(ctx, nil); err != nil {
			return nil, err
		}

		for _, teamID := range league.TeamIDs {
			teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamID})
		}
	}

	// Import all teams in one batched activity
	if len(teamIDs) > 0 {
		input := ImportYahooTeamsInput{Season: startYear, Teams: teamIDs}
		if err := workflow.ExecuteActivity(ctx, sa.ImportYahooTeams, input).Get(ctx, nil); err != nil {
			return nil, err
		}
	}

	return teamIDs, nil
}

// WorkflowIDImportSeason returns the workflow ID for a single season import.
func WorkflowIDImportSeason(startYear int) string {
	return fmt.Sprintf("import-season-%d", startYear)
}

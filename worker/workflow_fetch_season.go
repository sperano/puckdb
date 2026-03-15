package worker

import (
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"go.temporal.io/sdk/workflow"
)

// Group index for FetchSeasonWorkflow progress
const GroupFetchSeasonData = 0

// NewFetchSeasonProgressReport creates the progress structure for a single season.
// Uses countDaysInSeason for consistency with ExtractBoxscorePlayers progress.
func NewFetchSeasonProgressReport(season nhl.SeasonInfo) *ProgressReport {
	total, _ := countDaysInSeason(season)
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
func FetchSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Debug("FetchSeasonWorkflow started",
		"seasonID", season.ID,
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	// Set up progress tracking
	tracker := NewReportTracker(NewFetchSeasonProgressReport(season))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonData)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Look up Yahoo config and download league/team data
	// Note: Yahoo downloads are not tracked in progress - only days are tracked for consistency
	var teamIDs []TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.ID.StartYear()]; inYahoo {
			// Build team list and fetch leagues
			var leagueAct *YahooActivities
			for _, league := range yahooCfg.Leagues {
				if err := workflow.ExecuteActivity(ctx, leagueAct.FetchLeague, season.ID.StartYear(), league.LeagueID).Get(ctx, nil); err != nil {
					return nil, err
				}
				for _, teamid := range league.TeamIDs {
					teamIDs = append(teamIDs, TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}

			// Fetch all teams in one batched activity
			if len(teamIDs) > 0 {
				input := FetchTeamsInput{StartSeason: season.ID.StartYear(), Teams: teamIDs}
				if err := workflow.ExecuteActivity(ctx, leagueAct.FetchTeams, input).Get(ctx, nil); err != nil {
					return nil, err
				}
			}
		}
	}

	// Calculate days to process (up to today)
	endDate := effectiveEndDate(season.StandingsEnd.Time)
	numDays := countDays(season.StandingsStart.Time, endDate)
	concurrency := getDayConcurrency()

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", concurrency)

	// Process days in parallel using RunWorkerPool
	// FetchDayActivity needs longer timeout due to rate-limited Yahoo downloads
	dayCtx := workflow.WithActivityOptions(ctx, fetchDayActivityOptions())
	startDate := season.StandingsStart.Time
	startSeason := season.ID.StartYear()
	counts := core.OriginCounts{}
	err = tracker.RunWorkerPool(ctx, GroupFetchSeasonData, 0, numDays, concurrency, func(_ workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		dayInput := FetchDayInput{
			Day:         day,
			StartSeason: startSeason,
			TeamIDs:     teamIDs,
			DayIndex:    i,
			TotalDays:   numDays,
		}
		var dsa *DailyScheduleActivities
		return workflow.ExecuteActivity(dayCtx, dsa.FetchDay, dayInput)
	}, func(_ workflow.Context, _ int, f workflow.Future) error {
		var dayCounts core.OriginCounts
		if err := f.Get(ctx, &dayCounts); err != nil {
			return err
		}
		counts.Add(dayCounts)
		return nil
	})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonData, fmt.Sprintf("Fetched %s", season.Label()))

	logger.Info("FetchSeasonWorkflow completed",
		"startYear", season.ID.StartYear())
	return counts, nil
}

// WorkflowIDFetchSeason returns the workflow ID for a single season fetch.
func WorkflowIDFetchSeason(startYear int) string {
	return fmt.Sprintf("fetch-season-%d", startYear)
}

// FetchDayInput contains parameters for FetchDayActivity.
// TeamIDs are pre-computed by the parent workflow.
type FetchDayInput struct {
	Day         time.Time
	StartSeason int        // Season start year (e.g., 2023 for 2023-2024 season)
	TeamIDs     []TeamInfo // Teams to fetch Yahoo data for (empty if no Yahoo config)
	DayIndex    int        // 0-based index for progress tracking
	TotalDays   int        // Total days in season for progress tracking
}

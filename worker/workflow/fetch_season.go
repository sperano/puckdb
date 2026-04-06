package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// Group index for FetchSeasonWorkflow progress
const GroupFetchSeasonData = 0

// NewFetchSeasonProgressReport creates the progress structure for a single season.
// Uses shared.CountDaysInSeason for consistency with ExtractBoxscorePlayers progress.
func NewFetchSeasonProgressReport(season nhl.SeasonInfo) *shared.ProgressReport {
	total, _ := shared.CountDaysInSeason(season)
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Fetching %s...", season.Label()), Bars: []shared.ProgressBar{{Total: total}}},
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
	tracker := shared.NewReportTracker(NewFetchSeasonProgressReport(season))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonData)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Look up Yahoo config and download league/team data
	// Note: Yahoo downloads are not tracked in progress - only days are tracked for consistency
	var teamIDs []yahoo.TeamInfo
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err == nil {
		if yahooCfg, inYahoo := yahooConfig[season.ID.StartYear()]; inYahoo {
			// Build team list and fetch leagues
			var leagueAct *yahoo.FetchActivities
			for _, league := range yahooCfg.Leagues {
				if err := workflow.ExecuteActivity(ctx, leagueAct.FetchLeague, season.ID.StartYear(), league.LeagueID).Get(ctx, nil); err != nil {
					return nil, err
				}
				for _, teamid := range league.TeamIDs {
					teamIDs = append(teamIDs, yahoo.TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
				}
			}

			// Fetch all teams in one batched activity
			if len(teamIDs) > 0 {
				input := yahoo.FetchTeamsInput{StartSeason: season.ID.StartYear(), Teams: teamIDs}
				if err := workflow.ExecuteActivity(ctx, leagueAct.FetchTeams, input).Get(ctx, nil); err != nil {
					return nil, err
				}
			}

			// Fetch league-level data (transactions, draft results, matchups)
			for _, league := range yahooCfg.Leagues {
				leagueDataInput := yahoo.FetchYahooLeagueDataInput{
					Season:   season.ID.StartYear(),
					LeagueID: league.LeagueID,
				}
				if err := workflow.ExecuteActivity(ctx, leagueAct.FetchYahooLeagueData, leagueDataInput).Get(ctx, nil); err != nil {
					return nil, err
				}
			}
		}
	}

	// Fetch season-level NHL data (rosters, club stats)
	var sa *worknhl.SeasonsActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, err
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, err
	}

	// Calculate days to process (up to today)
	endDate := shared.EffectiveEndDate(season.StandingsEnd.Time)
	numDays := core.CountDays(season.StandingsStart.Time, endDate)
	concurrency := shared.GetDayConcurrency()

	logger.Info("Processing days in parallel",
		"numDays", numDays,
		"concurrency", concurrency)

	// Process days in parallel using RunWorkerPool
	// FetchDayActivity needs longer timeout due to rate-limited Yahoo downloads
	dayCtx := workflow.WithActivityOptions(ctx, shared.FetchDayActivityOptions())
	startDate := season.StandingsStart.Time
	startSeason := season.ID.StartYear()
	counts := core.OriginCounts{}
	err = tracker.RunWorkerPool(ctx, GroupFetchSeasonData, 0, numDays, concurrency, func(_ workflow.Context, i int) workflow.Future {
		day := startDate.AddDate(0, 0, i)
		dayInput := worknhl.FetchDayInput{
			Day:         day,
			StartSeason: startSeason,
			TeamIDs:     teamIDs,
			DayIndex:    i,
			TotalDays:   numDays,
		}
		var dsa *worknhl.DailyScheduleActivities
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

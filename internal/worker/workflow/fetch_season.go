package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// Group indices for FetchSeasonWorkflow progress.
const (
	GroupFetchSeasonData    = 0
	GroupFetchSeasonPlayoff = 1
)

// NewFetchSeasonProgressReport creates the progress structure for a single season.
// The playoff bar starts with Total 0; it is sized to the season's team count
// (one unit per club-schedule fetch) once ListSeasonTeams has run.
func NewFetchSeasonProgressReport(ctx workflow.Context, season nhl.SeasonInfo) *shared.ProgressReport {
	days, _ := shared.CountDaysInSeason(ctx, season)
	return &shared.ProgressReport{
		Total: days,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Fetching %s...", season.Label()), Bars: []shared.ProgressBar{{Total: days}}},
			{Header: "Fetching playoff games...", Bars: []shared.ProgressBar{{Total: 0}}},
		},
	}
}

// FetchSeasonWorkflow fetches all data for a single season.
// Each season runs in its own child workflow to isolate history.
// A typical season (~270 days) generates ~600 history events, well under the 50K limit.
func FetchSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	return runFetchSeasonWorkflow(ctx, season, true)
}

// FetchNHLSeasonWorkflow fetches only date-gated NHL data. Yahoo metadata is
// handled independently by FetchYahooSeasonWorkflow in the parent workflow.
func FetchNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	return runFetchSeasonWorkflow(ctx, season, false)
}

func runFetchSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo, includeYahoo bool) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Debug("FetchSeasonWorkflow started",
		"seasonID", season.ID,
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	cfg, err := snapshotSeasonConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Set up progress tracking
	tracker, err := shared.InitTracker(ctx, NewFetchSeasonProgressReport(ctx, season))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonData)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Download Yahoo league/team data for the season, if configured.
	// Note: Yahoo downloads are not tracked in progress - only days are tracked for consistency
	var teamIDs []yahoo.TeamInfo
	if includeYahoo {
		teamIDs, err = fetchYahooLeaguesAndTeams(ctx, cfg.Yahoo, season.ID.StartYear())
		if err != nil {
			return nil, err
		}
	}

	// Fetch season-level NHL data (rosters, club stats)
	var sa *worknhl.SeasonsActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("fetch season rosters: %w", err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("fetch club stats: %w", err)
	}

	// Calculate days to process (up to today)
	endDate := shared.EffectiveEndDate(ctx, season.StandingsEnd.Time)
	numDays := core.CountDays(season.StandingsStart.Time, endDate)
	concurrency := cfg.DayConcurrency

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
	}, shared.AggregateInto[core.OriginCounts](counts))
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonData,
		fmt.Sprintf("Fetched %d days for %s in %s.", numDays, season.Label(), tracker.GetElapsed(ctx, GroupFetchSeasonData)))

	// Fetch playoff games after regular-season day loop: one activity per team,
	// each covering the team's home playoff games, so the bar ticks per team.
	tracker.StartGroup(ctx, GroupFetchSeasonPlayoff)

	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startSeason).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("list season teams: %w", err)
	}
	tracker.SetBarTotal(GroupFetchSeasonPlayoff, 0, len(teams))
	tracker.RecalcTotal()
	tracker.Save(ctx)

	playoffGames := 0
	err = tracker.RunWorkerPool(ctx, GroupFetchSeasonPlayoff, 0, len(teams), concurrency,
		func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.FetchTeamPlayoffGamesInput{Season: startSeason, TeamAbbrev: teams[i]}
			return workflow.ExecuteActivity(ctx, pa.FetchTeamPlayoffGames, input)
		},
		func(ctx workflow.Context, i int, f workflow.Future) error {
			var res worknhl.FetchTeamPlayoffGamesResult
			if err := f.Get(ctx, &res); err != nil {
				return fmt.Errorf("fetch playoff games for %s: %w", teams[i], err)
			}
			playoffGames += res.GamesFetched
			return nil
		})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonPlayoff,
		fmt.Sprintf("Fetched %d playoff games for %s in %s.", playoffGames, season.Label(), tracker.GetElapsed(ctx, GroupFetchSeasonPlayoff)))

	logger.Info("FetchSeasonWorkflow completed",
		"startYear", season.ID.StartYear(),
		"playoffGames", playoffGames)
	return counts, nil
}

// fetchYahooLeaguesAndTeams downloads Yahoo league, team, and league-level data
// for the season. Returns the team IDs to fetch per-day Yahoo data for, or nil
// when the season has no Yahoo config.
func fetchYahooLeaguesAndTeams(ctx workflow.Context, yahooSeasons shared.YahooSeasonsSnapshot, startYear int) ([]yahoo.TeamInfo, error) {
	result, err := runYahooSeasonMetadata(ctx, yahooSeasons, startYear, false)
	return result.TeamIDs, err
}

type yahooSeasonMetadataResult struct {
	TeamIDs              []yahoo.TeamInfo
	UnavailableResources []string
}

func fetchYahooSeasonMetadata(ctx workflow.Context, yahooSeasons shared.YahooSeasonsSnapshot,
	startYear int) (yahooSeasonMetadataResult, error) {
	return runYahooSeasonMetadata(ctx, yahooSeasons, startYear, true)
}

func runYahooSeasonMetadata(ctx workflow.Context, yahooSeasons shared.YahooSeasonsSnapshot,
	startYear int, captureUnavailable bool) (yahooSeasonMetadataResult, error) {
	result := yahooSeasonMetadataResult{}
	season, inYahoo := yahooSeasons.Season(startYear)
	if !inYahoo {
		return result, nil
	}

	var leagueAct *yahoo.FetchActivities

	// Build team list and fetch leagues
	for _, league := range season.Leagues {
		if league.UsesTemporaryMetadata() {
			if err := fetchStandInLeagueSource(ctx, league); err != nil {
				return result, err
			}
			result.UnavailableResources = append(result.UnavailableResources, standInLeagueNote(league))
			continue
		}
		if err := workflow.ExecuteActivity(ctx, leagueAct.FetchLeague, startYear, league.LeagueID).Get(ctx, nil); err != nil {
			return result, fmt.Errorf("fetch yahoo league %d: %w", league.LeagueID, err)
		}
		for _, teamid := range league.TeamIDs {
			result.TeamIDs = append(result.TeamIDs, yahoo.TeamInfo{LeagueID: league.LeagueID, TeamID: teamid})
		}
	}

	// Fetch all teams in one batched activity
	if len(result.TeamIDs) > 0 {
		input := yahoo.FetchTeamsInput{StartSeason: startYear, Teams: result.TeamIDs}
		if err := workflow.ExecuteActivity(ctx, leagueAct.FetchTeams, input).Get(ctx, nil); err != nil {
			return result, fmt.Errorf("fetch yahoo teams: %w", err)
		}
	}

	unavailable, err := fetchYahooSeasonLeagueData(ctx, season.Leagues, startYear, captureUnavailable)
	result.UnavailableResources = append(result.UnavailableResources, unavailable...)
	return result, err
}

// fetchYahooSeasonLeagueData fetches league-level data (transactions, draft
// results, matchups) and returns the optional resources Yahoo has not published.
func fetchYahooSeasonLeagueData(ctx workflow.Context, leagues []config.League, startYear int,
	captureUnavailable bool) ([]string, error) {
	var leagueAct *yahoo.FetchActivities
	var unavailable []string
	for _, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		leagueDataInput := yahoo.FetchYahooLeagueDataInput{Season: startYear, LeagueID: league.LeagueID}
		var leagueResult yahoo.FetchYahooLeagueDataResult
		future := workflow.ExecuteActivity(ctx, leagueAct.FetchYahooLeagueData, leagueDataInput)
		var err error
		if captureUnavailable {
			err = future.Get(ctx, &leagueResult)
		} else {
			err = future.Get(ctx, nil)
		}
		if err != nil {
			return unavailable, fmt.Errorf("fetch yahoo league data %d: %w", league.LeagueID, err)
		}
		for _, resourceName := range leagueResult.UnavailableResources {
			unavailable = append(unavailable, fmt.Sprintf("league %d %s", league.LeagueID, resourceName))
		}
	}
	return unavailable, nil
}

// WorkflowIDFetchSeason returns the workflow ID for a single season fetch.
func WorkflowIDFetchSeason(startYear int) string {
	return fmt.Sprintf("fetch-season-%d", startYear)
}

// WorkflowIDFetchNHLSeason identifies an NHL-only season child workflow.
func WorkflowIDFetchNHLSeason(startYear int) string {
	return fmt.Sprintf("fetch-nhl-season-%d", startYear)
}

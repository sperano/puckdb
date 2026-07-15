package workflow

import (
	"errors"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// Group indices for ImportSeasonWorkflow progress.
const (
	GroupImportDays    = 0
	GroupImportPlayoff = 1
)

// NewImportSeasonProgressReport creates the progress structure for a single season import.
// The playoff bar starts with Total 0; it is sized to the season's team count
// (one unit per club-schedule import) once ListSeasonTeams has run.
func NewImportSeasonProgressReport(ctx workflow.Context, season nhl.SeasonInfo) *shared.ProgressReport {
	days, _ := shared.CountDaysInSeason(ctx, season)
	return &shared.ProgressReport{
		Total: days,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Importing games for %s...", season.Label()), Bars: []shared.ProgressBar{{Total: days}}},
			{Header: "Importing playoff games...", Bars: []shared.ProgressBar{{Total: 0}}},
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

	tracker, err := shared.InitTracker(ctx, NewImportSeasonProgressReport(ctx, season))
	if err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Yahoo setup (not tracked in progress)
	teamIDs, err := importYahooLeaguesAndTeams(ctx, season.ID.StartYear())
	if err != nil {
		return nil, err
	}

	// Import season-level data (rosters, club stats, player career, Yahoo league data)
	var sa *worknhl.SeasonsActivities
	var ia *worknhl.ImportActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import season rosters: %w", err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import club stats: %w", err)
	}

	// Import Yahoo league-level data (transactions, draft results, matchups)
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err != nil && !errors.Is(err, config.ErrYahooNotConfigured) {
		return nil, fmt.Errorf("load yahoo seasons config: %w", err)
	}
	if yahooCfg, hasYahoo := yahooConfig[season.ID.StartYear()]; hasYahoo {
		var yia *yahoo.ImportActivities
		for _, league := range yahooCfg.Leagues {
			leagueDataInput := yahoo.ImportYahooLeagueDataInput{
				Season:   season.ID.StartYear(),
				LeagueID: league.LeagueID,
			}
			if err := workflow.ExecuteActivity(ctx, yia.ImportYahooLeagueData, leagueDataInput).Get(ctx, nil); err != nil {
				return nil, fmt.Errorf("import yahoo league data %d: %w", league.LeagueID, err)
			}
		}
	}

	// --- Import days ---
	tracker.StartGroup(ctx, GroupImportDays)

	endDate := shared.EffectiveEndDate(ctx, season.StandingsEnd.Time)
	numDays := core.CountDays(season.StandingsStart.Time, endDate)
	dayConcurrency := shared.GetDayConcurrency()
	startDate := season.StandingsStart.Time
	startYear := season.ID.StartYear()
	seasonID := season.ID.ID()

	counts := core.OriginCounts{}
	err = tracker.RunWorkerPool(ctx, GroupImportDays, 0, numDays, dayConcurrency,
		func(_ workflow.Context, i int) workflow.Future {
			day := startDate.AddDate(0, 0, i)
			input := worknhl.ImportDayInput{
				Date:      day,
				Season:    startYear,
				SeasonID:  seasonID,
				TeamIDs:   teamIDs,
				TotalDays: numDays,
			}
			return workflow.ExecuteActivity(ctx, ia.ImportDay, input)
		}, shared.AggregateInto[core.OriginCounts](counts))
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupImportDays,
		fmt.Sprintf("Imported %d days for %s in %s.", numDays, season.Label(), tracker.GetElapsed(ctx, GroupImportDays)))

	// Import playoff games after regular-season day loop: one activity per team,
	// each covering the team's home playoff games, so the bar ticks per team.
	tracker.StartGroup(ctx, GroupImportPlayoff)

	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startYear).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("list season teams: %w", err)
	}
	tracker.SetBarTotal(GroupImportPlayoff, 0, len(teams))
	tracker.RecalcTotal()
	tracker.Save(ctx)

	playoffTotals := worknhl.ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}
	err = tracker.RunWorkerPool(ctx, GroupImportPlayoff, 0, len(teams), dayConcurrency,
		func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.ImportTeamPlayoffGamesInput{Season: startYear, TeamAbbrev: teams[i]}
			return workflow.ExecuteActivity(ctx, pa.ImportTeamPlayoffGames, input)
		},
		func(ctx workflow.Context, i int, f workflow.Future) error {
			var res worknhl.ImportTeamPlayoffGamesResult
			if err := f.Get(ctx, &res); err != nil {
				return fmt.Errorf("import playoff games for %s: %w", teams[i], err)
			}
			playoffTotals.Origins.Add(res.Origins)
			playoffTotals.GamesImported += res.GamesImported
			playoffTotals.SkatersImported += res.SkatersImported
			playoffTotals.GoaliesImported += res.GoaliesImported
			return nil
		})
	if err != nil {
		return nil, err
	}
	counts.Add(playoffTotals.Origins)

	tracker.CompleteGroup(ctx, GroupImportPlayoff,
		fmt.Sprintf("Imported %d playoff games (%d skaters, %d goalies) for %s in %s.",
			playoffTotals.GamesImported, playoffTotals.SkatersImported, playoffTotals.GoaliesImported,
			season.Label(), tracker.GetElapsed(ctx, GroupImportPlayoff)))

	logger.Info("ImportSeasonWorkflow completed",
		"startYear", season.ID.StartYear(),
		"playoffGames", playoffTotals.GamesImported)

	return counts, nil
}

// importYahooLeaguesAndTeams imports Yahoo league and team metadata.
// Returns the list of team IDs to process for per-day Yahoo data import.
func importYahooLeaguesAndTeams(ctx workflow.Context, startYear int) ([]yahoo.TeamInfo, error) {
	logger := workflow.GetLogger(ctx)
	var teamIDs []yahoo.TeamInfo

	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err != nil {
		if errors.Is(err, config.ErrYahooNotConfigured) {
			return nil, nil
		}
		return nil, fmt.Errorf("load yahoo seasons config: %w", err)
	}

	yahooCfg, hasYahoo := yahooConfig[startYear]
	if !hasYahoo {
		return nil, nil // Season not in Yahoo config
	}

	logger.Info("Importing Yahoo data for season", "startYear", startYear)

	var yia *yahoo.ImportActivities

	// Import each league and collect team IDs
	for _, league := range yahooCfg.Leagues {
		input := yahoo.ImportYahooLeagueInput{Season: startYear, LeagueID: league.LeagueID}
		if err := workflow.ExecuteActivity(ctx, yia.ImportYahooLeague, input).Get(ctx, nil); err != nil {
			return nil, err
		}

		for _, teamID := range league.TeamIDs {
			teamIDs = append(teamIDs, yahoo.TeamInfo{LeagueID: league.LeagueID, TeamID: teamID})
		}
	}

	// Import all teams in one batched activity
	if len(teamIDs) > 0 {
		input := yahoo.ImportYahooTeamsInput{Season: startYear, Teams: teamIDs}
		if err := workflow.ExecuteActivity(ctx, yia.ImportYahooTeams, input).Get(ctx, nil); err != nil {
			return nil, err
		}
	}

	return teamIDs, nil
}

// WorkflowIDImportSeason returns the workflow ID for a single season import.
func WorkflowIDImportSeason(startYear int) string {
	return fmt.Sprintf("import-season-%d", startYear)
}

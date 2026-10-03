package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// YahooSeasonWorkflowInput is the snapshotted Yahoo configuration for one season.
type YahooSeasonWorkflowInput struct {
	StartYear int           `json:"startYear"`
	Season    config.Season `json:"season"`
}

// YahooSeasonSyncResult describes a successful metadata sync with optional
// resources Yahoo has not published yet. LeagueTotals holds each configured
// league's final progress Total, in configuration order, so the parent can
// complete its mirrored bars with the numbers the child ended on.
type YahooSeasonSyncResult struct {
	UnavailableResources []string `json:"unavailableResources,omitempty"`
	LeagueTotals         []int    `json:"leagueTotals,omitempty"`
}

// FetchYahooSeasonWorkflow fetches Yahoo metadata without scheduling NHL work.
func FetchYahooSeasonWorkflow(ctx workflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	result := YahooSeasonSyncResult{}
	progress, err := startYahooSeasonProgress(ctx, input, seasonSyncFetch)
	if err != nil {
		return result, err
	}
	unavailable, err := fetchYahooSeasonMetadata(ctx, input.StartYear, input.Season.Leagues, progress)
	if err != nil {
		return result, fmt.Errorf("yahoo resources for season %d are unavailable: %w", input.StartYear, err)
	}
	result.UnavailableResources = unavailable
	pools, err := fetchYahooPlayerPools(ctx, input.StartYear, input.Season.Leagues, progress)
	if err != nil {
		return result, err
	}
	result.UnavailableResources = append(result.UnavailableResources, pools...)
	progress.complete(ctx)
	result.LeagueTotals = progress.leagueTotals()
	return result, nil
}

// ImportYahooSeasonWorkflow imports cached Yahoo metadata without scheduling NHL work.
func ImportYahooSeasonWorkflow(ctx workflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	result := YahooSeasonSyncResult{}
	progress, err := startYahooSeasonProgress(ctx, input, seasonSyncImport)
	if err != nil {
		return result, err
	}
	if err := importYahooLeaguesAndTeams(ctx, input.Season, input.StartYear, progress); err != nil {
		return result, fmt.Errorf("import Yahoo season %d metadata: %w", input.StartYear, err)
	}
	unavailable, err := importYahooSeasonLeagueData(ctx, input, progress)
	if err != nil {
		return result, err
	}
	pools, err := importYahooPlayerPools(ctx, input.StartYear, input.Season.Leagues, progress)
	if err != nil {
		return result, err
	}
	result.UnavailableResources = append(unavailable, pools...)
	progress.complete(ctx)
	result.LeagueTotals = progress.leagueTotals()
	return result, nil
}

func importYahooSeasonLeagueData(ctx workflow.Context, input YahooSeasonWorkflowInput,
	progress yahooSeasonProgress) ([]string, error) {
	var activities *yahoo.ImportActivities
	var unavailable []string
	for i, league := range input.Season.Leagues {
		if league.UsesTemporaryMetadata() {
			unavailable = append(unavailable, standInLeagueNote(league))
			continue
		}
		activityInput := yahoo.ImportYahooLeagueDataInput{
			Season: input.StartYear, LeagueID: league.LeagueID,
		}
		var result yahoo.ImportYahooLeagueDataResult
		if err := workflow.ExecuteActivity(ctx, activities.ImportYahooLeagueData, activityInput).Get(ctx, &result); err != nil {
			return nil, fmt.Errorf("import Yahoo league data %d/%d: %w", input.StartYear, league.LeagueID, err)
		}
		progress.advance(ctx, i)
		for _, resourceName := range result.UnavailableResources {
			unavailable = append(unavailable, fmt.Sprintf("league %d %s", league.LeagueID, resourceName))
		}
	}
	return unavailable, nil
}

// importYahooLeaguesAndTeams imports Yahoo league and team metadata from the
// season's snapshotted Yahoo config. The teams of every league are imported
// in one batched activity, which advances each league with teams by one.
func importYahooLeaguesAndTeams(ctx workflow.Context, yahooCfg config.Season, startYear int,
	progress yahooSeasonProgress) error {
	if len(yahooCfg.Leagues) == 0 {
		return nil // Season not in Yahoo config
	}
	workflow.GetLogger(ctx).Info("Importing Yahoo data for season", "startYear", startYear)
	var yia *yahoo.ImportActivities
	var teamIDs []yahoo.TeamInfo
	var leaguesWithTeams []int
	for i, league := range yahooCfg.Leagues {
		if league.UsesTemporaryMetadata() {
			if err := importStandInLeague(ctx, startYear, league); err != nil {
				return err
			}
			progress.advance(ctx, i)
			continue
		}
		input := yahoo.ImportYahooLeagueInput{Season: startYear, LeagueID: league.LeagueID}
		if err := workflow.ExecuteActivity(ctx, yia.ImportYahooLeague, input).Get(ctx, nil); err != nil {
			return err
		}
		progress.advance(ctx, i)
		for _, teamID := range league.TeamIDs {
			teamIDs = append(teamIDs, yahoo.TeamInfo{LeagueID: league.LeagueID, TeamID: teamID})
		}
		if len(league.TeamIDs) > 0 {
			leaguesWithTeams = append(leaguesWithTeams, i)
		}
	}
	if len(teamIDs) == 0 {
		return nil
	}
	input := yahoo.ImportYahooTeamsInput{Season: startYear, Teams: teamIDs}
	if err := workflow.ExecuteActivity(ctx, yia.ImportYahooTeams, input).Get(ctx, nil); err != nil {
		return err
	}
	progress.advanceBars(ctx, leaguesWithTeams)
	return nil
}

// WorkflowIDFetchYahooSeason returns the workflow ID for one Yahoo metadata fetch.
func WorkflowIDFetchYahooSeason(startYear int) string {
	return fmt.Sprintf("fetch-yahoo-season-%d", startYear)
}

// WorkflowIDImportYahooSeason returns the workflow ID for one Yahoo metadata import.
func WorkflowIDImportYahooSeason(startYear int) string {
	return fmt.Sprintf("import-yahoo-season-%d", startYear)
}

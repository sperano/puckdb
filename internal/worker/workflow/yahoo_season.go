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
// resources Yahoo has not published yet.
type YahooSeasonSyncResult struct {
	UnavailableResources []string `json:"unavailableResources,omitempty"`
}

// FetchYahooSeasonWorkflow fetches Yahoo metadata without scheduling NHL work.
func FetchYahooSeasonWorkflow(ctx workflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	result := YahooSeasonSyncResult{}
	snapshot := shared.YahooSeasonsSnapshot{
		Seasons: config.YahooSeasonsMap{input.StartYear: input.Season},
	}
	metadata, err := fetchYahooSeasonMetadata(ctx, snapshot, input.StartYear)
	if err != nil {
		return result, fmt.Errorf("yahoo resources for season %d are unavailable: %w", input.StartYear, err)
	}
	result.UnavailableResources = metadata.UnavailableResources
	return result, nil
}

// ImportYahooSeasonWorkflow imports cached Yahoo metadata without scheduling NHL work.
func ImportYahooSeasonWorkflow(ctx workflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	result := YahooSeasonSyncResult{}
	if _, err := importYahooLeaguesAndTeams(ctx, input.Season, input.StartYear); err != nil {
		return result, fmt.Errorf("import Yahoo season %d metadata: %w", input.StartYear, err)
	}
	unavailable, err := importYahooSeasonLeagueData(ctx, input)
	if err != nil {
		return result, err
	}
	result.UnavailableResources = unavailable
	return result, nil
}

func importYahooSeasonLeagueData(ctx workflow.Context, input YahooSeasonWorkflowInput) ([]string, error) {
	var activities *yahoo.ImportActivities
	var unavailable []string
	for _, league := range input.Season.Leagues {
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
		for _, resourceName := range result.UnavailableResources {
			unavailable = append(unavailable, fmt.Sprintf("league %d %s", league.LeagueID, resourceName))
		}
	}
	return unavailable, nil
}

// WorkflowIDFetchYahooSeason returns the workflow ID for one Yahoo metadata fetch.
func WorkflowIDFetchYahooSeason(startYear int) string {
	return fmt.Sprintf("fetch-yahoo-season-%d", startYear)
}

// WorkflowIDImportYahooSeason returns the workflow ID for one Yahoo metadata import.
func WorkflowIDImportYahooSeason(startYear int) string {
	return fmt.Sprintf("import-yahoo-season-%d", startYear)
}

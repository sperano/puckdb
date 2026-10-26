package cmd

import (
	"context"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/spf13/viper"
)

// buildSeasonsInput maps the sync command's season flags to the GraphQL
// SeasonsInput shared by every season-scoped workflow it starts.
func buildSeasonsInput() *model.SeasonsInput {
	input := &model.SeasonsInput{}

	start, end := config.GetSeasonRange()
	if start != 0 {
		input.StartSeason = &start
	}
	if end != 0 {
		input.EndSeason = &end
	}

	if concurrency := viper.GetInt(config.FlagSeasonConcurrency); concurrency > 0 {
		input.SeasonConcurrency = &concurrency
	}

	if viper.GetBool(config.FlagRefreshCurrentPlayerLogs) {
		refreshPlayerLogs := true
		input.RefreshCurrentPlayerLogs = &refreshPlayerLogs
	}
	if viper.GetBool(config.FlagRefreshCurrentEdge) {
		refreshEdge := true
		input.RefreshCurrentEdge = &refreshEdge
	}
	if viper.GetBool(config.FlagReimportDays) {
		reimportDays := true
		input.ReimportDays = &reimportDays
	}

	return input
}

// syncStep defines a workflow-driven sync phase for the data-driven loop.
type syncStep struct {
	name      string
	wt        workflowType
	label     string
	trigger   func() (bool, error)
	getStatus func(context.Context) (*WorkflowStatus, error)
}

// parallelSyncRunners returns the enabled steps of the parallel phase:
// init, Yahoo players and the season fetch.
func parallelSyncRunners(ctx context.Context, client *GraphQLClient, enabled map[string]bool) []workflowRunner {
	var runners []workflowRunner
	if enabled[config.StepInit] {
		runners = append(runners, workflowRunner{
			workflowType: workflowInitialize,
			trigger:      func() (bool, error) { return client.Initialize(ctx) },
			getStatus:    client.GetInitializeStatus,
		})
	}

	if enabled[config.StepYahooPlayers] {
		runners = append(runners, workflowRunner{
			workflowType: workflowYahooPlayers,
			trigger:      func() (bool, error) { return client.FetchYahooPlayers(ctx) },
			getStatus:    client.GetFetchYahooPlayersStatus,
		})
	}

	if enabled[config.StepFetchSeasons] {
		runners = append(runners, workflowRunner{
			workflowType: workflowFetchSeasons,
			trigger:      func() (bool, error) { return client.FetchSeasons(ctx, buildSeasonsInput()) },
			getStatus:    client.GetFetchSeasonsStatus,
		})
	}
	return runners
}

// sequentialSyncSteps returns every step of the sequential phase in run
// order; runSequentialPhase skips the disabled ones.
func sequentialSyncSteps(ctx context.Context, client *GraphQLClient) []syncStep {
	return []syncStep{
		{config.StepExtractBoxscorePlayers, workflowExtractBoxscorePlayers, "boxscore players extraction",
			func() (bool, error) { return client.ExtractBoxscorePlayers(ctx, buildSeasonsInput()) },
			client.GetExtractBoxscorePlayersStatus},
		{config.StepFetchPlayerLandings, workflowFetchPlayerLandings, "player landings fetch",
			func() (bool, error) { return client.FetchPlayerLandings(ctx, nil) },
			client.GetFetchPlayerLandingsStatus},
		{config.StepFetchPlayerLogs, workflowFetchPlayerLogs, "player logs fetch",
			func() (bool, error) { return client.FetchPlayerLogs(ctx, buildSeasonsInput()) },
			client.GetFetchPlayerLogsStatus},
		{config.StepProcessPlayers, workflowProcessPlayers, "players processing",
			func() (bool, error) { return client.ProcessPlayers(ctx, nil) },
			client.GetProcessPlayersStatus},
		{config.StepImportSeasons, workflowImportSeasons, "seasons import",
			func() (bool, error) { return client.ImportSeasons(ctx, buildSeasonsInput()) },
			client.GetImportSeasonsStatus},
		{config.StepImportPlayerLogs, workflowImportPlayerLogs, "player logs import",
			func() (bool, error) { return client.ImportPlayerLogs(ctx, buildSeasonsInput()) },
			client.GetImportPlayerLogsStatus},
		{config.StepFetchEdgeStats, workflowFetchEdgeStats, "edge stats fetch",
			func() (bool, error) { return client.FetchEdgeStats(ctx, buildSeasonsInput()) },
			client.GetFetchEdgeStatsStatus},
		{config.StepImportEdgeStats, workflowImportEdgeStats, "edge stats import",
			func() (bool, error) { return client.ImportEdgeStats(ctx, buildSeasonsInput()) },
			client.GetImportEdgeStatsStatus},
		{config.StepRefreshNews, workflowRefreshNews, "news refresh",
			func() (bool, error) { return client.RefreshNews(ctx, buildRefreshNewsInput()) },
			client.GetRefreshNewsStatus},
		{config.StepRefreshDraftRankings, workflowRefreshDraftRankings, "draft rankings refresh",
			func() (bool, error) {
				input, err := buildRefreshDraftRankingsInput()
				if err != nil {
					return false, err
				}
				return client.RefreshDraftRankings(ctx, input)
			},
			client.GetRefreshDraftRankingsStatus},
		{config.StepFetchAssets, workflowFetchAssets, "assets fetch",
			func() (bool, error) { return client.FetchAssets(ctx, nil) },
			client.GetFetchAssetsStatus},
	}
}

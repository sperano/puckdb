package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/graph/model"
)

// Common GraphQL field selections for workflow queries.
const (
	resultFields         = "{ status failureReason }"
	progressFieldsBasic  = "{ total completed message }"
	progressReportFields = "{ total completed message groups { header completedMsg bars { label current total started } startedAt completedAt } }"
)

// GetDownloadEverythingStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetDownloadEverythingStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query { downloadEverythingResult `+resultFields+` downloadEverythingProgress `+progressFieldsBasic+` }`,
		"downloadEverythingResult", "downloadEverythingProgress", nil)
}

// GetDownloadEverythingForSeasonStatus queries both workflow result and progress for a specific season.
func (c *GraphQLClient) GetDownloadEverythingForSeasonStatus(ctx context.Context, season int) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query($season: Int!) { downloadEverythingForSeasonResult(season: $season) `+resultFields+` downloadEverythingForSeasonProgress(season: $season) { total completed } }`,
		"downloadEverythingForSeasonResult", "downloadEverythingForSeasonProgress",
		map[string]any{"season": season})
}

// GetFetchSeasonsStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { fetchSeasonsResult `+resultFields+` fetchSeasonsProgress `+progressReportFields+` }`,
		"fetchSeasonsResult", "fetchSeasonsProgress")
}

// GetFetchPlayerLogsStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchPlayerLogsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { fetchPlayerLogsResult `+resultFields+` fetchPlayerLogsProgress `+progressReportFields+` }`,
		"fetchPlayerLogsResult", "fetchPlayerLogsProgress")
}

// GetFetchYahooPlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchYahooPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { fetchYahooPlayersResult `+resultFields+` fetchYahooPlayersProgress `+progressReportFields+` }`,
		"fetchYahooPlayersResult", "fetchYahooPlayersProgress")
}

// GetProcessPlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetProcessPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { processPlayersResult `+resultFields+` processPlayersProgress `+progressReportFields+` }`,
		"processPlayersResult", "processPlayersProgress")
}

// GetProcessPlayersResultData queries the process players workflow result data
func (c *GraphQLClient) GetProcessPlayersResultData(ctx context.Context) (*model.ProcessPlayersResultData, error) {
	const query = `query { processPlayersResultData {
		totalPlayers importedPlayers matchedWithYahoo downloaded cacheHits missing
		totalYahooPlayers skippedNonNHL verifiedNonNHLThisRun
		trulyUnmatched { yahooID firstName lastName nhlGames nhlPlayerID nhlName } errors
	}}`
	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	var result *model.ProcessPlayersResultData
	if err := json.Unmarshal(raw["processPlayersResultData"], &result); err != nil {
		return nil, fmt.Errorf("parse processPlayersResultData: %w", err)
	}
	return result, nil
}

// GetImportSeasonsStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { importSeasonsResult `+resultFields+` importSeasonsProgress `+progressReportFields+` }`,
		"importSeasonsResult", "importSeasonsProgress")
}

// GetImportPlayerLogsStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportPlayerLogsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { importPlayerLogsResult `+resultFields+` importPlayerLogsProgress `+progressReportFields+` }`,
		"importPlayerLogsResult", "importPlayerLogsProgress")
}

// GetInitializeStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetInitializeStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { initializeResult `+resultFields+` initializeProgress `+progressReportFields+` }`,
		"initializeResult", "initializeProgress")
}

// GetExtractBoxscorePlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetExtractBoxscorePlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { extractBoxscorePlayersResult `+resultFields+` extractBoxscorePlayersProgress `+progressReportFields+` }`,
		"extractBoxscorePlayersResult", "extractBoxscorePlayersProgress")
}

// GetFetchPlayerLandingsStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchPlayerLandingsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeProgressReportQuery(ctx,
		`query { fetchPlayerLandingsResult `+resultFields+` fetchPlayerLandingsProgress `+progressReportFields+` }`,
		"fetchPlayerLandingsResult", "fetchPlayerLandingsProgress")
}

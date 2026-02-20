package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/graph/model"
)

// Common GraphQL field selections for workflow queries.
const (
	resultFields        = "{ status failureReason }"
	progressFieldsBasic = "{ total completed message }"
	progressFieldsFull  = "{ total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }"
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

// GetFetchSeasonsStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetFetchSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query { fetchSeasonsResult `+resultFields+` fetchSeasonsProgress `+progressFieldsFull+` }`,
		"fetchSeasonsResult", "fetchSeasonsProgress", nil)
}

// GetFetchYahooPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetFetchYahooPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query { fetchYahooPlayersResult `+resultFields+` fetchYahooPlayersProgress `+progressFieldsFull+` }`,
		"fetchYahooPlayersResult", "fetchYahooPlayersProgress", nil)
}

// GetProcessPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetProcessPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query { processPlayersResult `+resultFields+` processPlayersProgress `+progressFieldsFull+` }`,
		"processPlayersResult", "processPlayersProgress", nil)
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
	return c.executeWorkflowStatusQuery(ctx,
		`query { importSeasonsResult `+resultFields+` importSeasonsProgress `+progressFieldsFull+` }`,
		"importSeasonsResult", "importSeasonsProgress", nil)
}

// GetInitializeStatus queries both workflow result and progress
func (c *GraphQLClient) GetInitializeStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.executeWorkflowStatusQuery(ctx,
		`query { initializeResult `+resultFields+` initializeProgress `+progressFieldsFull+` }`,
		"initializeResult", "initializeProgress", nil)
}

// GetInitializeResultData queries the initialize workflow result data
func (c *GraphQLClient) GetInitializeResultData(ctx context.Context) (*model.InitializeResultData, error) {
	const query = `query { initializeResultData { franchisesUpserted seasonsUpserted seasonTeamsUpserted }}`
	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	var result *model.InitializeResultData
	if err := json.Unmarshal(raw["initializeResultData"], &result); err != nil {
		return nil, fmt.Errorf("parse initializeResultData: %w", err)
	}
	return result, nil
}

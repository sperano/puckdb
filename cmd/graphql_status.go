package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/graph/model"
)

// Common GraphQL field selections for workflow queries.
const (
	resultFields          = "{ status failureReason }"
	progressReportFields  = "{ total completed message groups { header completedMsg bars { label current total started } startedAt completedAt } }"
	yahooTokenStatusField = "yahooTokenStatus { valid loginURL }"
)

// workflowStatus builds and runs the standard "<name>Result + <name>Progress"
// status query shared by every workflow status getter below. withYahoo
// controls whether the yahooTokenStatus field is appended: most workflows
// can be blocked on a stale/missing Yahoo OAuth token so the CLI can warn
// about it inline, but Edge-stats and asset workflows don't touch Yahoo.
func (c *GraphQLClient) workflowStatus(ctx context.Context, resultName, progressName string, withYahoo bool) (*WorkflowStatus, error) {
	query := "query { " + resultName + " " + resultFields + " " + progressName + " " + progressReportFields
	if withYahoo {
		query += " " + yahooTokenStatusField
	}
	query += " }"
	return c.executeProgressReportQuery(ctx, query, resultName, progressName)
}

// GetFetchSeasonsStatus queries both workflow result and progress
func (c *GraphQLClient) GetFetchSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchSeasonsResult", "fetchSeasonsProgress", true)
}

// GetFetchPlayerLogsStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchPlayerLogsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchPlayerLogsResult", "fetchPlayerLogsProgress", true)
}

// GetFetchYahooPlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchYahooPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchYahooPlayersResult", "fetchYahooPlayersProgress", true)
}

// GetProcessPlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetProcessPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "processPlayersResult", "processPlayersProgress", true)
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
	return c.workflowStatus(ctx, "importSeasonsResult", "importSeasonsProgress", true)
}

// GetImportPlayerLogsStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportPlayerLogsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "importPlayerLogsResult", "importPlayerLogsProgress", true)
}

// GetInitializeStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetInitializeStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "initializeResult", "initializeProgress", true)
}

// GetExtractBoxscorePlayersStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetExtractBoxscorePlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "extractBoxscorePlayersResult", "extractBoxscorePlayersProgress", true)
}

// GetFetchPlayerLandingsStatus queries both workflow result and progress (uses ProgressReport format)
func (c *GraphQLClient) GetFetchPlayerLandingsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchPlayerLandingsResult", "fetchPlayerLandingsProgress", true)
}

// GetFetchEdgeStatsStatus queries both workflow result and progress.
func (c *GraphQLClient) GetFetchEdgeStatsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchEdgeStatsResult", "fetchEdgeStatsProgress", false)
}

// GetImportEdgeStatsStatus queries both workflow result and progress.
func (c *GraphQLClient) GetImportEdgeStatsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "importEdgeStatsResult", "importEdgeStatsProgress", false)
}

// GetFetchAssetsStatus queries both workflow result and progress for the
// parent FetchAssetsWorkflow. Per-class child workflow progress is merged
// server-side via each bar's ProgressSourceKey.
func (c *GraphQLClient) GetFetchAssetsStatus(ctx context.Context) (*WorkflowStatus, error) {
	return c.workflowStatus(ctx, "fetchAssetsResult", "fetchAssetsProgress", false)
}

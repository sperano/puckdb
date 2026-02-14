package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/graph/model"
)

// GetDownloadEverythingStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetDownloadEverythingStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadEverythingResult { status failureReason }
		downloadEverythingProgress { total completed message }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadEverythingResult   *model.WorkflowResult   `json:"downloadEverythingResult"`
		DownloadEverythingProgress *model.WorkflowProgress `json:"downloadEverythingProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadEverythingResult,
		Progress: result.DownloadEverythingProgress,
	}, nil
}

// GetDownloadEverythingForSeasonStatus queries both workflow result and progress for a specific season.
func (c *GraphQLClient) GetDownloadEverythingForSeasonStatus(ctx context.Context, season int) (*WorkflowStatus, error) {
	const query = `query($season: Int!) {
		downloadEverythingForSeasonResult(season: $season) { status failureReason }
		downloadEverythingForSeasonProgress(season: $season) { total completed }
	}`

	resp, err := c.execute(ctx, query, map[string]any{"season": season})
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadEverythingForSeasonResult   *model.WorkflowResult   `json:"downloadEverythingForSeasonResult"`
		DownloadEverythingForSeasonProgress *model.WorkflowProgress `json:"downloadEverythingForSeasonProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadEverythingForSeasonResult,
		Progress: result.DownloadEverythingForSeasonProgress,
	}, nil
}

// GetFetchSeasonsStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetFetchSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		fetchSeasonsResult { status failureReason }
		fetchSeasonsProgress { total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		FetchSeasonsResult   *model.WorkflowResult   `json:"fetchSeasonsResult"`
		FetchSeasonsProgress *model.WorkflowProgress `json:"fetchSeasonsProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.FetchSeasonsResult,
		Progress: result.FetchSeasonsProgress,
	}, nil
}

// GetFetchYahooPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetFetchYahooPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		fetchYahooPlayersResult { status failureReason }
		fetchYahooPlayersProgress { total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		FetchYahooPlayersResult   *model.WorkflowResult   `json:"fetchYahooPlayersResult"`
		FetchYahooPlayersProgress *model.WorkflowProgress `json:"fetchYahooPlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.FetchYahooPlayersResult,
		Progress: result.FetchYahooPlayersProgress,
	}, nil
}

// GetProcessPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetProcessPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		processPlayersResult { status failureReason }
		processPlayersProgress { total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ProcessPlayersResult   *model.WorkflowResult   `json:"processPlayersResult"`
		ProcessPlayersProgress *model.WorkflowProgress `json:"processPlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.ProcessPlayersResult,
		Progress: result.ProcessPlayersProgress,
	}, nil
}

// GetProcessPlayersResultData queries the process players workflow result data
func (c *GraphQLClient) GetProcessPlayersResultData(ctx context.Context) (*model.ProcessPlayersResultData, error) {
	const query = `query {
		processPlayersResultData {
			totalPlayers
			importedPlayers
			matchedWithYahoo
			downloaded
			cacheHits
			missing
			totalYahooPlayers
			skippedNonNHL
			verifiedNonNHLThisRun
			trulyUnmatched { yahooID firstName lastName nhlGames nhlPlayerID nhlName }
			errors
		}
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ProcessPlayersResultData *model.ProcessPlayersResultData `json:"processPlayersResultData"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ProcessPlayersResultData, nil
}

// GetImportSeasonsStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		importSeasonsResult { status failureReason }
		importSeasonsProgress { total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ImportSeasonsResult   *model.WorkflowResult   `json:"importSeasonsResult"`
		ImportSeasonsProgress *model.WorkflowProgress `json:"importSeasonsProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.ImportSeasonsResult,
		Progress: result.ImportSeasonsProgress,
	}, nil
}

// GetInitializeStatus queries both workflow result and progress
func (c *GraphQLClient) GetInitializeStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		initializeResult { status failureReason }
		initializeProgress { total completed message header completedHeader displayStyle items { id description completedDescription total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		InitializeResult   *model.WorkflowResult   `json:"initializeResult"`
		InitializeProgress *model.WorkflowProgress `json:"initializeProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.InitializeResult,
		Progress: result.InitializeProgress,
	}, nil
}

// GetInitializeResultData queries the initialize workflow result data
func (c *GraphQLClient) GetInitializeResultData(ctx context.Context) (*model.InitializeResultData, error) {
	const query = `query {
		initializeResultData {
			franchisesUpserted
			seasonsUpserted
			seasonTeamsUpserted
		}
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		InitializeResultData *model.InitializeResultData `json:"initializeResultData"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.InitializeResultData, nil
}

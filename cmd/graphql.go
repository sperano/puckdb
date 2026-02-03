package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
)

// GraphQL request/response types
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

// GraphQLClient handles communication with the GraphQL API
type GraphQLClient struct {
	endpoint   string
	httpClient *http.Client
}

// NewGraphQLClient creates a new GraphQL client
func NewGraphQLClient(endpoint string) *GraphQLClient {
	return &GraphQLClient{
		endpoint:   strings.TrimSuffix(endpoint, "/") + "/graphql/query",
		httpClient: &http.Client{Timeout: config.DefaultHTTPClientTimeout},
	}
}

// execute sends a GraphQL request and returns the response
func (c *GraphQLClient) execute(ctx context.Context, query string, variables map[string]any) (*graphQLResponse, error) {
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	log.Debug().Str("url", c.endpoint).Msg("Submitting GraphQL query")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return &gqlResp, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	return &gqlResp, nil
}

// WorkflowStatus combines result and progress from a workflow query
type WorkflowStatus struct {
	Result   *model.WorkflowResult
	Progress *model.WorkflowProgress
}

// statusFetcher is a function type for fetching workflow status (result and progress)
type statusFetcher func(ctx context.Context) (*WorkflowStatus, error)

// workflowType identifies the workflow now running for cancel handling
type workflowType int

const (
	workflowNone workflowType = iota
	workflowYahooPlayers
	workflowDownloadSeasons
	workflowDownloadPlayers
	workflowImportPlayers
)

// DownloadEverything triggers the downloadEverything mutation
func (c *GraphQLClient) DownloadEverything(ctx context.Context) (bool, error) {
	const mutation = `mutation { downloadEverything }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadEverything bool `json:"downloadEverything"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadEverything, nil
}

// DownloadSeasons triggers the downloadSeasons mutation with an optional season range.
func (c *GraphQLClient) DownloadSeasons(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	const mutation = `mutation($input: DownloadSeasonsInput) { downloadSeasons(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadSeasons bool `json:"downloadSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadSeasons, nil
}

// GetDownloadSeasonsStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetDownloadSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadSeasonsResult { status failureReason }
		downloadSeasonsProgress { total completed message items { id description total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadSeasonsResult   *model.WorkflowResult   `json:"downloadSeasonsResult"`
		DownloadSeasonsProgress *model.WorkflowProgress `json:"downloadSeasonsProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadSeasonsResult,
		Progress: result.DownloadSeasonsProgress,
	}, nil
}

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

// DownloadEverythingForSeason triggers the downloadEverythingForSeason mutation
func (c *GraphQLClient) DownloadEverythingForSeason(ctx context.Context, season int) (bool, error) {
	const mutation = `mutation($season: Int!) { downloadEverythingForSeason(season: $season) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"season": season})
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadEverythingForSeason bool `json:"downloadEverythingForSeason"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadEverythingForSeason, nil
}

// DownloadYahooPlayers triggers the downloadYahooPlayers mutation
func (c *GraphQLClient) DownloadYahooPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { downloadYahooPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadYahooPlayers bool `json:"downloadYahooPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadYahooPlayers, nil
}

// GetDownloadYahooPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetDownloadYahooPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadYahooPlayersResult { status failureReason }
		downloadYahooPlayersProgress { total completed message }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadYahooPlayersResult   *model.WorkflowResult   `json:"downloadYahooPlayersResult"`
		DownloadYahooPlayersProgress *model.WorkflowProgress `json:"downloadYahooPlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadYahooPlayersResult,
		Progress: result.DownloadYahooPlayersProgress,
	}, nil
}

// CancelDownloadYahooPlayers cancels the downloadYahooPlayers workflow
func (c *GraphQLClient) CancelDownloadYahooPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelDownloadYahooPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelDownloadYahooPlayers bool `json:"cancelDownloadYahooPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelDownloadYahooPlayers, nil
}

// CancelDownloadSeasons cancels the downloadSeasons workflow
func (c *GraphQLClient) CancelDownloadSeasons(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelDownloadSeasons }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelDownloadSeasons bool `json:"cancelDownloadSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelDownloadSeasons, nil
}

// DownloadPlayers triggers the downloadPlayers mutation
func (c *GraphQLClient) DownloadPlayers(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	const mutation = `mutation($input: DownloadSeasonsInput) { downloadPlayers(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadPlayers bool `json:"downloadPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadPlayers, nil
}

// GetDownloadPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetDownloadPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadPlayersResult { status failureReason }
		downloadPlayersProgress { total completed message }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadPlayersResult   *model.WorkflowResult   `json:"downloadPlayersResult"`
		DownloadPlayersProgress *model.WorkflowProgress `json:"downloadPlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadPlayersResult,
		Progress: result.DownloadPlayersProgress,
	}, nil
}

// CancelDownloadPlayers cancels the downloadPlayers workflow
func (c *GraphQLClient) CancelDownloadPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelDownloadPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelDownloadPlayers bool `json:"cancelDownloadPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelDownloadPlayers, nil
}

// FlushRedisDB calls the flushRedisDB mutation to flush all keys in the configured Redis DB
func (c *GraphQLClient) FlushRedisDB(ctx context.Context) (bool, error) {
	const mutation = `mutation { flushRedisDB }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		FlushRedisDB bool `json:"flushRedisDB"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.FlushRedisDB, nil
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

// ImportPlayers triggers the importPlayers mutation
func (c *GraphQLClient) ImportPlayers(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	const mutation = `mutation($input: DownloadSeasonsInput) { importPlayers(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		ImportPlayers bool `json:"importPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ImportPlayers, nil
}

// GetImportPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		importPlayersResult { status failureReason }
		importPlayersProgress { total completed message items { id description total completed started startedAt completedAt } }
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ImportPlayersResult   *model.WorkflowResult   `json:"importPlayersResult"`
		ImportPlayersProgress *model.WorkflowProgress `json:"importPlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.ImportPlayersResult,
		Progress: result.ImportPlayersProgress,
	}, nil
}

// CancelImportPlayers cancels the importPlayers workflow
func (c *GraphQLClient) CancelImportPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelImportPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelImportPlayers bool `json:"cancelImportPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelImportPlayers, nil
}

// ImportPlayersResultData holds the result data from the import workflow
type ImportPlayersResultData struct {
	TotalPlayers     int                     `json:"totalPlayers"`
	ImportedPlayers  int                     `json:"importedPlayers"`
	MatchedWithYahoo int                     `json:"matchedWithYahoo"`
	UnmatchedYahoo   []UnmatchedYahooPlayer  `json:"unmatchedYahoo"`
	Errors           []string                `json:"errors"`
}

// UnmatchedYahooPlayer represents a Yahoo player that couldn't be matched to an NHL player
type UnmatchedYahooPlayer struct {
	YahooID      int    `json:"yahooID"`
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Team         string `json:"team"`
	JerseyNumber int    `json:"jerseyNumber"`
}

// GetImportPlayersResultData queries the import players workflow result data
func (c *GraphQLClient) GetImportPlayersResultData(ctx context.Context) (*ImportPlayersResultData, error) {
	const query = `query {
		importPlayersResultData {
			totalPlayers
			importedPlayers
			matchedWithYahoo
			unmatchedYahoo { yahooID firstName lastName team jerseyNumber }
			errors
		}
	}`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ImportPlayersResultData *ImportPlayersResultData `json:"importPlayersResultData"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ImportPlayersResultData, nil
}

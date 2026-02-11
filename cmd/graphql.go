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
	workflowInitialize
	workflowYahooPlayers
	workflowDownloadSeasons
	workflowProcessPlayers
	workflowImportSeasons
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
		downloadSeasonsProgress { total completed message header displayStyle items { id description completedDescription total completed started startedAt completedAt } }
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
		downloadYahooPlayersProgress { total completed message header displayStyle items { id description completedDescription total completed started startedAt completedAt } }
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

// ProcessPlayers triggers the processPlayers mutation (combined download + import)
func (c *GraphQLClient) ProcessPlayers(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	const mutation = `mutation($input: DownloadSeasonsInput) { processPlayers(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		ProcessPlayers bool `json:"processPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ProcessPlayers, nil
}

// GetProcessPlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetProcessPlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		processPlayersResult { status failureReason }
		processPlayersProgress { total completed message header displayStyle items { id description completedDescription total completed started startedAt completedAt } }
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

// CancelProcessPlayers cancels the processPlayers workflow
func (c *GraphQLClient) CancelProcessPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelProcessPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelProcessPlayers bool `json:"cancelProcessPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelProcessPlayers, nil
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


// ImportSeasons triggers the importSeasons mutation
func (c *GraphQLClient) ImportSeasons(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	const mutation = `mutation($input: DownloadSeasonsInput) { importSeasons(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		ImportSeasons bool `json:"importSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ImportSeasons, nil
}

// GetImportSeasonsStatus queries both workflow result and progress
func (c *GraphQLClient) GetImportSeasonsStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		importSeasonsResult { status failureReason }
		importSeasonsProgress { total completed message header displayStyle items { id description completedDescription total completed started startedAt completedAt } }
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

// CancelImportSeasons cancels the importSeasons workflow
func (c *GraphQLClient) CancelImportSeasons(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelImportSeasons }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelImportSeasons bool `json:"cancelImportSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelImportSeasons, nil
}

// ProcessPlayersResultData holds the result data from the process players workflow
type ProcessPlayersResultData struct {
	// Player counts
	TotalPlayers     int `json:"totalPlayers"`
	ImportedPlayers  int `json:"importedPlayers"`
	MatchedWithYahoo int `json:"matchedWithYahoo"`
	// Download stats
	Downloaded int `json:"downloaded"`
	CacheHits  int `json:"cacheHits"`
	Missing    int `json:"missing"`
	// Yahoo player stats
	TotalYahooPlayers     int                       `json:"totalYahooPlayers"`
	SkippedNonNHL         int                       `json:"skippedNonNHL"`
	VerifiedNonNHLThisRun int                       `json:"verifiedNonNHLThisRun"`
	TrulyUnmatched        []TrulyUnmatchedPlayer    `json:"trulyUnmatched"`
	Errors                []string                  `json:"errors"`
}

// TrulyUnmatchedPlayer represents a Yahoo player with NHL games who wasn't matched
type TrulyUnmatchedPlayer struct {
	YahooID     int    `json:"yahooID"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	NHLGames    int    `json:"nhlGames"`
	NHLPlayerID int    `json:"nhlPlayerID"`
	NHLName     string `json:"nhlName"`
}

// GetProcessPlayersResultData queries the process players workflow result data
func (c *GraphQLClient) GetProcessPlayersResultData(ctx context.Context) (*ProcessPlayersResultData, error) {
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
		ProcessPlayersResultData *ProcessPlayersResultData `json:"processPlayersResultData"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ProcessPlayersResultData, nil
}

// Initialize triggers the initialize mutation
func (c *GraphQLClient) Initialize(ctx context.Context) (bool, error) {
	const mutation = `mutation { initialize }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		Initialize bool `json:"initialize"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.Initialize, nil
}

// GetInitializeStatus queries both workflow result and progress
func (c *GraphQLClient) GetInitializeStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		initializeResult { status failureReason }
		initializeProgress { total completed message header displayStyle items { id description completedDescription total completed started startedAt completedAt } }
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

// CancelInitialize cancels the initialize workflow
func (c *GraphQLClient) CancelInitialize(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelInitialize }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelInitialize bool `json:"cancelInitialize"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelInitialize, nil
}

// InitializeResultData holds the result data from the initialize workflow
type InitializeResultData struct {
	FranchisesUpserted  int `json:"franchisesUpserted"`
	SeasonsUpserted     int `json:"seasonsUpserted"`
	SeasonTeamsUpserted int `json:"seasonTeamsUpserted"`
}

// GetInitializeResultData queries the initialize workflow result data
func (c *GraphQLClient) GetInitializeResultData(ctx context.Context) (*InitializeResultData, error) {
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
		InitializeResultData *InitializeResultData `json:"initializeResultData"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.InitializeResultData, nil
}

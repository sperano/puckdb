package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagAPIServerAddr    = "api-server-addr"
	FlagSeason           = "season"
	DefaultAPIServerAddr = "http://localhost:8080"
	workflowPollInterval = 2 * time.Second
	workflowPollTimeout  = 30 * time.Minute
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
		endpoint:   endpoint + "/graphql/query",
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Execute sends a GraphQL request and returns the response
func (c *GraphQLClient) Execute(ctx context.Context, query string, variables map[string]any) (*graphQLResponse, error) {
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

// DownloadEverything triggers the downloadEverything mutation
func (c *GraphQLClient) DownloadEverything(ctx context.Context) (bool, error) {
	const mutation = `mutation { downloadEverything }`

	resp, err := c.Execute(ctx, mutation, nil)
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

// WorkflowStatus combines result and progress from a workflow query
type WorkflowStatus struct {
	Result   *model.WorkflowResult
	Progress *model.WorkflowProgress
}

// GetDownloadEverythingStatus queries both workflow result and progress in a single request
func (c *GraphQLClient) GetDownloadEverythingStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadEverythingResult { status failureReason }
		downloadEverythingProgress { total completed }
	}`

	resp, err := c.Execute(ctx, query, nil)
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

	resp, err := c.Execute(ctx, mutation, map[string]any{"season": season})
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

// GetDownloadEverythingForSeasonStatus queries both workflow result and progress for a specific season
func (c *GraphQLClient) GetDownloadEverythingForSeasonStatus(ctx context.Context, season int) (*WorkflowStatus, error) {
	const query = `query($season: Int!) {
		downloadEverythingForSeasonResult(season: $season) { status failureReason }
		downloadEverythingForSeasonProgress(season: $season) { total completed }
	}`

	resp, err := c.Execute(ctx, query, map[string]any{"season": season})
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

func cmdDownload() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "download",
		Short: "Trigger downloadEverything workflow via GraphQL API",
		Long:  `Connects to the API server and triggers the downloadEverything mutation, then monitors the workflow until completion. Use --season to download only a specific season.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(FlagAPIServerAddr, flags.Lookup(FlagAPIServerAddr)); err != nil {
				return err
			}
			return viper.BindPFlag(FlagSeason, flags.Lookup(FlagSeason))
		},
		RunE: runDownload,
	}
	flags := cmd.Flags()
	flags.String(FlagAPIServerAddr, DefaultAPIServerAddr, "API server address (e.g., http://localhost:8080)")
	flags.Int(FlagSeason, 0, "Season year to download (e.g., 2024). If not specified, downloads all seasons.")
	return cmd
}

// statusFetcher is a function type for fetching workflow status (result + progress)
type statusFetcher func(ctx context.Context) (*WorkflowStatus, error)

func runDownload(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	ctx := cmd.Context()
	season := viper.GetInt(FlagSeason)

	var started bool
	var err error
	var getStatus statusFetcher

	if season > 0 {
		log.Info().Str("server", apiAddr).Int("season", season).Msg("Triggering downloadEverythingForSeason workflow")
		started, err = client.DownloadEverythingForSeason(ctx, season)
		getStatus = func(ctx context.Context) (*WorkflowStatus, error) {
			return client.GetDownloadEverythingForSeasonStatus(ctx, season)
		}
	} else {
		log.Info().Str("server", apiAddr).Msg("Triggering downloadEverything workflow")
		started, err = client.DownloadEverything(ctx)
		getStatus = client.GetDownloadEverythingStatus
	}

	if err != nil {
		return fmt.Errorf("failed to trigger download: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	// Monitor the workflow
	return monitorWorkflow(ctx, cmd, getStatus)
}

func monitorWorkflow(ctx context.Context, cmd *cobra.Command, getStatus statusFetcher) error {
	sp := newSpinner(cmd.OutOrStdout(), "Monitoring workflow...")
	sp.Start()
	defer sp.Stop()

	ticker := time.NewTicker(workflowPollInterval)
	defer ticker.Stop()

	timeout := time.After(workflowPollTimeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("workflow monitoring timed out after %v", workflowPollTimeout)
		case <-ticker.C:
			status, err := getStatus(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to get status, retrying...")
				continue
			}

			sp.mu.Lock()
			sp.message = formatStatusMessage(status)
			sp.mu.Unlock()

			switch status.Result.Status {
			case model.TemporalWorkflowStatusCompleted:
				sp.Stop()
				log.Info().Msg("Workflow completed successfully")
				return nil
			case model.TemporalWorkflowStatusFailed:
				if status.Result.FailureReason != nil {
					return fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
				}
				return fmt.Errorf("workflow failed")
			case model.TemporalWorkflowStatusCanceled:
				return fmt.Errorf("workflow was canceled")
			case model.TemporalWorkflowStatusTerminated:
				return fmt.Errorf("workflow was terminated")
			case model.TemporalWorkflowStatusTimedOut:
				return fmt.Errorf("workflow timed out")
			case model.TemporalWorkflowStatusRunning:
				// Continue polling
			case model.TemporalWorkflowStatusUnspecified:
				// Workflow might not have started yet or doesn't exist
				log.Debug().Msg("Workflow status unspecified")
			}
		}
	}
}

func formatStatusMessage(status *WorkflowStatus) string {
	if status.Progress != nil && status.Progress.Total > 0 {
		pct := float64(status.Progress.Completed) / float64(status.Progress.Total) * 100
		return fmt.Sprintf("%.0f%% (%d/%d)", pct, status.Progress.Completed, status.Progress.Total)
	}
	return fmt.Sprintf("Workflow status: %s", status.Result.Status)
}

// ExtractUniquePlayers triggers the extractUniquePlayers mutation
func (c *GraphQLClient) ExtractUniquePlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { extractUniquePlayers }`

	resp, err := c.Execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		ExtractUniquePlayers bool `json:"extractUniquePlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ExtractUniquePlayers, nil
}

// GetExtractUniquePlayersStatus queries both workflow result and progress
func (c *GraphQLClient) GetExtractUniquePlayersStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		extractUniquePlayersResult { status failureReason }
		extractUniquePlayersProgress { total completed }
	}`

	resp, err := c.Execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ExtractUniquePlayersResult   *model.WorkflowResult   `json:"extractUniquePlayersResult"`
		ExtractUniquePlayersProgress *model.WorkflowProgress `json:"extractUniquePlayersProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.ExtractUniquePlayersResult,
		Progress: result.ExtractUniquePlayersProgress,
	}, nil
}

func cmdImport() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "import",
		Short: "Import commands for extracting and processing data",
		Long:  `Import commands for extracting and processing data from Yahoo and NHL sources.`,
	}

	cmd.AddCommand(cmdImportPlayers())
	return cmd
}

func cmdImportPlayers() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "players",
		Short: "Extract unique players from downloaded data",
		Long:  `Triggers the extractUniquePlayers workflow via GraphQL API to extract and merge player data from Yahoo and NHL sources.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			return viper.BindPFlag(FlagAPIServerAddr, flags.Lookup(FlagAPIServerAddr))
		},
		RunE: runImportPlayers,
	}
	flags := cmd.Flags()
	flags.String(FlagAPIServerAddr, DefaultAPIServerAddr, "API server address (e.g., http://localhost:8080)")
	return cmd
}

func runImportPlayers(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	ctx := cmd.Context()

	log.Info().Str("server", apiAddr).Msg("Triggering extractUniquePlayers workflow")
	started, err := client.ExtractUniquePlayers(ctx)
	if err != nil {
		return fmt.Errorf("failed to trigger extract players: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	// Monitor the workflow
	return monitorWorkflow(ctx, cmd, client.GetExtractUniquePlayersStatus)
}

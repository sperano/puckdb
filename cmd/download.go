package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagAPIServerAddr      = "api-server-addr"
	FlagMonitor            = "monitor"
	FlagSeasonConcurrency  = "season-concurrency"
	DefaultAPIServerAddr   = "http://localhost:8080"
	workflowPollInterval   = 2 * time.Second
	workflowPollTimeout    = 30 * time.Minute
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

// DownloadAll triggers the downloadAll mutation with optional season range
func (c *GraphQLClient) DownloadAll(ctx context.Context, input *model.DownloadAllInput) (bool, error) {
	const mutation = `mutation($input: DownloadAllInput) { downloadAll(input: $input) }`

	resp, err := c.Execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		DownloadAll bool `json:"downloadAll"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DownloadAll, nil
}

// GetDownloadAllStatus queries both workflow result and progress in a single request
func (c *GraphQLClient) GetDownloadAllStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadAllResult { status failureReason }
		downloadAllProgress { total completed seasons { startYear total completed } }
	}`

	resp, err := c.Execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		DownloadAllResult   *model.WorkflowResult   `json:"downloadAllResult"`
		DownloadAllProgress *model.WorkflowProgress `json:"downloadAllProgress"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &WorkflowStatus{
		Result:   result.DownloadAllResult,
		Progress: result.DownloadAllProgress,
	}, nil
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
		Short: "Download NHL and Yahoo data",
		Long: `Trigger download workflow via GraphQL API and monitor until completion.
Use --season for a specific season, or --from-season/--to-season for a range.
Use --monitor to watch an existing workflow without triggering a new one.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(FlagAPIServerAddr, flags.Lookup(FlagAPIServerAddr)); err != nil {
				return err
			}
			if err := config.BindSeasonRangeFlags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagMonitor, flags.Lookup(FlagMonitor)); err != nil {
				return err
			}
			return viper.BindPFlag(FlagSeasonConcurrency, flags.Lookup(FlagSeasonConcurrency))
		},
		RunE: runDownload,
	}
	flags := cmd.Flags()
	flags.String(FlagAPIServerAddr, DefaultAPIServerAddr, "API server address (e.g., http://localhost:8080)")
	config.InitSeasonRangeFlags(flags)
	flags.Bool(FlagMonitor, false, "Skip triggering workflow, only monitor existing workflow")
	flags.Int(FlagSeasonConcurrency, 0, "Number of seasons to process concurrently (default: 3)")
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

	if viper.GetBool(FlagMonitor) {
		log.Info().Str("server", apiAddr).Msg("Monitoring existing downloadAll workflow")
		return monitorWorkflow(ctx, cmd, client.GetDownloadAllStatus)
	}

	// Build input from flags
	input := buildDownloadAllInput()

	logEvent := log.Info().Str("server", apiAddr)
	if input.StartSeason != nil {
		logEvent = logEvent.Int("startSeason", *input.StartSeason)
	}
	if input.EndSeason != nil {
		logEvent = logEvent.Int("endSeason", *input.EndSeason)
	}
	if input.SeasonConcurrency != nil {
		logEvent = logEvent.Int("seasonConcurrency", *input.SeasonConcurrency)
	}
	logEvent.Msg("Triggering downloadAll workflow")

	started, err := client.DownloadAll(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to trigger download: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	return monitorWorkflow(ctx, cmd, client.GetDownloadAllStatus)
}

func buildDownloadAllInput() *model.DownloadAllInput {
	input := &model.DownloadAllInput{}

	start, end := config.GetSeasonRange()
	if start > 0 {
		input.StartSeason = &start
	}
	if end > 0 {
		input.EndSeason = &end
	}

	if concurrency := viper.GetInt(FlagSeasonConcurrency); concurrency > 0 {
		input.SeasonConcurrency = &concurrency
	}

	return input
}

func monitorWorkflow(ctx context.Context, cmd *cobra.Command, getStatus statusFetcher) error {
	sp := newSpinner(cmd.OutOrStdout(), "Monitoring workflow...")
	sp.Start()
	defer sp.Stop()

	ticker := time.NewTicker(workflowPollInterval)
	defer ticker.Stop()

	timeout := time.After(workflowPollTimeout)

	for {
		status, err := getStatus(ctx)
		if err != nil {
			sp.mu.Lock()
			sp.lineCount++ // account for the log line we're about to print
			sp.mu.Unlock()
			log.Warn().Err(err).Msg("Failed to get status, retrying...")
		} else {
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

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("workflow monitoring timed out after %v", workflowPollTimeout)
		case <-ticker.C:
			// continue to next iteration
		}
	}
}

const progressBarWidth = 40

func formatStatusMessage(status *WorkflowStatus) string {
	if status.Progress == nil || status.Progress.Total == 0 {
		return fmt.Sprintf("Workflow status: %s", status.Result.Status)
	}

	// Sort seasons by startYear
	seasons := make([]*model.SeasonProgress, len(status.Progress.Seasons))
	copy(seasons, status.Progress.Seasons)
	sort.Slice(seasons, func(i, j int) bool {
		return seasons[i].StartYear < seasons[j].StartYear
	})

	// Collect active seasons (> 0% and < 100%)
	type progressLine struct {
		label     string
		completed int
		total     int
	}
	var activeSeasons []progressLine

	for _, season := range seasons {
		if season.Total > 0 {
			pct := float64(season.Completed) / float64(season.Total) * 100
			if pct > 0 && pct < 100 {
				activeSeasons = append(activeSeasons, progressLine{
					label:     fmt.Sprintf("%d", season.StartYear),
					completed: season.Completed,
					total:     season.Total,
				})
			}
		}
	}

	// Add total line
	allLines := append(activeSeasons, progressLine{
		label:     "Total",
		completed: status.Progress.Completed,
		total:     status.Progress.Total,
	})

	// Calculate max widths for alignment
	maxLabelWidth := 0
	maxCountWidth := 0
	for _, line := range allLines {
		if len(line.label) > maxLabelWidth {
			maxLabelWidth = len(line.label)
		}
		countStr := fmt.Sprintf("%d/%d", line.completed, line.total)
		if len(countStr) > maxCountWidth {
			maxCountWidth = len(countStr)
		}
	}

	// Format lines with aligned columns
	var lines []string
	for _, line := range allLines {
		pct := float64(line.completed) / float64(line.total) * 100
		pctTrunc := int(pct) // truncate, never round up to 100%
		countStr := fmt.Sprintf("%d/%d", line.completed, line.total)
		bar := renderProgressBar(pct, progressBarWidth)
		lines = append(lines, fmt.Sprintf("%*s: %*s %s %3d%%",
			maxLabelWidth, line.label,
			maxCountWidth, countStr,
			bar,
			pctTrunc))
	}

	return strings.Join(lines, "\n")
}

func renderProgressBar(pct float64, width int) string {
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	empty := width - filled
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", empty) + "]"
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
		Short: "Extract and process downloaded data",
		Long:  `Extract and process data from downloaded NHL and Yahoo files.`,
	}

	cmd.AddCommand(cmdImportPlayers())
	return cmd
}

func cmdImportPlayers() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "players",
		Short: "Extract and merge player data",
		Long:  `Extract unique players from NHL boxscores and Yahoo rosters, then merge into unified player records.`,
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

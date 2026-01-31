package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
		httpClient: &http.Client{Timeout: 60 * time.Second},
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
		downloadSeasonsProgress { total completed seasons { startYear total completed } }
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

// WorkflowStatus combines result and progress from a workflow query
type WorkflowStatus struct {
	Result   *model.WorkflowResult
	Progress *model.WorkflowProgress
}

// GetDownloadEverythingStatus queries both workflow result and progress in a single request.
func (c *GraphQLClient) GetDownloadEverythingStatus(ctx context.Context) (*WorkflowStatus, error) {
	const query = `query {
		downloadEverythingResult { status failureReason }
		downloadEverythingProgress { total completed }
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
		downloadYahooPlayersProgress { total completed }
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
		downloadPlayersProgress { total completed }
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

func cmdDownload() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "download",
		Short: "Download NHL and Yahoo data",
		Long: `Trigger download workflow via GraphQL API and monitor until completion.
Use --season for a specific season, or --from-season/--to-season for a range.
Use --monitor to watch an existing workflow without triggering a new one.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindAPIServerAddrFlag(flags); err != nil {
				return err
			}
			if err := config.BindSeasonRangeFlags(flags); err != nil {
				return err
			}
			if err := config.BindMonitorFlag(flags); err != nil {
				return err
			}
			if err := config.BindSkipYahooPlayersFlag(flags); err != nil {
				return err
			}
			if err := config.BindSkipSeasonsFlag(flags); err != nil {
				return err
			}
			return config.BindSeasonConcurrencyFlag(flags)
		},
		RunE: runDownload,
	}
	flags := cmd.Flags()
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitMonitorFlag(flags)
	config.InitSkipYahooPlayersFlag(flags)
	config.InitSkipSeasonsFlag(flags)
	config.InitSeasonConcurrencyFlag(flags)
	return cmd
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
)

// downloadState tracks the current workflow for signal handling
type downloadState struct {
	client  *GraphQLClient
	current workflowType
}

func (s *downloadState) cancel(_ context.Context) {
	// Use a fresh context to cancel since the original may be canceled.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch s.current {
	case workflowYahooPlayers:
		log.Info().Msg("Canceling downloadYahooPlayers workflow...")
		if _, err := s.client.CancelDownloadYahooPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadYahooPlayers workflow")
		} else {
			log.Info().Msg("downloadYahooPlayers workflow canceled")
		}
	case workflowDownloadSeasons:
		log.Info().Msg("Canceling downloadSeasons workflow...")
		if _, err := s.client.CancelDownloadSeasons(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadSeasons workflow")
		} else {
			log.Info().Msg("downloadSeasons workflow canceled")
		}
	case workflowDownloadPlayers:
		log.Info().Msg("Canceling downloadPlayers workflow...")
		if _, err := s.client.CancelDownloadPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadPlayers workflow")
		} else {
			log.Info().Msg("downloadPlayers workflow canceled")
		}
	case workflowNone:
	}
}

func runDownload(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &downloadState{client: client, current: workflowNone}

	// Set up signal handling for Ctrl+C
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println() // newline after ^C
		log.Warn().Msg("Interrupt received, canceling workflow...")
		state.cancel(ctx)
		cancel()
	}()
	defer signal.Stop(sigChan)

	if viper.GetBool(config.FlagMonitor) {
		log.Info().Str("server", apiAddr).Msg("Monitoring existing downloadSeasons workflow")
		state.current = workflowDownloadSeasons
		return monitorWorkflow(ctx, cmd, client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout)
	}

	totalStart := time.Now()
	var yahooPlayersDuration, seasonsDuration, playersDuration time.Duration

	// Step 1: Download Yahoo players (unless skipped)
	if !viper.GetBool(config.FlagSkipYahooPlayers) {
		stepStart := time.Now()
		if err := runDownloadYahooPlayers(ctx, cmd, client, state); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return err
		}
		yahooPlayersDuration = time.Since(stepStart)
		log.Info().Dur("duration", yahooPlayersDuration).Msg("Yahoo players download completed")
	} else {
		log.Info().Msg("Skipping Yahoo players download")
	}

	// Check if context was canceled during Yahoo players download
	if ctx.Err() != nil {
		return fmt.Errorf("workflow canceled by user")
	}

	input := buildDownloadSeasonsInput()

	// Step 2: Download all season data (unless skipped)
	if !viper.GetBool(config.FlagSkipSeasons) {
		stepStart := time.Now()
		state.current = workflowDownloadSeasons

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
		logEvent.Msg("Triggering downloadSeasons workflow")

		started, err := client.DownloadSeasons(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return fmt.Errorf("failed to trigger download: %w", err)
		}

		if !started {
			log.Warn().Msg("Workflow was not started (may already be running)")
		} else {
			log.Info().Msg("Workflow started successfully")
		}

		if err := monitorWorkflow(ctx, cmd, client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return err
		}
		seasonsDuration = time.Since(stepStart)
		log.Info().Dur("duration", seasonsDuration).Msg("Seasons download completed")
	} else {
		log.Info().Msg("Skipping seasons download")
	}

	// Check if context was canceled
	if ctx.Err() != nil {
		return fmt.Errorf("workflow canceled by user")
	}

	// Step 3: Download players (extract player IDs from boxscores)
	stepStart := time.Now()
	if err := runDownloadPlayers(ctx, cmd, client, state, input); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}
	playersDuration = time.Since(stepStart)
	log.Info().Dur("duration", playersDuration).Msg("Players download completed")

	// Log final summary
	totalDuration := time.Since(totalStart)
	log.Info().
		Dur("yahooPlayers", yahooPlayersDuration).
		Dur("seasons", seasonsDuration).
		Dur("players", playersDuration).
		Dur("total", totalDuration).
		Msg("Download completed")

	return nil
}

func runDownloadYahooPlayers(ctx context.Context, cmd *cobra.Command, client *GraphQLClient, state *downloadState) error {
	state.current = workflowYahooPlayers
	log.Info().Msg("Triggering downloadYahooPlayers workflow")

	started, err := client.DownloadYahooPlayers(ctx)
	if err != nil {
		return fmt.Errorf("failed to trigger downloadYahooPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Yahoo players workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Yahoo players workflow started successfully")
	}

	if err := monitorWorkflow(ctx, cmd, client.GetDownloadYahooPlayersStatus, config.DefaultYahooPlayersTimeout); err != nil {
		return fmt.Errorf("downloadYahooPlayers failed: %w", err)
	}

	return nil
}

func runDownloadPlayers(ctx context.Context, cmd *cobra.Command, client *GraphQLClient, state *downloadState, input *model.DownloadSeasonsInput) error {
	state.current = workflowDownloadPlayers
	log.Info().Msg("Triggering downloadPlayers workflow")

	started, err := client.DownloadPlayers(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to trigger downloadPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Download players workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Download players workflow started successfully")
	}

	if err := monitorWorkflow(ctx, cmd, client.GetDownloadPlayersStatus, config.DefaultDownloadPlayersTimeout); err != nil {
		return fmt.Errorf("downloadPlayers failed: %w", err)
	}

	return nil
}

func buildDownloadSeasonsInput() *model.DownloadSeasonsInput {
	input := &model.DownloadSeasonsInput{}

	start, end := config.GetSeasonRange()
	if start > 0 {
		input.StartSeason = &start
	}
	if end > 0 {
		input.EndSeason = &end
	}

	if concurrency := viper.GetInt(config.FlagSeasonConcurrency); concurrency > 0 {
		input.SeasonConcurrency = &concurrency
	}

	return input
}

func monitorWorkflow(ctx context.Context, cmd *cobra.Command, getStatus statusFetcher, pollTimeout time.Duration) error {
	sp := newSpinner(cmd.OutOrStdout(), "Monitoring workflow...")
	sp.Start()
	defer sp.Stop()

	ticker := time.NewTicker(config.DefaultWorkflowPollInterval)
	defer ticker.Stop()

	timeout := time.After(pollTimeout)

	for {
		status, err := getStatus(ctx)
		if err != nil {
			sp.PrintAbove(func() {
				log.Warn().Err(err).Msg("Failed to get status, retrying...")
			})
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
			return fmt.Errorf("workflow monitoring timed out after %v", pollTimeout)
		case <-ticker.C:
			// continue to next iteration
		}
	}
}


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
		bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
		lines = append(lines, fmt.Sprintf("%*s: %*s %s %3d%% ",
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

	resp, err := c.execute(ctx, mutation, nil)
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

	resp, err := c.execute(ctx, query, nil)
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
			return config.BindAPIServerAddrFlag(flags)
		},
		RunE: runImportPlayers,
	}
	flags := cmd.Flags()
	config.InitAPIServerAddrFlag(flags)
	return cmd
}

func runImportPlayers(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(config.FlagAPIServerAddr)
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
	return monitorWorkflow(ctx, cmd, client.GetExtractUniquePlayersStatus, config.DefaultWorkflowPollTimeout)
}

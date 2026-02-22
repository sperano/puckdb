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

// executeBoolMutation handles mutations that return a single boolean field.
func (c *GraphQLClient) executeBoolMutation(ctx context.Context, mutation, field string, variables map[string]any) (bool, error) {
	resp, err := c.execute(ctx, mutation, variables)
	if err != nil {
		return false, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return false, fmt.Errorf("parse response: %w", err)
	}
	var result bool
	if err := json.Unmarshal(raw[field], &result); err != nil {
		return false, fmt.Errorf("parse %s: %w", field, err)
	}
	return result, nil
}

// executeWorkflowStatusQuery handles queries that return result + progress fields.
func (c *GraphQLClient) executeWorkflowStatusQuery(ctx context.Context, query, resultField, progressField string, variables map[string]any) (*WorkflowStatus, error) {
	resp, err := c.execute(ctx, query, variables)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	var status WorkflowStatus
	if data, ok := raw[resultField]; ok {
		if err := json.Unmarshal(data, &status.Result); err != nil {
			return nil, fmt.Errorf("parse %s: %w", resultField, err)
		}
	}
	if data, ok := raw[progressField]; ok {
		if err := json.Unmarshal(data, &status.Progress); err != nil {
			return nil, fmt.Errorf("parse %s: %w", progressField, err)
		}
	}
	return &status, nil
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
	workflowFetchSeasons
	workflowFetchPlayerLogs
	workflowProcessPlayers
	workflowImportSeasons
)

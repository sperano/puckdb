package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/graph/model"
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

// FetchSeasons triggers the fetchSeasons mutation with an optional season range.
func (c *GraphQLClient) FetchSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	const mutation = `mutation($input: SeasonsInput) { fetchSeasons(input: $input) }`

	resp, err := c.execute(ctx, mutation, map[string]any{"input": input})
	if err != nil {
		return false, err
	}

	var result struct {
		FetchSeasons bool `json:"fetchSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.FetchSeasons, nil
}

// CancelFetchSeasons cancels the fetchSeasons workflow
func (c *GraphQLClient) CancelFetchSeasons(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelFetchSeasons }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelFetchSeasons bool `json:"cancelFetchSeasons"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelFetchSeasons, nil
}

// FetchYahooPlayers triggers the fetchYahooPlayers mutation
func (c *GraphQLClient) FetchYahooPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { fetchYahooPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		FetchYahooPlayers bool `json:"fetchYahooPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.FetchYahooPlayers, nil
}

// CancelFetchYahooPlayers cancels the fetchYahooPlayers workflow
func (c *GraphQLClient) CancelFetchYahooPlayers(ctx context.Context) (bool, error) {
	const mutation = `mutation { cancelFetchYahooPlayers }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CancelFetchYahooPlayers bool `json:"cancelFetchYahooPlayers"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CancelFetchYahooPlayers, nil
}

// ProcessPlayers triggers the processPlayers mutation (combined fetch + import)
func (c *GraphQLClient) ProcessPlayers(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	const mutation = `mutation($input: SeasonsInput) { processPlayers(input: $input) }`

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

// ImportSeasons triggers the importSeasons mutation
func (c *GraphQLClient) ImportSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	const mutation = `mutation($input: SeasonsInput) { importSeasons(input: $input) }`

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

// DropDatabase calls the dropDatabase mutation to delete all tables
func (c *GraphQLClient) DropDatabase(ctx context.Context) (bool, error) {
	const mutation = `mutation { dropDatabase }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		DropDatabase bool `json:"dropDatabase"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.DropDatabase, nil
}

// CreateDatabase calls the createDatabase mutation to create all tables
func (c *GraphQLClient) CreateDatabase(ctx context.Context) (bool, error) {
	const mutation = `mutation { createDatabase }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		CreateDatabase bool `json:"createDatabase"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.CreateDatabase, nil
}

// ClearDatabase calls the clearDatabase mutation (drop + create + init)
func (c *GraphQLClient) ClearDatabase(ctx context.Context) (bool, error) {
	const mutation = `mutation { clearDatabase }`

	resp, err := c.execute(ctx, mutation, nil)
	if err != nil {
		return false, err
	}

	var result struct {
		ClearDatabase bool `json:"clearDatabase"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ClearDatabase, nil
}

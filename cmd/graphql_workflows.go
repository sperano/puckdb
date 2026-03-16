package cmd

import (
	"context"

	"github.com/sperano/puckdb/graph/model"
)

// DownloadEverything triggers the downloadEverything mutation
func (c *GraphQLClient) DownloadEverything(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { downloadEverything }`, "downloadEverything", nil)
}

// DownloadEverythingForSeason triggers the downloadEverythingForSeason mutation
func (c *GraphQLClient) DownloadEverythingForSeason(ctx context.Context, season int) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($season: Int!) { downloadEverythingForSeason(season: $season) }`,
		"downloadEverythingForSeason",
		map[string]any{"season": season})
}

// FetchSeasons triggers the fetchSeasons mutation with an optional season range.
func (c *GraphQLClient) FetchSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: SeasonsInput) { fetchSeasons(input: $input) }`,
		"fetchSeasons",
		map[string]any{"input": input})
}

// CancelFetchSeasons cancels the fetchSeasons workflow
func (c *GraphQLClient) CancelFetchSeasons(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelFetchSeasons }`, "cancelFetchSeasons", nil)
}

// FetchPlayerLogs triggers the fetchPlayerLogs mutation for historical seasons.
func (c *GraphQLClient) FetchPlayerLogs(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: SeasonsInput) { fetchPlayerLogs(input: $input) }`,
		"fetchPlayerLogs",
		map[string]any{"input": input})
}

// CancelFetchPlayerLogs cancels the fetchPlayerLogs workflow
func (c *GraphQLClient) CancelFetchPlayerLogs(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelFetchPlayerLogs }`, "cancelFetchPlayerLogs", nil)
}

// FetchYahooPlayers triggers the fetchYahooPlayers mutation
func (c *GraphQLClient) FetchYahooPlayers(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { fetchYahooPlayers }`, "fetchYahooPlayers", nil)
}

// CancelFetchYahooPlayers cancels the fetchYahooPlayers workflow
func (c *GraphQLClient) CancelFetchYahooPlayers(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelFetchYahooPlayers }`, "cancelFetchYahooPlayers", nil)
}

// ProcessPlayers triggers the processPlayers mutation (combined fetch + import)
func (c *GraphQLClient) ProcessPlayers(ctx context.Context, input *model.ProcessPlayersInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: ProcessPlayersInput) { processPlayers(input: $input) }`,
		"processPlayers",
		map[string]any{"input": input})
}

// CancelProcessPlayers cancels the processPlayers workflow
func (c *GraphQLClient) CancelProcessPlayers(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelProcessPlayers }`, "cancelProcessPlayers", nil)
}

// ImportSeasons triggers the importSeasons mutation
func (c *GraphQLClient) ImportSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: SeasonsInput) { importSeasons(input: $input) }`,
		"importSeasons",
		map[string]any{"input": input})
}

// CancelImportSeasons cancels the importSeasons workflow
func (c *GraphQLClient) CancelImportSeasons(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelImportSeasons }`, "cancelImportSeasons", nil)
}

// ImportPlayerLogs triggers the importPlayerLogs mutation
func (c *GraphQLClient) ImportPlayerLogs(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: SeasonsInput) { importPlayerLogs(input: $input) }`,
		"importPlayerLogs",
		map[string]any{"input": input})
}

// CancelImportPlayerLogs cancels the importPlayerLogs workflow
func (c *GraphQLClient) CancelImportPlayerLogs(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelImportPlayerLogs }`, "cancelImportPlayerLogs", nil)
}

// Initialize triggers the initialize mutation
func (c *GraphQLClient) Initialize(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { initialize }`, "initialize", nil)
}

// CancelInitialize cancels the initialize workflow
func (c *GraphQLClient) CancelInitialize(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelInitialize }`, "cancelInitialize", nil)
}

// FlushRedisDB calls the flushRedisDB mutation to flush all keys in the configured Redis DB
func (c *GraphQLClient) FlushRedisDB(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { flushRedisDB }`, "flushRedisDB", nil)
}

// DropDatabase calls the dropDatabase mutation to delete all tables
func (c *GraphQLClient) DropDatabase(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { dropDatabase }`, "dropDatabase", nil)
}

// CreateDatabase calls the createDatabase mutation to create all tables
func (c *GraphQLClient) CreateDatabase(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { createDatabase }`, "createDatabase", nil)
}

// ClearDatabase calls the clearDatabase mutation (drop + create + init)
func (c *GraphQLClient) ClearDatabase(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { clearDatabase }`, "clearDatabase", nil)
}

// ExtractBoxscorePlayers triggers the extractBoxscorePlayers mutation to extract and cache player data.
func (c *GraphQLClient) ExtractBoxscorePlayers(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: SeasonsInput) { extractBoxscorePlayers(input: $input) }`,
		"extractBoxscorePlayers",
		map[string]any{"input": input})
}

// CancelExtractBoxscorePlayers cancels the extractBoxscorePlayers workflow.
func (c *GraphQLClient) CancelExtractBoxscorePlayers(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelExtractBoxscorePlayers }`, "cancelExtractBoxscorePlayers", nil)
}

// FetchPlayerLandings triggers the fetchPlayerLandings mutation to download player landing pages.
func (c *GraphQLClient) FetchPlayerLandings(ctx context.Context, input *model.FetchPlayerLandingsInput) (bool, error) {
	return c.executeBoolMutation(ctx,
		`mutation($input: FetchPlayerLandingsInput) { fetchPlayerLandings(input: $input) }`,
		"fetchPlayerLandings",
		map[string]any{"input": input})
}

// CancelFetchPlayerLandings cancels the fetchPlayerLandings workflow.
func (c *GraphQLClient) CancelFetchPlayerLandings(ctx context.Context) (bool, error) {
	return c.executeBoolMutation(ctx, `mutation { cancelFetchPlayerLandings }`, "cancelFetchPlayerLandings", nil)
}

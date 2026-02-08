package worker

import (
	"encoding/json"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDInitialize = "initialize"

// InitializeResult contains the results of the initialization workflow.
type InitializeResult struct {
	FranchisesDownloaded int  `json:"franchisesDownloaded"`
	FranchisesFromCache  bool `json:"franchisesFromCache"`
	FranchisesUpserted   int  `json:"franchisesUpserted"`
	SeasonsDownloaded    int  `json:"seasonsDownloaded"`
	SeasonsFromCache     bool `json:"seasonsFromCache"`
	SeasonsUpserted      int  `json:"seasonsUpserted"`
	SeasonTeamsUpserted  int  `json:"seasonTeamsUpserted"`
}

// InitializeWorkflow downloads and upserts reference data (franchises, seasons, league structure).
// This workflow populates static NHL reference data and historical league alignments.
func InitializeWorkflow(ctx workflow.Context) (InitializeResult, error) {
	logger := workflow.GetLogger(ctx)
	result := InitializeResult{}

	activityOpts := defaultActivityOptions()
	ctx = workflow.WithActivityOptions(ctx, activityOpts)

	// Step 1: Download franchises
	logger.Info("Downloading NHL franchises")
	var franchisesDownloadResult DownloadFranchisesResult
	if err := workflow.ExecuteActivity(ctx, DownloadFranchisesActivity).Get(ctx, &franchisesDownloadResult); err != nil {
		return result, err
	}
	result.FranchisesDownloaded = franchisesDownloadResult.Count
	result.FranchisesFromCache = franchisesDownloadResult.FromCache

	// Step 2: Upsert franchises to database
	logger.Info("Upserting franchises to database")
	var franchisesUpsertResult UpsertFranchisesResult
	if err := workflow.ExecuteActivity(ctx, UpsertFranchisesActivity).Get(ctx, &franchisesUpsertResult); err != nil {
		return result, err
	}
	result.FranchisesUpserted = franchisesUpsertResult.FranchisesUpserted

	// Step 3: Download seasons manifest
	logger.Info("Downloading NHL seasons manifest")
	var seasonsManifestResult DownloadSeasonsManifestResult
	if err := workflow.ExecuteActivity(ctx, DownloadSeasonsManifestActivity).Get(ctx, &seasonsManifestResult); err != nil {
		return result, err
	}
	result.SeasonsDownloaded = seasonsManifestResult.Count
	result.SeasonsFromCache = seasonsManifestResult.FromCache

	// Step 4: Upsert seasons to database
	logger.Info("Upserting seasons to database")
	var seasonsUpsertResult UpsertSeasonsResult
	if err := workflow.ExecuteActivity(ctx, UpsertSeasonsActivity).Get(ctx, &seasonsUpsertResult); err != nil {
		return result, err
	}
	result.SeasonsUpserted = seasonsUpsertResult.SeasonsUpserted

	// Step 5: Download standings for each season and upsert teams
	// Read seasons from cache to get list
	seasons, err := readSeasonsFromCache(ctx)
	if err != nil {
		return result, err
	}

	logger.Info("Downloading standings for all seasons", "count", len(seasons))

	for _, season := range seasons {
		seasonID := season.ID.ToInt()

		// Download standings for this season
		var standingsResult DownloadSeasonStandingsResult
		if err := workflow.ExecuteActivity(ctx, DownloadSeasonStandingsActivity, seasonID).Get(ctx, &standingsResult); err != nil {
			logger.Warn("Failed to download standings for season", "season", seasonID, "error", err)
			continue // Skip this season but continue with others
		}

		// Upsert teams for this season
		var teamsResult UpsertSeasonTeamsResult
		if err := workflow.ExecuteActivity(ctx, UpsertSeasonTeamsActivity, seasonID).Get(ctx, &teamsResult); err != nil {
			logger.Warn("Failed to upsert teams for season", "season", seasonID, "error", err)
			continue
		}

		result.SeasonTeamsUpserted += teamsResult.TeamsUpserted
	}

	logger.Info("Initialization complete",
		"franchises_downloaded", result.FranchisesDownloaded,
		"franchises_upserted", result.FranchisesUpserted,
		"seasons_downloaded", result.SeasonsDownloaded,
		"seasons_upserted", result.SeasonsUpserted,
		"season_teams_upserted", result.SeasonTeamsUpserted)

	return result, nil
}

// readSeasonsFromCache is a side effect that reads the seasons manifest from cache.
// This is safe because the manifest was just downloaded/cached in a previous activity.
func readSeasonsFromCache(ctx workflow.Context) ([]nhl.SeasonInfo, error) {
	var seasons []nhl.SeasonInfo

	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		fs := cache.NewSimpleCache()
		file := cache.SeasonsManifestFile{}
		data, err := fs.Read(file)
		if err != nil {
			return nil
		}
		var s []nhl.SeasonInfo
		if err := json.Unmarshal(data, &s); err != nil {
			return nil
		}
		return s
	}).Get(&seasons)

	if err != nil {
		return nil, err
	}

	return seasons, nil
}

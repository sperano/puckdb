package worker

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDInitialize = "initialize"

// Group indices for Initialize workflow progress
const (
	GroupFetchFranchises = iota
	GroupUpsertFranchises
	GroupFetchSeasons
	GroupUpsertSeasons
	GroupInitializeSeasonTeams
)

// NewInitializeProgressReport creates the initial progress structure for the Initialize workflow.
func NewInitializeProgressReport() *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Fetching franchises...", Bars: []ProgressBar{{Total: 1}}},
			{Header: "Upserting franchises...", Bars: []ProgressBar{{Total: 1}}},
			{Header: "Fetching seasons...", Bars: []ProgressBar{{Total: 1}}},
			{Header: "Upserting seasons...", Bars: []ProgressBar{{Total: 1}}},
			{Header: "Upserting season teams...", Bars: []ProgressBar{{}}}, // Total set later
		},
	}
}

// InitializeResult contains the results of the initialization workflow.
type InitializeResult struct {
	FranchisesFetched   int  `json:"franchisesFetched"`
	FranchisesFromCache bool `json:"franchisesFromCache"`
	FranchisesUpserted  int  `json:"franchisesUpserted"`
	SeasonsFetched      int  `json:"seasonsFetched"`
	SeasonsFromCache    bool `json:"seasonsFromCache"`
	SeasonsUpserted     int  `json:"seasonsUpserted"`
	SeasonTeamsUpserted int  `json:"seasonTeamsUpserted"`
}

// InitializeWorkflow downloads and upserts reference data (franchises, seasons, league structure).
// This workflow populates static NHL reference data and historical league alignments.
func InitializeWorkflow(ctx workflow.Context) (InitializeResult, error) {
	logger := workflow.GetLogger(ctx)
	result := InitializeResult{}

	// Set up progress tracking
	tracker := NewReportTracker(NewInitializeProgressReport())
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return result, err
	}

	activityOpts := defaultActivityOptions()
	ctx = workflow.WithActivityOptions(ctx, activityOpts)

	// Phase 1: Fetch franchises
	tracker.StartGroup(ctx, GroupFetchFranchises)
	logger.Info("Phase 1: Fetching NHL franchises")
	var franchisesDownloadResult DownloadFranchisesResult
	if err := workflow.ExecuteActivity(ctx, DownloadFranchisesActivity).Get(ctx, &franchisesDownloadResult); err != nil {
		return result, err
	}
	result.FranchisesFetched = franchisesDownloadResult.Count
	result.FranchisesFromCache = franchisesDownloadResult.FromCache
	tracker.CompleteGroup(ctx, GroupFetchFranchises,
		fmt.Sprintf("Fetched %d franchises in %s.", franchisesDownloadResult.Count, tracker.GetElapsed(ctx, GroupFetchFranchises)))

	// Phase 2: Upsert franchises to database
	tracker.StartGroup(ctx, GroupUpsertFranchises)
	logger.Info("Phase 2: Upserting franchises to database")
	var franchisesUpsertResult UpsertFranchisesResult
	if err := workflow.ExecuteActivity(ctx, UpsertFranchisesActivity).Get(ctx, &franchisesUpsertResult); err != nil {
		return result, err
	}
	result.FranchisesUpserted = franchisesUpsertResult.FranchisesUpserted
	tracker.CompleteGroup(ctx, GroupUpsertFranchises,
		fmt.Sprintf("Upserted %d franchises in %s.", franchisesUpsertResult.FranchisesUpserted, tracker.GetElapsed(ctx, GroupUpsertFranchises)))

	// Phase 3: Fetch seasons manifest
	tracker.StartGroup(ctx, GroupFetchSeasons)
	logger.Info("Phase 3: Fetching NHL seasons manifest")
	var seasonsManifestResult DownloadSeasonsManifestResult
	if err := workflow.ExecuteActivity(ctx, DownloadSeasonsManifestActivity).Get(ctx, &seasonsManifestResult); err != nil {
		return result, err
	}
	result.SeasonsFetched = seasonsManifestResult.Count
	result.SeasonsFromCache = seasonsManifestResult.FromCache
	tracker.CompleteGroup(ctx, GroupFetchSeasons,
		fmt.Sprintf("Fetched %d seasons in %s.", seasonsManifestResult.Count, tracker.GetElapsed(ctx, GroupFetchSeasons)))

	// Phase 4: Upsert seasons to database
	tracker.StartGroup(ctx, GroupUpsertSeasons)
	logger.Info("Phase 4: Upserting seasons to database")
	var seasonsUpsertResult UpsertSeasonsResult
	if err := workflow.ExecuteActivity(ctx, UpsertSeasonsActivity).Get(ctx, &seasonsUpsertResult); err != nil {
		return result, err
	}
	result.SeasonsUpserted = seasonsUpsertResult.SeasonsUpserted
	tracker.CompleteGroup(ctx, GroupUpsertSeasons,
		fmt.Sprintf("Upserted %d seasons in %s.", seasonsUpsertResult.SeasonsUpserted, tracker.GetElapsed(ctx, GroupUpsertSeasons)))

	// Phase 5: Download standings and upsert teams for each season (concurrent)
	seasons, err := readSeasonsFromCache(ctx)
	if err != nil {
		return result, err
	}

	tracker.SetBarTotal(GroupInitializeSeasonTeams, 0, len(seasons))
	tracker.StartGroup(ctx, GroupInitializeSeasonTeams)

	seasonIDs := make([]int, len(seasons))
	for i, season := range seasons {
		seasonIDs[i] = season.ID.ToInt()
	}

	startActivity := func(ctx workflow.Context, index int) workflow.Future {
		return workflow.ExecuteActivity(ctx, InitializeSeasonTeamsActivity, seasonIDs[index])
	}
	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var activityResult InitializeSeasonTeamsResult
		if err := future.Get(ctx, &activityResult); err != nil {
			return err
		}
		result.SeasonTeamsUpserted += activityResult.UpsertResult.TeamsUpserted
		return nil
	}
	if err := tracker.RunWorkerPool(ctx, GroupInitializeSeasonTeams, 0, len(seasonIDs), config.DefaultSeasonConcurrency, startActivity, handler); err != nil {
		return result, err
	}

	tracker.CompleteGroup(ctx, GroupInitializeSeasonTeams,
		fmt.Sprintf("Upserted %d season teams in %s.", result.SeasonTeamsUpserted, tracker.GetElapsed(ctx, GroupInitializeSeasonTeams)))

	return result, nil
}

// readSeasonsFromCache is a side effect that reads the seasons manifest from cache.
// This is safe because the manifest was just downloaded/cached in a previous activity.
func readSeasonsFromCache(ctx workflow.Context) ([]nhl.SeasonInfo, error) {
	var seasons []nhl.SeasonInfo

	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		repos := store.NewDefaultRepos()
		if !repos.Season.ManifestExists() {
			return nil
		}
		s, err := repos.Season.GetManifest()
		if err != nil {
			return nil
		}
		return s
	}).Get(&seasons)

	if err != nil {
		return nil, err
	}

	return seasons, nil
}

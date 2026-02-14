package worker

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDInitialize = "initialize"

// Phase IDs for initialization workflow
const (
	PhaseFetchFranchises = iota + 1
	PhaseUpsertFranchises
	PhaseFetchSeasons
	PhaseUpsertSeasons
	PhaseInitializeSeasonTeams
)

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

	// Set up progress tracking with phases
	// CompletedDescription is set dynamically via SetItemCompletedDescription with counts/elapsed
	phases := []PhaseInfo{
		{ID: PhaseFetchFranchises, Description: "Fetching franchises...", Total: 1},
		{ID: PhaseUpsertFranchises, Description: "Upserting franchises...", Total: 1},
		{ID: PhaseFetchSeasons, Description: "Fetching seasons...", Total: 1},
		{ID: PhaseUpsertSeasons, Description: "Upserting seasons...", Total: 1},
		{ID: PhaseInitializeSeasonTeams, Description: "Upserting season teams...", Total: 0}, // Total set later
	}
	tracker := NewProgressTrackerWithPhases(phases)
	//tracker.SetMessage("Starting initialization")
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return result, err
	}

	activityOpts := defaultActivityOptions()
	ctx = workflow.WithActivityOptions(ctx, activityOpts)

	// Phase 1: Fetch franchises
	tracker.MarkItemStarted(ctx, PhaseFetchFranchises)
	logger.Info("Phase 1: Fetching NHL franchises")
	var franchisesDownloadResult DownloadFranchisesResult
	if err := workflow.ExecuteActivity(ctx, DownloadFranchisesActivity).Get(ctx, &franchisesDownloadResult); err != nil {
		return result, err
	}
	result.FranchisesFetched = franchisesDownloadResult.Count
	result.FranchisesFromCache = franchisesDownloadResult.FromCache
	tracker.IncrementItem(PhaseFetchFranchises)
	tracker.SetItemCompletedDescription(PhaseFetchFranchises,
		fmt.Sprintf("Fetched %d franchises in %s.", franchisesDownloadResult.Count, tracker.GetItemElapsed(ctx, PhaseFetchFranchises)))
	tracker.MarkItemCompleted(ctx, PhaseFetchFranchises)

	// Phase 2: Upsert franchises to database
	tracker.MarkItemStarted(ctx, PhaseUpsertFranchises)
	logger.Info("Phase 2: Upserting franchises to database")
	var franchisesUpsertResult UpsertFranchisesResult
	if err := workflow.ExecuteActivity(ctx, UpsertFranchisesActivity).Get(ctx, &franchisesUpsertResult); err != nil {
		return result, err
	}
	result.FranchisesUpserted = franchisesUpsertResult.FranchisesUpserted
	tracker.IncrementItem(PhaseUpsertFranchises)
	tracker.SetItemCompletedDescription(PhaseUpsertFranchises,
		fmt.Sprintf("Upserted %d franchises in %s.", franchisesUpsertResult.FranchisesUpserted, tracker.GetItemElapsed(ctx, PhaseUpsertFranchises)))
	tracker.MarkItemCompleted(ctx, PhaseUpsertFranchises)

	// Phase 3: Fetch seasons manifest
	tracker.MarkItemStarted(ctx, PhaseFetchSeasons)
	logger.Info("Phase 3: Fetching NHL seasons manifest")
	var seasonsManifestResult DownloadSeasonsManifestResult
	if err := workflow.ExecuteActivity(ctx, DownloadSeasonsManifestActivity).Get(ctx, &seasonsManifestResult); err != nil {
		return result, err
	}
	result.SeasonsFetched = seasonsManifestResult.Count
	result.SeasonsFromCache = seasonsManifestResult.FromCache
	tracker.IncrementItem(PhaseFetchSeasons)
	tracker.SetItemCompletedDescription(PhaseFetchSeasons,
		fmt.Sprintf("Fetched %d seasons in %s.", seasonsManifestResult.Count, tracker.GetItemElapsed(ctx, PhaseFetchSeasons)))
	tracker.MarkItemCompleted(ctx, PhaseFetchSeasons)

	// Phase 4: Upsert seasons to database
	tracker.MarkItemStarted(ctx, PhaseUpsertSeasons)
	logger.Info("Phase 4: Upserting seasons to database")
	var seasonsUpsertResult UpsertSeasonsResult
	if err := workflow.ExecuteActivity(ctx, UpsertSeasonsActivity).Get(ctx, &seasonsUpsertResult); err != nil {
		return result, err
	}
	result.SeasonsUpserted = seasonsUpsertResult.SeasonsUpserted
	tracker.IncrementItem(PhaseUpsertSeasons)
	tracker.SetItemCompletedDescription(PhaseUpsertSeasons,
		fmt.Sprintf("Upserted %d seasons in %s.", seasonsUpsertResult.SeasonsUpserted, tracker.GetItemElapsed(ctx, PhaseUpsertSeasons)))
	tracker.MarkItemCompleted(ctx, PhaseUpsertSeasons)

	// Phase 5: Download standings and upsert teams for each season (concurrent)
	// Read seasons from cache to get list
	seasons, err := readSeasonsFromCache(ctx)
	if err != nil {
		return result, err
	}

	tracker.SetItemTotal(PhaseInitializeSeasonTeams, len(seasons))
	tracker.MarkItemStarted(ctx, PhaseInitializeSeasonTeams)

	// Process seasons concurrently using worker pool with error tolerance
	seasonIDs := make([]int, len(seasons))
	for i, season := range seasons {
		seasonIDs[i] = season.ID.ToInt()
	}

	teamsUpserted, err := runInitializeSeasonTeamsConcurrent(ctx, tracker, seasonIDs)
	if err != nil {
		return result, err
	}
	result.SeasonTeamsUpserted = teamsUpserted
	tracker.SetItemCompletedDescription(PhaseInitializeSeasonTeams,
		fmt.Sprintf("Upserted %d season teams in %s.", teamsUpserted, tracker.GetItemElapsed(ctx, PhaseInitializeSeasonTeams)))
	tracker.MarkItemCompleted(ctx, PhaseInitializeSeasonTeams)
	return result, nil
}

// runInitializeSeasonTeamsConcurrent processes seasons concurrently and returns total teams upserted.
// Uses RunWorkerPoolWithHandler to aggregate team counts from each activity result.
func runInitializeSeasonTeamsConcurrent(ctx workflow.Context, tracker *ProgressTracker, seasonIDs []int) (int, error) {
	totalTeamsUpserted := 0

	startActivity := func(ctx workflow.Context, index int) workflow.Future {
		return workflow.ExecuteActivity(ctx, InitializeSeasonTeamsActivity, seasonIDs[index])
	}

	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var result InitializeSeasonTeamsResult
		if err := future.Get(ctx, &result); err != nil {
			return err
		}
		totalTeamsUpserted += result.UpsertResult.TeamsUpserted
		return nil
	}

	err := tracker.RunWorkerPoolForItem(ctx, len(seasonIDs), config.DefaultSeasonConcurrency, PhaseInitializeSeasonTeams, startActivity, handler)
	if err != nil {
		return totalTeamsUpserted, err
	}

	return totalTeamsUpserted, nil
}

// readSeasonsFromCache is a side effect that reads the seasons manifest from cache.
// This is safe because the manifest was just downloaded/cached in a previous activity.
func readSeasonsFromCache(ctx workflow.Context) ([]nhl.SeasonInfo, error) {
	var seasons []nhl.SeasonInfo

	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		fs := store.NewStore()
		file := store.SeasonsManifestFile{}
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

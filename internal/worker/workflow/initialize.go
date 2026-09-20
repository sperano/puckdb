package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/log"
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
func NewInitializeProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Fetching franchises...", Bars: []shared.ProgressBar{{}}},
			{Header: "Upserting franchises...", Bars: []shared.ProgressBar{{}}},
			{Header: "Fetching seasons...", Bars: []shared.ProgressBar{{}}},
			{Header: "Upserting seasons...", Bars: []shared.ProgressBar{{}}},
			{Header: "Upserting season teams...", Bars: []shared.ProgressBar{{}}},
		},
	}
}

// InitializeResult contains the results of the initialization workflow.
type InitializeResult struct {
	FranchisesOrigin    core.DataOrigin `json:"franchisesOrigin"`
	FranchisesUpserted  int             `json:"franchisesUpserted"`
	SeasonsOrigin       core.DataOrigin `json:"seasonsOrigin"`
	SeasonsUpserted     int             `json:"seasonsUpserted"`
	SeasonTeamsUpserted int             `json:"seasonTeamsUpserted"`
}

// InitializeWorkflow downloads and upserts reference data (franchises, seasons, league structure).
// This workflow populates static NHL reference data and historical league alignments.
func InitializeWorkflow(ctx workflow.Context) (InitializeResult, error) {
	logger := workflow.GetLogger(ctx)
	result := InitializeResult{}
	// Set up progress tracking
	tracker, err := shared.InitTracker(ctx, NewInitializeProgressReport())
	if err != nil {
		return result, err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	// Phase 1: Fetch franchises
	origin, err := fetchFranchises(ctx, tracker, logger)
	if err != nil {
		return result, err
	}
	result.FranchisesOrigin = origin
	// Phase 2: Upsert franchises to database
	upserted, err := upsertFranchises(ctx, tracker, logger)
	if err != nil {
		return result, err
	}
	result.FranchisesUpserted = upserted
	// Phase 3: Fetch seasons manifest
	seasons, orig, err := fetchSeasonsManifestInit(ctx, tracker, logger)
	if err != nil {
		return result, err
	}
	result.SeasonsOrigin = orig
	// Phase 4: Upsert seasons to database
	upserted, err = upsertSeasons(ctx, tracker, logger)
	if err != nil {
		return result, err
	}
	result.SeasonsUpserted = upserted
	// Phase 5: Download standings and upsert teams for each season (concurrent)
	upserted, err = upsertSeasonTeams(ctx, tracker, logger, seasons)
	if err != nil {
		return result, err
	}
	result.SeasonTeamsUpserted = upserted
	return result, nil
}

func fetchFranchises(ctx workflow.Context, tracker *shared.ReportTracker, logger log.Logger) (core.DataOrigin, error) {
	logger.Info("Phase 1: Fetching NHL franchises")
	tracker.SetBarTotal(GroupFetchFranchises, 0, 1)
	tracker.StartGroup(ctx, GroupFetchFranchises)
	var (
		result worknhl.FetchFranchisesResult
		fa     *worknhl.FranchiseActivities
	)
	if err := workflow.ExecuteActivity(ctx, fa.FetchFranchises).Get(ctx, &result); err != nil {
		return core.OriginUnknown, err
	}
	msg := fmt.Sprintf("Fetched franchises from %s in %s.", result.Origin, tracker.GetElapsed(ctx, GroupFetchFranchises))
	tracker.CompleteGroup(ctx, GroupFetchFranchises, msg)
	return result.Origin, nil
}

func upsertFranchises(ctx workflow.Context, tracker *shared.ReportTracker, logger log.Logger) (int, error) {
	logger.Info("Phase 2: Upserting franchises to database")
	tracker.SetBarTotal(GroupUpsertFranchises, 0, 1)
	tracker.StartGroup(ctx, GroupUpsertFranchises)
	var (
		result worknhl.UpsertFranchisesResult
		fa     *worknhl.FranchiseActivities
	)
	if err := workflow.ExecuteActivity(ctx, fa.UpsertFranchises).Get(ctx, &result); err != nil {
		return 0, err
	}
	msg := fmt.Sprintf("Upserted %d franchises in %s.", result.FranchisesUpserted, tracker.GetElapsed(ctx, GroupUpsertFranchises))
	tracker.CompleteGroup(ctx, GroupUpsertFranchises, msg)
	return result.FranchisesUpserted, nil
}

func fetchSeasonsManifestInit(ctx workflow.Context, tracker *shared.ReportTracker, logger log.Logger) ([]nhl.SeasonInfo, core.DataOrigin, error) {
	logger.Info("Phase 3: Fetching NHL seasons manifest")
	tracker.SetBarTotal(GroupFetchSeasons, 0, 1)
	tracker.StartGroup(ctx, GroupFetchSeasons)
	var result worknhl.FetchSeasonsManifestResult
	if err := workflow.ExecuteActivity(ctx,
		((*worknhl.SeasonsActivities)(nil)).FetchSeasonsManifest,
		(*model.SeasonsInput)(nil)).Get(ctx, &result); err != nil {
		return nil, core.OriginUnknown, err
	}
	msg := fmt.Sprintf("Fetched %d seasons from %s in %s.", len(result.Seasons), result.Origin, tracker.GetElapsed(ctx, GroupFetchSeasons))
	tracker.CompleteGroup(ctx, GroupFetchSeasons, msg)
	return result.Seasons, result.Origin, nil
}

func upsertSeasons(ctx workflow.Context, tracker *shared.ReportTracker, logger log.Logger) (int, error) {
	logger.Info("Phase 4: Upserting seasons to database")
	tracker.SetBarTotal(GroupUpsertSeasons, 0, 1)
	tracker.StartGroup(ctx, GroupUpsertSeasons)
	var (
		sa     *worknhl.SeasonsActivities
		result worknhl.UpsertSeasonsResult
	)
	if err := workflow.ExecuteActivity(ctx, sa.UpsertSeasons).Get(ctx, &result); err != nil {
		return 0, err
	}
	msg := fmt.Sprintf("Upserted %d seasons in %s.", result.SeasonsUpserted, tracker.GetElapsed(ctx, GroupUpsertSeasons))
	tracker.CompleteGroup(ctx, GroupUpsertSeasons, msg)
	return result.SeasonsUpserted, nil
}

func upsertSeasonTeams(ctx workflow.Context, tracker *shared.ReportTracker, logger log.Logger, seasonIDs []nhl.SeasonInfo) (int, error) {
	logger.Info("Phase 5: Upserting season teams to database")
	tracker.SetBarTotal(GroupInitializeSeasonTeams, 0, len(seasonIDs))
	tracker.StartGroup(ctx, GroupInitializeSeasonTeams)
	var (
		sa       *worknhl.SeasonsActivities
		upserted int
	)
	startActivity := func(ctx workflow.Context, index int) workflow.Future {
		return workflow.ExecuteActivity(ctx, sa.InitializeSeasonTeamsActivity, seasonIDs[index].ID)
	}
	handler := func(ctx workflow.Context, index int, future workflow.Future) error {
		var activityResult worknhl.InitializeSeasonTeamsResult
		if err := future.Get(ctx, &activityResult); err != nil {
			return err
		}
		upserted += activityResult.UpsertResult.TeamsUpserted // safe: Temporal workflow handlers are single-threaded
		return nil
	}
	if err := tracker.RunWorkerPool(ctx, GroupInitializeSeasonTeams, 0, len(seasonIDs), config.DefaultSeasonConcurrency, startActivity, handler); err != nil {
		return 0, err
	}

	msg := fmt.Sprintf("Upserted %d season teams in %s.", upserted, tracker.GetElapsed(ctx, GroupInitializeSeasonTeams))
	tracker.CompleteGroup(ctx, GroupInitializeSeasonTeams, msg)
	return upserted, nil
}

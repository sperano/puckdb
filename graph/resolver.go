package graph

import (
	"context"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/redis"
	"github.com/sperano/puckdb/temporal"
	"github.com/sperano/puckdb/worker"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// Imports are managed by goimports

type Resolver struct {
	TemporalClient client.Client
}

var temporalStatusToGQL = map[temporalEnums.WorkflowExecutionStatus]model.TemporalWorkflowStatus{
	temporalEnums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED:      model.TemporalWorkflowStatusUnspecified,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_RUNNING:          model.TemporalWorkflowStatusRunning,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_COMPLETED:        model.TemporalWorkflowStatusCompleted,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_FAILED:           model.TemporalWorkflowStatusFailed,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_CANCELED:         model.TemporalWorkflowStatusCanceled,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_TERMINATED:       model.TemporalWorkflowStatusTerminated,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW: model.TemporalWorkflowStatusContinuedAsNew,
	temporalEnums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:        model.TemporalWorkflowStatusTimedOut,
}

func clearDatabase(ctx context.Context) (bool, error) {
	_, err := dropDatabase(ctx)
	if err != nil {
		return false, err
	}
	return createDatabase(ctx)
}

func dropDatabase(ctx context.Context) (bool, error) {
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return false, err
	}
	defer pool.Close()
	if err := database.DropEverything(ctx, pool); err != nil {
		return false, err
	}
	return true, nil
}

func createDatabase(_ context.Context) (bool, error) {
	if err := database.DoMigration(); err != nil {
		return false, err
	}
	return true, nil
}

func flushRedisDB(ctx context.Context) (bool, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()
	if err := redis.FlushDB(ctx, redisClient); err != nil {
		return false, err
	}
	return true, nil
}

func currentFantasyGameKey(_ context.Context) (int, error) {
	content, err := worker.DownloadFromYahoo(http.YahooFantasyGameURL())
	if err != nil {
		return 0, err
	}
	fantasy, err := cache.ParseXML(content)
	if err != nil {
		return 0, err
	}
	return fantasy.Game.Key, nil
}

func (r *Resolver) downloadSeasons(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDDownloadSeasons)
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.DownloadSeasonsWorkflow, input); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelDownloadSeasons(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDDownloadSeasons, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) downloadSeasonsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDDownloadSeasons)
}

func (r *Resolver) downloadSeasonsProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDDownloadSeasons)
}

func (r *Resolver) downloadYahooPlayers(ctx context.Context) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDDownloadYahooPlayers)
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.DownloadYahooPlayersWorkflow, nil); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelDownloadYahooPlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDDownloadYahooPlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) downloadYahooPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDDownloadYahooPlayers)
}

func (r *Resolver) downloadYahooPlayersProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDDownloadYahooPlayers)
}

func (r *Resolver) downloadPlayers(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDDownloadPlayers)
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.DownloadPlayersWorkflow, input); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelDownloadPlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDDownloadPlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) downloadPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDDownloadPlayers)
}

func (r *Resolver) downloadPlayersProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDDownloadPlayers)
}

func (r *Resolver) importPlayers(ctx context.Context, _ *model.DownloadSeasonsInput) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDImportPlayers)
	// ImportPlayersWorkflow doesn't use season parameters - they'll be used by importSeasonsWorkflow
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.ImportPlayersWorkflow, (*worker.ImportPlayersInput)(nil)); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelImportPlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDImportPlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) importPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDImportPlayers)
}

func (r *Resolver) importPlayersProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDImportPlayers)
}

func (r *Resolver) importPlayersResultData(ctx context.Context) (*model.ImportPlayersResultData, error) {
	run := r.TemporalClient.GetWorkflow(ctx, worker.WorkflowIDImportPlayers, "")

	var result worker.ImportPlayersResult
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}

	// Convert worker result to GraphQL model
	trulyUnmatched := make([]*model.TrulyUnmatchedPlayer, len(result.TrulyUnmatched))
	for i, p := range result.TrulyUnmatched {
		trulyUnmatched[i] = &model.TrulyUnmatchedPlayer{
			YahooID:     p.YahooID,
			FirstName:   p.FirstName,
			LastName:    p.LastName,
			NhlGames:    p.NHLGames,
			NhlPlayerID: int(p.NHLPlayerID),
			NhlName:     p.NHLName,
		}
	}

	return &model.ImportPlayersResultData{
		TotalPlayers:          result.TotalPlayers,
		ImportedPlayers:       result.ImportedPlayers,
		MatchedWithYahoo:      result.MatchedWithYahoo,
		TotalYahooPlayers:     result.TotalYahooPlayers,
		SkippedNonNHL:         result.SkippedNonNHL,
		VerifiedNonNHLThisRun: result.VerifiedNonNHLThisRun,
		TrulyUnmatched:        trulyUnmatched,
		Errors:                result.Errors,
	}, nil
}

func (r *Resolver) importSeasons(ctx context.Context, input *model.DownloadSeasonsInput) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDImportSeasons)
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.ImportSeasonsWorkflow, input); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelImportSeasons(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDImportSeasons, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) importSeasonsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDImportSeasons)
}

func (r *Resolver) importSeasonsProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDImportSeasons)
}

func (r *Resolver) initialize(ctx context.Context) (bool, error) {
	opts := workflowOptions(worker.WorkflowIDInitialize)
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.InitializeWorkflow); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) cancelInitialize(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDInitialize, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) initializeResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDInitialize)
}

func (r *Resolver) initializeProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDInitialize)
}

func (r *Resolver) initializeResultData(ctx context.Context) (*model.InitializeResultData, error) {
	run := r.TemporalClient.GetWorkflow(ctx, worker.WorkflowIDInitialize, "")

	var result worker.InitializeResult
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}

	return &model.InitializeResultData{
		FranchisesUpserted:  result.FranchisesUpserted,
		SeasonsUpserted:     result.SeasonsUpserted,
		SeasonTeamsUpserted: result.SeasonTeamsUpserted,
	}, nil
}

func (r *Resolver) downloadEverythingResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDDownloadEverything)
}

func (r *Resolver) downloadEverythingForSeasonResult(ctx context.Context, season int) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDDownloadEverythingForSeason(season))
}

func (r *Resolver) getWorkflowResult(ctx context.Context, workflowID string) (*model.WorkflowResult, error) {
	resp, err := r.TemporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		return nil, err
	}

	status := temporalStatusToGQL[resp.WorkflowExecutionInfo.Status]
	result := &model.WorkflowResult{
		Status: status,
	}

	// If the workflow failed, try to get the failure reason
	if status == model.TemporalWorkflowStatusFailed {
		// Get the workflow run to extract the error
		run := r.TemporalClient.GetWorkflow(ctx, workflowID, "")
		var dummy any
		err := run.Get(ctx, &dummy)
		if err != nil {
			result.FailureReason = ptrString(err.Error())
		}
	}

	return result, nil
}

func (r *Resolver) downloadEverythingProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDDownloadEverything)
}

func (r *Resolver) downloadEverythingForSeasonProgress(ctx context.Context, season int) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDDownloadEverythingForSeason(season))
}

func (r *Resolver) queryWorkflowProgress(ctx context.Context, workflowID string) (*model.WorkflowProgress, error) {
	queryCtx, cancel := context.WithTimeout(ctx, config.DefaultQueryTimeout)
	defer cancel()

	response, err := r.TemporalClient.QueryWorkflow(queryCtx, workflowID, "", worker.ProgressQueryName)
	if err != nil {
		// Workflow may not exist or not have query handler registered yet
		return nil, err
	}

	var progress worker.WorkflowProgress
	if err := response.Get(&progress); err != nil {
		return nil, err
	}

	result := &model.WorkflowProgress{
		Total:     progress.Total,
		Completed: progress.Completed,
		Message:   ptrStringIfNotEmpty(progress.Message),
	}

	if len(progress.Items) > 0 {
		result.Items = make([]*model.ProgressItem, len(progress.Items))

		// Identify items that need child workflow queries
		type childQuery struct {
			index  int
			itemID int
		}
		var queries []childQuery

		for i, item := range progress.Items {
			result.Items[i] = &model.ProgressItem{
				ID:          item.ID,
				Description: ptrStringIfNotEmpty(item.Description),
				Total:       item.Total,
				Completed:   item.Completed,
				Started:     item.Started,
				StartedAt:   ptrStringIfNotEmpty(item.StartedAt),
				CompletedAt: ptrStringIfNotEmpty(item.CompletedAt),
			}

			// Mark started but incomplete items for parallel querying
			if item.Started && item.Completed < item.Total {
				queries = append(queries, childQuery{index: i, itemID: item.ID})
			}
		}

		// Query child workflows in parallel
		if len(queries) > 0 {
			type childResult struct {
				index    int
				progress *worker.WorkflowProgress
			}
			results := make(chan childResult, len(queries))

			for _, q := range queries {
				go func(idx, id int) {
					results <- childResult{
						index:    idx,
						progress: r.queryChildItemProgress(ctx, id),
					}
				}(q.index, q.itemID)
			}

			// Collect results
			for range queries {
				cr := <-results
				if cr.progress != nil && cr.progress.Completed > result.Items[cr.index].Completed {
					diff := cr.progress.Completed - result.Items[cr.index].Completed
					result.Items[cr.index].Completed = cr.progress.Completed
					result.Completed += diff
				}
			}
		}
	}

	return result, nil
}

// queryChildItemProgress queries a child workflow for its progress.
// For season-based workflows, itemID is the startYear.
// Returns nil if the child workflow doesn't exist or can't be queried.
func (r *Resolver) queryChildItemProgress(ctx context.Context, itemID int) *worker.WorkflowProgress {
	childCtx, cancel := context.WithTimeout(ctx, config.DefaultChildWorkflowTimeout)
	defer cancel()

	childWorkflowID := worker.WorkflowIDDownloadSeason(itemID)
	response, err := r.TemporalClient.QueryWorkflow(childCtx, childWorkflowID, "", worker.ProgressQueryName)
	if err != nil {
		return nil
	}

	var childProgress worker.WorkflowProgress
	if err := response.Get(&childProgress); err != nil {
		return nil
	}

	return &childProgress
}

func ptrString(s string) *string {
	return &s
}

func ptrStringIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// TODO move to temporal/worker
func workflowOptions(id string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                  id,
		TaskQueue:           temporal.QueueTasks,
		WorkflowTaskTimeout: config.DefaultWorkflowTaskTimeout,
	}
}

package graph

import (
	"context"
	"time"

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
	_, err = createDatabase(ctx)
	if err != nil {
		return false, err
	}
	return initDatabase(ctx)
}

func dropDatabase(ctx context.Context) (bool, error) {
	db := database.FromContext(ctx)
	if err := database.DropEverything(db); err != nil {
		return false, err
	}
	return true, nil
}

func createDatabase(ctx context.Context) (bool, error) {
	db := database.FromContext(ctx)
	if err := database.DoMigration(db); err != nil {
		return false, err
	}
	return true, nil
}

func initDatabase(ctx context.Context) (bool, error) {
	q := database.QueriesFromContext(ctx)
	if err := database.EnsureNHLWithSQLC(ctx, q); err != nil {
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

// Divisions is the resolver for the divisions field.
func divisions(ctx context.Context, obj *model.NHLConference) ([]*model.NHLDivision, error) {
	q := database.QueriesFromContext(ctx)
	divs, err := q.GetNHLDivisionsByConference(ctx, int64(obj.ID))
	if err != nil {
		return nil, err
	}
	gqlDivs := make([]*model.NHLDivision, len(divs))
	for i, d := range divs {
		gqlDivs[i] = &model.NHLDivision{
			ID:   int(d.ID),
			Name: d.Name,
			Conference: &model.NHLConference{
				ID:   int(d.ConfID),
				Name: d.ConfName,
			},
		}
	}
	return gqlDivs, nil
}

// Teams is the resolver for the teams field.
func teams(ctx context.Context, obj *model.NHLDivision) ([]*model.NHLTeam, error) {
	q := database.QueriesFromContext(ctx)
	teams, err := q.GetNHLTeamsByDivision(ctx, int64(obj.ID))
	if err != nil {
		return nil, err
	}
	gqlTeams := make([]*model.NHLTeam, len(teams))
	for i, t := range teams {
		gqlTeams[i] = &model.NHLTeam{
			ID:            int(t.ID),
			City:          t.City,
			Name:          t.Name,
			Abbreviation:  t.Abbreviation,
			NhlHomeLink:   t.NhlHomeLink,
			YahooHomeLink: t.YahooHomeLink,
			SmallLogoURL:  t.SmallLogoUrl,
			LargeLogoURL:  t.LargeLogoUrl,
			Division: &model.NHLDivision{
				ID:   int(t.DivID),
				Name: t.DivName,
				Conference: &model.NHLConference{
					ID:   int(t.ConfID),
					Name: t.ConfName,
				},
			},
		}
	}
	return gqlTeams, nil
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

func (r *Resolver) downloadDay(ctx context.Context, input model.DownloadDayInput) (bool, error) {
	day, err := time.Parse(config.DateFormat, input.Day)
	if err != nil {
		return false, err
	}

	workflowID := worker.WorkflowIDDownloadDay(input.Season, day)
	opts := workflowOptions(workflowID)

	workerInput := &worker.DownloadDayWorkflowInput{
		Day:       day,
		StartYear: input.Season,
	}

	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, worker.DownloadDayWorkflow, workerInput); err != nil {
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

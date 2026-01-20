package graph

import (
	"context"
	"time"

	"github.com/sperano/puckdb/cache"
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

func clearCache(ctx context.Context) (bool, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()
	if err := redis.ClearCache(ctx, redisClient); err != nil {
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

func (r *Resolver) cancelExtractUniquePlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDExtractUniquePlayers, ""); err != nil {
		return false, err
	}
	return true, nil
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

func (r *Resolver) extractUniquePlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDExtractUniquePlayers)
}

func (r *Resolver) extractUniquePlayersProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDExtractUniquePlayers)
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
	// Use a longer timeout for workflow queries (default is 10s which can be too short)
	queryTimeout := 30 * time.Second
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
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
	}

	if len(progress.Seasons) > 0 {
		result.Seasons = make([]*model.SeasonProgress, len(progress.Seasons))
		for i, s := range progress.Seasons {
			result.Seasons[i] = &model.SeasonProgress{
				StartYear: s.StartYear,
				Total:     s.Total,
				Completed: s.Completed,
			}
		}
	}

	return result, nil
}

func ptrString(s string) *string {
	return &s
}

// TODO move to temporal/worker
func workflowOptions(id string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                  id,
		TaskQueue:           temporal.QueueTasks,
		WorkflowTaskTimeout: 60 * time.Second,
	}
}

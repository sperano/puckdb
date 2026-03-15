package graph

import (
	"bytes"
	"context"
	"encoding/gob"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/temporal"
	"github.com/sperano/puckdb/worker"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// Imports are managed by goimports

type Resolver struct {
	TemporalClient client.Client
	RedisClient    cache.Client
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

func (r *Resolver) clearDatabase(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDResetDatabase, worker.ResetDatabaseWorkflow, nil)
}

func (r *Resolver) dropDatabase(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDDropDatabase, worker.DropDatabaseWorkflow, nil)
}

func (r *Resolver) createDatabase(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDMigrateDatabase, worker.MigrateDatabaseWorkflow, nil)
}

func (r *Resolver) flushRedisDB(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDFlushRedis, worker.FlushRedisWorkflow, nil)
}

func (r *Resolver) fetchSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDFetchSeasons, worker.FetchSeasonsWorkflow, input)
}

func (r *Resolver) cancelFetchSeasons(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDFetchSeasons, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) fetchSeasonsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDFetchSeasons)
}

func (r *Resolver) fetchSeasonsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDFetchSeasons)
}

func (r *Resolver) fetchPlayerLogs(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDFetchPlayerLogs, worker.FetchPlayerLogsWorkflow, input)
}

func (r *Resolver) cancelFetchPlayerLogs(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDFetchPlayerLogs, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) fetchPlayerLogsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDFetchPlayerLogs)
}

func (r *Resolver) fetchPlayerLogsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDFetchPlayerLogs)
}

func (r *Resolver) fetchYahooPlayers(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDFetchYahooPlayers, worker.FetchYahooPlayersWorkflow, (*worker.FetchYahooPlayersInput)(nil))
}

func (r *Resolver) cancelFetchYahooPlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDFetchYahooPlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) fetchYahooPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDFetchYahooPlayers)
}

func (r *Resolver) fetchYahooPlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDFetchYahooPlayers)
}

func (r *Resolver) processPlayers(ctx context.Context, input *model.ProcessPlayersInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &worker.ProcessPlayersInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, worker.WorkflowIDProcessPlayers, worker.ProcessPlayersWorkflow, workflowInput)
}

func (r *Resolver) cancelProcessPlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDProcessPlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) processPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDProcessPlayers)
}

func (r *Resolver) processPlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDProcessPlayers)
}

func (r *Resolver) processPlayersResultData(ctx context.Context) (*model.ProcessPlayersResultData, error) {
	run := r.TemporalClient.GetWorkflow(ctx, worker.WorkflowIDProcessPlayers, "")

	var result worker.ProcessPlayersResult
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}

	// Convert worker result to GraphQL model
	trulyUnmatched := make([]*model.TrulyUnmatchedPlayer, len(result.TrulyUnmatched))
	for i, p := range result.TrulyUnmatched {
		trulyUnmatched[i] = &model.TrulyUnmatchedPlayer{
			YahooID:     int(p.YahooID),
			FirstName:   p.FirstName,
			LastName:    p.LastName,
			NhlGames:    p.NHLGames,
			NhlPlayerID: int(p.NHLPlayerID),
			NhlName:     p.NHLName,
		}
	}

	return &model.ProcessPlayersResultData{
		TotalPlayers:          result.TotalPlayers,
		ImportedPlayers:       result.ImportedPlayers,
		MatchedWithYahoo:      result.MatchedWithYahoo,
		Downloaded:            result.Downloaded,
		CacheHits:             result.CacheHits,
		Missing:               result.Missing,
		TotalYahooPlayers:     result.TotalYahooPlayers,
		SkippedNonNHL:         result.SkippedNonNHL,
		VerifiedNonNHLThisRun: result.VerifiedNonNHLThisRun,
		TrulyUnmatched:        trulyUnmatched,
		Errors:                result.Errors,
	}, nil
}

func (r *Resolver) importSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDImportSeasons, worker.ImportSeasonsWorkflow, input)
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

func (r *Resolver) importSeasonsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDImportSeasons)
}

func (r *Resolver) extractBoxscorePlayers(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDExtractBoxscorePlayers, worker.ExtractBoxscorePlayersWorkflow, input)
}

func (r *Resolver) cancelExtractBoxscorePlayers(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDExtractBoxscorePlayers, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) extractBoxscorePlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDExtractBoxscorePlayers)
}

func (r *Resolver) extractBoxscorePlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDExtractBoxscorePlayers)
}

func (r *Resolver) fetchPlayerLandings(ctx context.Context, input *model.FetchPlayerLandingsInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &worker.FetchPlayerLandingsInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, worker.WorkflowIDFetchPlayerLandings, worker.FetchPlayerLandingsWorkflow, workflowInput)
}

func (r *Resolver) cancelFetchPlayerLandings(ctx context.Context) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, worker.WorkflowIDFetchPlayerLandings, ""); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) fetchPlayerLandingsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, worker.WorkflowIDFetchPlayerLandings)
}

func (r *Resolver) fetchPlayerLandingsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDFetchPlayerLandings)
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

func (r *Resolver) initializeProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, worker.WorkflowIDInitialize)
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

func (r *Resolver) queryProgressReport(ctx context.Context, workflowID string) (*model.ProgressReport, error) {
	data, err := cache.LoadProgressReport(ctx, r.RedisClient, workflowID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	var progress worker.ProgressReport
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&progress); err != nil {
		return nil, err
	}

	result := &model.ProgressReport{
		Total:     progress.Total,
		Completed: progress.Completed,
		Message:   ptrStringIfNotEmpty(progress.Message),
		Groups:    make([]*model.ProgressGroup, 0, len(progress.Groups)),
	}

	// Collect bars that have child workflows to query
	var childQueries []childQuery

	for _, g := range progress.Groups {
		// Only include groups that have started (completed or in-progress)
		if g.StartedAt == 0 {
			continue
		}

		groupIdx := len(result.Groups)
		bars := make([]*model.ProgressBar, len(g.Bars))
		for j, b := range g.Bars {
			bars[j] = &model.ProgressBar{
				Label:   ptrStringIfNotEmpty(b.Label),
				Current: b.Current,
				Total:   b.Total,
				Started: b.Started,
			}
			// Queue all bars with child workflows — started state will be
			// inferred from Redis child progress in mergeChildWorkflowProgress.
			if b.ChildWorkflowID != "" {
				childQueries = append(childQueries, childQuery{
					groupIdx:   groupIdx,
					barIdx:     j,
					workflowID: b.ChildWorkflowID,
				})
			}
		}
		result.Groups = append(result.Groups, &model.ProgressGroup{
			Header:       g.Header,
			CompletedMsg: g.CompletedMsg,
			Bars:         bars,
			StartedAt:    g.StartedAt,
			CompletedAt:  g.CompletedAt,
		})
	}

	// Merge live child progress from Redis
	if len(childQueries) > 0 {
		r.mergeChildWorkflowProgress(ctx, result, childQueries)
	}

	return result, nil
}

// mergeChildWorkflowProgress reads child workflow ProgressReports from Redis
// and extracts the first bar's Current value as the child's day/player progress.
// This is exactly-once safe because ProgressReports are saved via local activities
// (workflow-side), not from retryable activities.
func (r *Resolver) mergeChildWorkflowProgress(ctx context.Context, report *model.ProgressReport, queries []childQuery) {
	if len(queries) == 0 {
		return
	}

	var workflowIDs []string
	queryMap := make(map[string][]childQuery)
	for _, q := range queries {
		if _, exists := queryMap[q.workflowID]; !exists {
			workflowIDs = append(workflowIDs, q.workflowID)
		}
		queryMap[q.workflowID] = append(queryMap[q.workflowID], q)
	}

	reportMap, err := cache.LoadProgressReportBatch(ctx, r.RedisClient, workflowIDs)
	if err != nil {
		return // Gracefully degrade - show parent-only progress
	}

	for workflowID, data := range reportMap {
		var childReport worker.ProgressReport
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&childReport); err != nil {
			continue
		}
		// Extract the first group's first bar as the child's primary progress.
		if len(childReport.Groups) == 0 || len(childReport.Groups[0].Bars) == 0 {
			continue
		}
		childBar := childReport.Groups[0].Bars[0]

		for _, q := range queryMap[workflowID] {
			bar := report.Groups[q.groupIdx].Bars[q.barIdx]
			bar.Started = true

			current := childBar.Current
			if current > bar.Total {
				current = bar.Total // Safety cap
			}
			if current > bar.Current {
				diff := current - bar.Current
				bar.Current = current
				report.Completed += diff
			}
		}
	}
}

// childQuery identifies a bar that needs its child workflow queried for progress.
type childQuery struct {
	groupIdx   int
	barIdx     int
	workflowID string
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

// executeWorkflow starts a workflow and returns success status.
func (r *Resolver) executeWorkflow(ctx context.Context, workflowID string, workflow interface{}, arg interface{}) (bool, error) {
	opts := workflowOptions(workflowID)
	var err error
	if arg == nil {
		_, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow)
	} else {
		_, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow, arg)
	}
	return err == nil, err
}

// TODO move to temporal/worker
func workflowOptions(id string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                  id,
		TaskQueue:           temporal.QueueTasks,
		WorkflowTaskTimeout: config.DefaultWorkflowTaskTimeout,
	}
}

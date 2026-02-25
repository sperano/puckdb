package graph

import (
	"context"

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

func (r *Resolver) fetchPlayerLogsProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDFetchPlayerLogs, worker.WorkflowIDFetchSeasonPlayerLogs)
}

func (r *Resolver) fetchYahooPlayers(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, worker.WorkflowIDFetchYahooPlayers, worker.FetchYahooPlayersWorkflow, nil)
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

func (r *Resolver) processPlayers(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &worker.ProcessPlayersInput{}
	if input != nil {
		workflowInput.StartSeason = input.StartSeason
		workflowInput.EndSeason = input.EndSeason
		workflowInput.SeasonConcurrency = input.SeasonConcurrency
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

func (r *Resolver) processPlayersProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	progress, err := r.queryWorkflowProgress(ctx, worker.WorkflowIDProcessPlayers, nil)
	if err != nil {
		return nil, err
	}

	// Check if Phase 1 (extracting player IDs) is in progress - query child workflow
	const phaseExtractIDs = 1
	if progress != nil && len(progress.Items) > 0 {
		for i, item := range progress.Items {
			if item.ID == phaseExtractIDs && item.Started && (item.CompletedAt == nil || *item.CompletedAt == "") {
				// Phase 1 in progress - query the child workflow for real progress
				childProgress := r.queryChildWorkflowProgress(ctx, worker.WorkflowIDImportNHLTeamsAndPlayers)
				if childProgress != nil {
					progress.Items[i].Total = childProgress.Total
					progress.Items[i].Completed = childProgress.Completed
				}
				break
			}
		}
	}

	return progress, nil
}

// queryChildWorkflowProgress queries a specific child workflow by ID for its progress.
func (r *Resolver) queryChildWorkflowProgress(ctx context.Context, workflowID string) *worker.WorkflowProgress {
	childCtx, cancel := context.WithTimeout(ctx, config.DefaultChildWorkflowTimeout)
	defer cancel()

	response, err := r.TemporalClient.QueryWorkflow(childCtx, workflowID, "", worker.ProgressQueryName)
	if err != nil {
		return nil
	}

	var childProgress worker.WorkflowProgress
	if err := response.Get(&childProgress); err != nil {
		return nil
	}

	return &childProgress
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

func (r *Resolver) importSeasonsProgress(ctx context.Context) (*model.WorkflowProgress, error) {
	return r.queryWorkflowProgress(ctx, worker.WorkflowIDImportSeasons, worker.WorkflowIDImportSeason)
}

func (r *Resolver) extractBoxscorePlayers(ctx context.Context, input *model.ExtractBoxscorePlayersInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &worker.ExtractBoxscorePlayersInput{}
	if input != nil {
		workflowInput.StartSeason = input.StartSeason
		workflowInput.EndSeason = input.EndSeason
		workflowInput.SeasonConcurrency = input.SeasonConcurrency
		workflowInput.TTLMinutes = input.TTLMinutes
	}
	return r.executeWorkflow(ctx, worker.WorkflowIDExtractBoxscorePlayers, worker.ExtractBoxscorePlayersWorkflow, workflowInput)
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

// childWorkflowIDFunc maps an item ID (e.g., season start year) to a child workflow ID.
type childWorkflowIDFunc func(itemID int) string

func (r *Resolver) queryWorkflowProgress(ctx context.Context, workflowID string, childIDFunc childWorkflowIDFunc) (*model.WorkflowProgress, error) {
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
		Total:           progress.Total,
		Completed:       progress.Completed,
		Message:         ptrStringIfNotEmpty(progress.Message),
		Header:          ptrStringIfNotEmpty(progress.Header),
		CompletedHeader: ptrStringIfNotEmpty(progress.CompletedHeader),
	}

	// Convert display style if set
	if progress.DisplayStyle != "" {
		style := model.ProgressDisplayStyle(progress.DisplayStyle)
		result.DisplayStyle = &style
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
				ID:                   item.ID,
				Description:          ptrStringIfNotEmpty(item.Description),
				CompletedDescription: ptrStringIfNotEmpty(item.CompletedDescription),
				Total:                item.Total,
				Completed:            item.Completed,
				Started:              item.Started,
				StartedAt:            ptrStringIfNotEmpty(item.StartedAt),
				CompletedAt:          ptrStringIfNotEmpty(item.CompletedAt),
			}

			// Mark started items for parallel querying (only if we have a child ID function)
			// Query if: incomplete (Completed < Total) OR deferred totals (Total == 0)
			if childIDFunc != nil && item.Started && (item.Completed < item.Total || item.Total == 0) {
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
						progress: r.queryChildItemProgress(ctx, childIDFunc, id),
					}
				}(q.index, q.itemID)
			}

			// Collect results - propagate both Total and Completed from children
			for range queries {
				cr := <-results
				if cr.progress != nil {
					if cr.progress.Total > 0 && cr.progress.Total != result.Items[cr.index].Total {
						totalDiff := cr.progress.Total - result.Items[cr.index].Total
						result.Items[cr.index].Total = cr.progress.Total
						result.Total += totalDiff
					}
					if cr.progress.Completed > result.Items[cr.index].Completed {
						diff := cr.progress.Completed - result.Items[cr.index].Completed
						result.Items[cr.index].Completed = cr.progress.Completed
						result.Completed += diff
					}
				}
			}
		}
	}

	return result, nil
}

func (r *Resolver) queryProgressReport(ctx context.Context, workflowID string) (*model.ProgressReport, error) {
	queryCtx, cancel := context.WithTimeout(ctx, config.DefaultQueryTimeout)
	defer cancel()

	response, err := r.TemporalClient.QueryWorkflow(queryCtx, workflowID, "", worker.ProgressReportQueryName)
	if err != nil {
		return nil, err
	}

	var progress worker.ProgressReport
	if err := response.Get(&progress); err != nil {
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
			// If bar has a child workflow and isn't complete, queue it for querying
			if b.ChildWorkflowID != "" && b.Current < b.Total {
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

	// Query child workflows in parallel and merge progress
	if len(childQueries) > 0 {
		r.mergeChildWorkflowProgress(ctx, result, childQueries)
	}

	return result, nil
}

// mergeChildWorkflowProgress reads progress from Redis and updates bar progress.
// Activities write progress to Redis keys like "fetch-season-1945" or "extract-season-1945".
func (r *Resolver) mergeChildWorkflowProgress(ctx context.Context, report *model.ProgressReport, queries []childQuery) {
	if len(queries) == 0 {
		return
	}

	// Collect all workflow IDs for batch read
	var workflowIDs []string
	queryMap := make(map[string][]childQuery)

	for _, q := range queries {
		if _, exists := queryMap[q.workflowID]; !exists {
			workflowIDs = append(workflowIDs, q.workflowID)
		}
		queryMap[q.workflowID] = append(queryMap[q.workflowID], q)
	}

	// Batch read from Redis
	progressMap, err := cache.LoadProgressBatch(ctx, r.RedisClient, workflowIDs)
	if err != nil {
		return // Gracefully degrade - show parent-only progress
	}

	// Merge progress into bars
	for workflowID, progress := range progressMap {
		if progress == nil {
			continue
		}
		for _, q := range queryMap[workflowID] {
			bar := report.Groups[q.groupIdx].Bars[q.barIdx]
			if progress.Current > bar.Current {
				diff := progress.Current - bar.Current
				bar.Current = progress.Current
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

// queryChildItemProgress queries a child workflow for its progress.
// For season-based workflows, itemID is the startYear.
// Returns nil if the child workflow doesn't exist or can't be queried.
func (r *Resolver) queryChildItemProgress(ctx context.Context, childIDFunc childWorkflowIDFunc, itemID int) *worker.WorkflowProgress {
	childCtx, cancel := context.WithTimeout(ctx, config.DefaultChildWorkflowTimeout)
	defer cancel()

	childWorkflowID := childIDFunc(itemID)
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

// executeWorkflow starts a workflow and returns success status.
func (r *Resolver) executeWorkflow(ctx context.Context, workflowID string, workflow interface{}, arg interface{}) (bool, error) {
	opts := workflowOptions(workflowID)
	_, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow, arg)
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

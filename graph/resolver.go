package graph

//go:generate go run github.com/99designs/gqlgen generate

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/maurice"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/temporal"
	"github.com/sperano/puckdb/worker/admin"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/workflow"
	"github.com/spf13/viper"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// Imports are managed by goimports

type Resolver struct {
	TemporalClient client.Client
	RedisClient    cache.Client
	MauriceService maurice.Service    // nil if Maurice is not configured
	Queries        *sqlcdb.Queries    // nil-safe: data queries return errors if not wired
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
	return r.executeWorkflow(ctx, admin.WorkflowIDResetDatabase, admin.ResetDatabaseWorkflow, nil)
}

func (r *Resolver) dropDatabase(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, admin.WorkflowIDDropDatabase, admin.DropDatabaseWorkflow, nil)
}

func (r *Resolver) createDatabase(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, admin.WorkflowIDMigrateDatabase, admin.MigrateDatabaseWorkflow, nil)
}

func (r *Resolver) flushRedisDB(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, admin.WorkflowIDFlushRedis, admin.FlushRedisWorkflow, nil)
}

func (r *Resolver) fetchSeasons(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDFetchSeasons, workflow.FetchSeasonsWorkflow, input)
}

func (r *Resolver) cancelFetchSeasons(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDFetchSeasons)
}

func (r *Resolver) fetchSeasonsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDFetchSeasons)
}

func (r *Resolver) fetchSeasonsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDFetchSeasons)
}

func (r *Resolver) fetchPlayerLogs(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDFetchPlayerLogs, workflow.FetchPlayerLogsWorkflow, input)
}

func (r *Resolver) cancelFetchPlayerLogs(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDFetchPlayerLogs)
}

func (r *Resolver) fetchPlayerLogsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDFetchPlayerLogs)
}

func (r *Resolver) fetchPlayerLogsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDFetchPlayerLogs)
}

func (r *Resolver) fetchYahooPlayers(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, workflow.WorkflowIDFetchYahooPlayers, workflow.FetchYahooPlayersWorkflow, (*workflow.FetchYahooPlayersInput)(nil))
}

func (r *Resolver) cancelFetchYahooPlayers(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, workflow.WorkflowIDFetchYahooPlayers)
}

func (r *Resolver) fetchYahooPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, workflow.WorkflowIDFetchYahooPlayers)
}

func (r *Resolver) fetchYahooPlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, workflow.WorkflowIDFetchYahooPlayers)
}

func (r *Resolver) processPlayers(ctx context.Context, input *model.ProcessPlayersInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &workflow.ProcessPlayersInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, workflow.WorkflowIDProcessPlayers, workflow.ProcessPlayersWorkflow, workflowInput)
}

func (r *Resolver) cancelProcessPlayers(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, workflow.WorkflowIDProcessPlayers)
}

func (r *Resolver) processPlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, workflow.WorkflowIDProcessPlayers)
}

func (r *Resolver) processPlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, workflow.WorkflowIDProcessPlayers)
}

func (r *Resolver) processPlayersResultData(ctx context.Context) (*model.ProcessPlayersResultData, error) {
	run := r.TemporalClient.GetWorkflow(ctx, workflow.WorkflowIDProcessPlayers, "")

	var result workflow.ProcessPlayersResult
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
	return r.executeWorkflow(ctx, shared.WorkflowIDImportSeasons, workflow.ImportSeasonsWorkflow, input)
}

func (r *Resolver) cancelImportSeasons(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDImportSeasons)
}

func (r *Resolver) importSeasonsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDImportSeasons)
}

func (r *Resolver) importSeasonsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDImportSeasons)
}

func (r *Resolver) importPlayerLogs(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDImportPlayerLogs, workflow.ImportPlayerLogsWorkflow, input)
}

func (r *Resolver) cancelImportPlayerLogs(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDImportPlayerLogs)
}

func (r *Resolver) importPlayerLogsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDImportPlayerLogs)
}

func (r *Resolver) importPlayerLogsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDImportPlayerLogs)
}

func (r *Resolver) extractBoxscorePlayers(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, workflow.WorkflowIDExtractBoxscorePlayers, workflow.ExtractBoxscorePlayersWorkflow, input)
}

func (r *Resolver) cancelExtractBoxscorePlayers(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, workflow.WorkflowIDExtractBoxscorePlayers)
}

func (r *Resolver) extractBoxscorePlayersResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, workflow.WorkflowIDExtractBoxscorePlayers)
}

func (r *Resolver) extractBoxscorePlayersProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, workflow.WorkflowIDExtractBoxscorePlayers)
}

func (r *Resolver) fetchPlayerLandings(ctx context.Context, input *model.FetchPlayerLandingsInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &workflow.FetchPlayerLandingsInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, workflow.WorkflowIDFetchPlayerLandings, workflow.FetchPlayerLandingsWorkflow, workflowInput)
}

func (r *Resolver) cancelFetchPlayerLandings(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, workflow.WorkflowIDFetchPlayerLandings)
}

func (r *Resolver) fetchPlayerLandingsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, workflow.WorkflowIDFetchPlayerLandings)
}

func (r *Resolver) fetchPlayerLandingsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, workflow.WorkflowIDFetchPlayerLandings)
}

func (r *Resolver) fetchEdgeStats(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDFetchEdgeStats, workflow.FetchEdgeSeasonsWorkflow, input)
}

func (r *Resolver) cancelFetchEdgeStats(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDFetchEdgeStats)
}

func (r *Resolver) fetchEdgeStatsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDFetchEdgeStats)
}

func (r *Resolver) fetchEdgeStatsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDFetchEdgeStats)
}

func (r *Resolver) importEdgeStats(ctx context.Context, input *model.SeasonsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDImportEdgeStats, workflow.ImportEdgeSeasonsWorkflow, input)
}

func (r *Resolver) cancelImportEdgeStats(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, shared.WorkflowIDImportEdgeStats)
}

func (r *Resolver) importEdgeStatsResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, shared.WorkflowIDImportEdgeStats)
}

func (r *Resolver) importEdgeStatsProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, shared.WorkflowIDImportEdgeStats)
}

func (r *Resolver) initialize(ctx context.Context) (bool, error) {
	return r.executeWorkflow(ctx, workflow.WorkflowIDInitialize, workflow.InitializeWorkflow, nil)
}

func (r *Resolver) cancelInitialize(ctx context.Context) (bool, error) {
	return r.cancelWorkflow(ctx, workflow.WorkflowIDInitialize)
}

func (r *Resolver) initializeResult(ctx context.Context) (*model.WorkflowResult, error) {
	return r.getWorkflowResult(ctx, workflow.WorkflowIDInitialize)
}

func (r *Resolver) initializeProgress(ctx context.Context) (*model.ProgressReport, error) {
	return r.queryProgressReport(ctx, workflow.WorkflowIDInitialize)
}

func (r *Resolver) cancelWorkflow(ctx context.Context, workflowID string) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, workflowID, ""); err != nil {
		return false, err
	}
	return true, nil
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

	var progress shared.ProgressReport
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
		var childReport shared.ProgressReport
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&childReport); err != nil {
			continue
		}
		// Sum progress across all child groups and bars.
		// Child workflows may have multiple groups (e.g., days + playoffs).
		if len(childReport.Groups) == 0 {
			continue
		}
		childCurrent := 0
		for _, g := range childReport.Groups {
			for _, b := range g.Bars {
				childCurrent += b.Current
			}
		}

		for _, q := range queryMap[workflowID] {
			bar := report.Groups[q.groupIdx].Bars[q.barIdx]
			bar.Started = true

			current := childCurrent
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


func (r *Resolver) yahooTokenStatus(ctx context.Context) (*model.YahooTokenStatus, error) {
	valid, err := cache.HasValidToken(ctx, r.RedisClient, config.DefaultUser)
	if err != nil {
		return nil, err
	}
	loginURL := "/yahoo/login"
	if publicURL := viper.GetString(config.FlagPublicURL); publicURL != "" {
		loginURL = publicURL + loginURL
	}
	return &model.YahooTokenStatus{
		Valid:    valid,
		LoginURL: loginURL,
	}, nil
}

// Maurice resolver methods

var errMauriceNotConfigured = fmt.Errorf("Maurice AI chat is not configured")

func (r *Resolver) mauriceChat(ctx context.Context, conversationID *string, message string) (*model.MauriceChatResponse, error) {
	if r.MauriceService == nil {
		return nil, errMauriceNotConfigured
	}
	resp, err := r.MauriceService.Chat(ctx, conversationID, message)
	if err != nil {
		return nil, err
	}
	toolsUsed := resp.ToolsUsed
	if toolsUsed == nil {
		toolsUsed = []string{}
	}
	return &model.MauriceChatResponse{
		ConversationID: resp.ConversationID,
		MessageID:      resp.MessageID,
		Content:        resp.Content,
		ToolsUsed:      toolsUsed,
	}, nil
}

func (r *Resolver) mauriceDeleteConversation(ctx context.Context, id string) (bool, error) {
	if r.MauriceService == nil {
		return false, errMauriceNotConfigured
	}
	if err := r.MauriceService.DeleteConversation(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) mauriceConversations(ctx context.Context, limit *int) ([]*model.MauriceConversation, error) {
	if r.MauriceService == nil {
		return nil, errMauriceNotConfigured
	}
	lim := 20
	if limit != nil && *limit > 0 {
		lim = *limit
	}
	convs, err := r.MauriceService.ListConversations(ctx, lim)
	if err != nil {
		return nil, err
	}
	result := make([]*model.MauriceConversation, len(convs))
	for i, c := range convs {
		result[i] = &model.MauriceConversation{
			ID:        c.ID,
			Title:     c.Title,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		}
	}
	return result, nil
}

func (r *Resolver) mauriceConversation(ctx context.Context, id string) (*model.MauriceConversationDetail, error) {
	if r.MauriceService == nil {
		return nil, errMauriceNotConfigured
	}
	conv, msgs, err := r.MauriceService.GetConversation(ctx, id)
	if err != nil {
		return nil, err
	}
	gqlMsgs := make([]*model.MauriceMessage, len(msgs))
	for i, m := range msgs {
		toolsUsed := make([]string, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			toolsUsed[j] = tc.Function.Name
		}
		gqlMsgs[i] = &model.MauriceMessage{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			ToolsUsed: toolsUsed,
			CreatedAt: m.CreatedAt,
		}
	}
	return &model.MauriceConversationDetail{
		Conversation: &model.MauriceConversation{
			ID:        conv.ID,
			Title:     conv.Title,
			CreatedAt: conv.CreatedAt,
			UpdatedAt: conv.UpdatedAt,
		},
		Messages: gqlMsgs,
	}, nil
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
// It deletes any stale progress report from a previous run so the CLI
// doesn't display old completed data before the new workflow writes its own.
func (r *Resolver) executeWorkflow(ctx context.Context, workflowID string, workflow any, arg any) (bool, error) {
	opts := workflowOptions(workflowID)
	var err error
	if arg == nil {
		_, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow)
	} else {
		_, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow, arg)
	}
	if err != nil {
		return false, err
	}
	// Clear stale progress from previous run. The workflow will write fresh
	// progress once it starts, but there's a gap between trigger and first
	// save where the CLI would read the old key.
	_ = cache.DeleteProgressReport(ctx, r.RedisClient, workflowID)
	return true, nil
}

// TODO move to temporal/worker
func workflowOptions(id string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                  id,
		TaskQueue:           temporal.QueueTasks,
		WorkflowTaskTimeout: config.DefaultWorkflowTaskTimeout,
	}
}

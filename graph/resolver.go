package graph

//go:generate go run github.com/99designs/gqlgen generate

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/maurice"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/workflow"
	"github.com/spf13/viper"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// Imports are managed by goimports

// txBeginner is the subset of *pgxpool.Pool the resolver needs to run a
// multi-statement write atomically (createSimPool). Kept as an interface so
// the resolver doesn't hard-depend on pgxpool and tests can inject a fake.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Resolver holds the dependencies used by GraphQL resolvers. MauriceService
// may be nil when Maurice AI chat is not configured — resolvers that need it
// return errMauriceNotConfigured. Queries may be nil when the resolver is
// constructed without a live database connection — data queries then return
// errors rather than panicking. DB is the pgx pool used for resolvers that
// need an explicit transaction (createSimPool); it may be nil when the
// resolver is constructed without a live database connection, in which case
// those mutations return errDatabaseNotConfigured. TemporalClient and
// RedisClient are required.
type Resolver struct {
	TemporalClient client.Client
	RedisClient    *redis.Client
	MauriceService maurice.Service
	Queries        *sqlcdb.Queries
	DB             txBeginner
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

// Simple long-running workflows that need no input conversion, and their
// admin-queue counterparts, have no dedicated resolver.go wrapper: the
// schema.resolvers.go field methods call executeWorkflow / executeAdminWorkflow
// / cancelWorkflow / getWorkflowResult / queryProgressReport directly with the
// workflow's shared.WorkflowID* / worker/admin constant. Only workflows that
// need real input-shape conversion (processPlayers, fetchPlayerLandings,
// fetchAssets) or extra post-processing (processPlayersResultData) get a
// resolver.go method.

func (r *Resolver) processPlayers(ctx context.Context, input *model.ProcessPlayersInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &workflow.ProcessPlayersInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, workflow.WorkflowIDProcessPlayers, workflow.ProcessPlayersWorkflow, workflowInput)
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

func (r *Resolver) fetchPlayerLandings(ctx context.Context, input *model.FetchPlayerLandingsInput) (bool, error) {
	// Convert GraphQL input to workflow input
	workflowInput := &workflow.FetchPlayerLandingsInput{}
	if input != nil {
		workflowInput.BatchSize = input.BatchSize
		workflowInput.Concurrency = input.Concurrency
	}
	return r.executeWorkflow(ctx, workflow.WorkflowIDFetchPlayerLandings, workflow.FetchPlayerLandingsWorkflow, workflowInput)
}

func (r *Resolver) fetchAssets(ctx context.Context, input *model.FetchAssetsInput) (bool, error) {
	workflowInput := &workflow.FetchAssetsInput{}
	if input != nil {
		workflowInput.ClassConcurrency = input.ClassConcurrency
		workflowInput.MaxClassConcurrency = input.MaxClassConcurrency
		workflowInput.RefreshCurrent = input.RefreshCurrent
	}
	return r.executeAssetWorkflow(ctx, shared.WorkflowIDFetchAssets, workflow.FetchAssetsWorkflow, workflowInput)
}

func (r *Resolver) cancelWorkflow(ctx context.Context, workflowID string) (bool, error) {
	if err := r.TemporalClient.CancelWorkflow(ctx, workflowID, ""); err != nil {
		return false, err
	}
	return true, nil
}

// isTerminalFailureStatus reports whether a workflow status is a terminal
// non-success state whose FailureReason should be populated. Completed is a
// success and ContinuedAsNew / Running / Unspecified are not terminal
// failures, so none of them carry a reason.
func isTerminalFailureStatus(s model.TemporalWorkflowStatus) bool {
	switch s {
	case model.TemporalWorkflowStatusFailed,
		model.TemporalWorkflowStatusTimedOut,
		model.TemporalWorkflowStatusTerminated,
		model.TemporalWorkflowStatusCanceled:
		return true
	default:
		return false
	}
}

func (r *Resolver) getWorkflowResult(ctx context.Context, workflowID string) (*model.WorkflowResult, error) {
	resp, err := r.TemporalClient.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		return nil, err
	}

	rawStatus := resp.WorkflowExecutionInfo.Status
	status, ok := temporalStatusToGQL[rawStatus]
	if !ok {
		// A status Temporal added that this build doesn't know about.
		// Surface it explicitly rather than silently returning the zero
		// enum (which reads as UNSPECIFIED and hides the mismatch).
		return nil, fmt.Errorf("workflow %s has unknown temporal status %v", workflowID, rawStatus)
	}
	result := &model.WorkflowResult{
		Status: status,
	}

	// For every terminal non-success state (Failed, TimedOut, Terminated,
	// Canceled) fetch the run result to extract the failure reason. run.Get
	// returns the terminal error for all of these, not just Failed.
	if isTerminalFailureStatus(status) {
		run := r.TemporalClient.GetWorkflow(ctx, workflowID, "")
		var dummy any
		if err := run.Get(ctx, &dummy); err != nil {
			result.FailureReason = new(err.Error())
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
			if b.ProgressSourceKey != "" {
				childQueries = append(childQueries, childQuery{
					groupIdx:   groupIdx,
					barIdx:     j,
					workflowID: b.ProgressSourceKey,
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

			current := min(childCurrent, bar.Total) // Safety cap
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
	valid, err := cache.HasUsableToken(ctx, r.RedisClient, config.DefaultUser)
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

var errMauriceNotConfigured = errors.New("maurice AI chat is not configured")

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

func ptrStringIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

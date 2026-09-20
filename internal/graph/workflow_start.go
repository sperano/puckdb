package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// startWorkflow is the shared core that triggers a workflow with the given
// options, optional input argument, and post-start Redis cleanup. The three
// helpers below pin down the queue selection and progress-cleanup policy for
// each workflow class.
//
// An already-running workflow is an error to the caller — nothing was
// started — and performs no cleanup: the running execution's progress is
// live. This relies on buildWorkflowOptions setting
// WorkflowExecutionErrorWhenAlreadyStarted — without it the SDK swallows
// the conflict and hands back the running execution's run, which would
// flow into cleanup under an identity that need not match its stamp.
func (r *Resolver) startWorkflow(ctx context.Context, opts client.StartWorkflowOptions, workflow any, arg any, clearProgress bool) (bool, error) {
	var run client.WorkflowRun
	var err error
	if arg == nil {
		run, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow)
	} else {
		run, err = r.TemporalClient.ExecuteWorkflow(ctx, opts, workflow, arg)
	}
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return false, fmt.Errorf("workflow %s is already running (run %s): %w", opts.ID, alreadyStarted.RunId, err)
	}
	if err != nil {
		return false, err
	}
	if clearProgress {
		r.clearStaleProgress(ctx, opts.ID, run.GetRunID())
	}
	return true, nil
}

// clearStaleProgress removes a previous run's report so the gap between
// trigger and the new run's first save doesn't show the old one. The
// delete is conditional on the run stamp because the worker may already
// have saved the new run's report before ExecuteWorkflow returned — an
// unconditional delete here used to wipe it.
//
// Errors are logged, not returned: the workflow has started, and failing
// the mutation would tell the caller it hadn't.
func (r *Resolver) clearStaleProgress(ctx context.Context, workflowID, liveRunID string) {
	deleted, err := cache.DeleteStaleProgressReport(ctx, r.RedisClient, workflowID, liveRunID)
	if err != nil {
		log.Warn().Err(err).
			Str("workflowID", workflowID).
			Str("runID", liveRunID).
			Msg("Workflow started but stale progress cleanup failed")
		return
	}
	if deleted {
		log.Debug().Str("workflowID", workflowID).Str("runID", liveRunID).Msg("Cleared previous run's progress report")
	}
}

// executeWorkflow starts a long-running workflow on the main puckdb-tasks queue
// and clears any stale ProgressReport from a previous run.
func (r *Resolver) executeWorkflow(ctx context.Context, workflowID string, workflow any, arg any) (bool, error) {
	return r.startWorkflow(ctx, workflowOptions(workflowID), workflow, arg, true)
}

// executeAdminWorkflow starts an admin workflow on the dedicated admin queue.
// Admin workflows (drop DB, migrate DB, flush Redis) are single-step operations
// that don't publish ProgressReports, so cleanup is skipped.
func (r *Resolver) executeAdminWorkflow(ctx context.Context, workflowID string, workflow any) (bool, error) {
	return r.startWorkflow(ctx, adminWorkflowOptions(workflowID), workflow, nil, false)
}

// executeAssetWorkflow starts a workflow on shared.TaskQueueAssets so the asset
// worker — not the main tasks worker — picks up the run, and clears stale
// progress like executeWorkflow.
func (r *Resolver) executeAssetWorkflow(ctx context.Context, workflowID string, workflow any, arg any) (bool, error) {
	return r.startWorkflow(ctx, assetWorkflowOptions(workflowID), workflow, arg, true)
}

// TODO move to temporal/worker

// buildWorkflowOptions is the shared core of workflowOptions /
// adminWorkflowOptions / assetWorkflowOptions below — they differ only in
// which task queue a workflow is dispatched to.
//
// WorkflowExecutionErrorWhenAlreadyStarted makes a start against a running
// workflow surface as serviceerror.WorkflowExecutionAlreadyStarted instead
// of silently returning the running execution; startWorkflow branches on it.
func buildWorkflowOptions(id, taskQueue string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                                       id,
		TaskQueue:                                taskQueue,
		WorkflowTaskTimeout:                      config.DefaultWorkflowTaskTimeout,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}
}

func workflowOptions(id string) client.StartWorkflowOptions {
	return buildWorkflowOptions(id, temporal.QueueTasks)
}

func adminWorkflowOptions(id string) client.StartWorkflowOptions {
	return buildWorkflowOptions(id, temporal.QueueAdmin)
}

func assetWorkflowOptions(id string) client.StartWorkflowOptions {
	return buildWorkflowOptions(id, shared.TaskQueueAssets)
}

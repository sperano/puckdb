package graph

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// ============================================================================
// startWorkflow's post-start progress cleanup must be run-aware: it may only
// remove a report from an EARLIER run. The worker can save the new run's
// first report before ExecuteWorkflow returns to the API, and a rejected
// start must never touch the running execution's report.
// ============================================================================

const (
	testProgressWorkflowID = "wf-progress-race"
	oldRunID               = "run-old"
	newRunID               = "run-new"
)

// stubWorkflowRun is the minimal client.WorkflowRun the resolver needs
// after a successful start: the run ID Temporal assigned.
type stubWorkflowRun struct {
	client.WorkflowRun
	runID string
}

func (s stubWorkflowRun) GetRunID() string { return s.runID }

func newProgressResolver(t *testing.T) (*Resolver, *MockTemporalClient, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	mc := &MockTemporalClient{}
	return &Resolver{TemporalClient: mc, RedisClient: rc}, mc, rc
}

// encodedReport is a distinguishable gob ProgressReport for a given run.
func encodedReport(t *testing.T, message string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(&shared.ProgressReport{Message: message, Total: 1}))
	return buf.Bytes()
}

func saveReport(t *testing.T, rc *redis.Client, runID, message string) {
	t.Helper()
	require.NoError(t, cache.SaveProgressReport(context.Background(), rc, testProgressWorkflowID, runID, encodedReport(t, message)))
}

// loadedMessage returns the Message of the report the resolver would serve,
// or "" when there is none.
func loadedMessage(t *testing.T, r *Resolver) string {
	t.Helper()
	report, err := r.queryProgressReport(context.Background(), testProgressWorkflowID)
	require.NoError(t, err)
	if report == nil || report.Message == nil {
		return ""
	}
	return *report.Message
}

// Interleaving: the previous run's report is in Redis; ExecuteWorkflow is
// accepted and — before it returns — the worker saves the new run's first
// report. The cleanup that follows must leave that fresh report alone.
func TestStartWorkflow_KeepsNewRunReportSavedBeforeStartReturns(t *testing.T) {
	t.Parallel()
	r, mc, rc := newProgressResolver(t)
	saveReport(t, rc, oldRunID, "stale from previous run")

	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			// The worker got there first: new run already reported.
			saveReport(t, rc, newRunID, "fresh from new run")
		}).
		Return(stubWorkflowRun{runID: newRunID}, nil)

	ok, err := r.startWorkflow(context.Background(), workflowOptions(testProgressWorkflowID), "wf", nil, true)
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, "fresh from new run", loadedMessage(t, r),
		"cleanup must not wipe a report the new run has already written")
	mc.AssertExpectations(t)
}

// Temporal rejects the start because the workflow is already running (the
// options ask for the error rather than the SDK's silent swallow). Nothing
// was started, so the caller gets an error, and the running execution's
// report is live and must survive untouched.
func TestStartWorkflow_RejectedAlreadyRunningKeepsLiveReport(t *testing.T) {
	t.Parallel()
	r, mc, rc := newProgressResolver(t)
	saveReport(t, rc, oldRunID, "live from running execution")

	mc.On("ExecuteWorkflow", mock.Anything,
		mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.WorkflowExecutionErrorWhenAlreadyStarted
		}),
		mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already running", "req-1", oldRunID))

	ok, err := r.startWorkflow(context.Background(), workflowOptions(testProgressWorkflowID), "wf", nil, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
	assert.Contains(t, err.Error(), testProgressWorkflowID)
	require.False(t, ok, "nothing was started")

	assert.Equal(t, "live from running execution", loadedMessage(t, r),
		"a rejected start must never remove the running execution's progress")
	mc.AssertExpectations(t)
}

// The already-started branch depends on the option being set for every
// workflow class; without it the SDK returns the running execution as if
// freshly started and the cleanup would run against it.
func TestWorkflowOptions_ErrorWhenAlreadyStarted(t *testing.T) {
	t.Parallel()
	for name, opts := range map[string]client.StartWorkflowOptions{
		"tasks": workflowOptions("wf"),
		"admin": adminWorkflowOptions("wf"),
		"asset": assetWorkflowOptions("wf"),
	} {
		assert.True(t, opts.WorkflowExecutionErrorWhenAlreadyStarted, name)
	}
}

// Accepted start, worker hasn't saved yet: the previous run's report must
// not be served for the new run.
func TestStartWorkflow_ClearsPreviousRunReport(t *testing.T) {
	t.Parallel()
	r, mc, rc := newProgressResolver(t)
	saveReport(t, rc, oldRunID, "stale from previous run")

	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(stubWorkflowRun{runID: newRunID}, nil)

	ok, err := r.startWorkflow(context.Background(), workflowOptions(testProgressWorkflowID), "wf", nil, true)
	require.NoError(t, err)
	require.True(t, ok)

	assert.Empty(t, loadedMessage(t, r), "stale prior-run report must be gone once the new run is accepted")
	mc.AssertExpectations(t)
}

// Admin workflows opt out of cleanup entirely (clearProgress=false).
func TestStartWorkflow_NoCleanupWhenDisabled(t *testing.T) {
	t.Parallel()
	r, mc, rc := newProgressResolver(t)
	saveReport(t, rc, oldRunID, "untouched")

	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(stubWorkflowRun{runID: newRunID}, nil)

	ok, err := r.startWorkflow(context.Background(), adminWorkflowOptions(testProgressWorkflowID), "wf", nil, false)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "untouched", loadedMessage(t, r))
}

// A cleanup failure after an accepted start is logged, not returned: the
// workflow IS running, and an error here would tell the caller otherwise.
func TestStartWorkflow_CleanupErrorDoesNotFailStart(t *testing.T) {
	t.Parallel()
	srv := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	mc := &MockTemporalClient{}
	r := &Resolver{TemporalClient: mc, RedisClient: rc}

	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { srv.SetError("redis down") }).
		Return(stubWorkflowRun{runID: newRunID}, nil)

	ok, err := r.startWorkflow(context.Background(), workflowOptions(testProgressWorkflowID), "wf", nil, true)
	require.NoError(t, err, "started workflow must be reported as started")
	require.True(t, ok)
}

// Sanity: the generic start error path (not "already started") is still
// surfaced unchanged and performs no cleanup.
func TestStartWorkflow_StartErrorPropagates(t *testing.T) {
	t.Parallel()
	r, mc, rc := newProgressResolver(t)
	saveReport(t, rc, oldRunID, "kept")
	boom := errors.New("temporal unavailable")

	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, boom)

	ok, err := r.startWorkflow(context.Background(), workflowOptions(testProgressWorkflowID), "wf", nil, true)
	require.ErrorIs(t, err, boom)
	require.False(t, ok)
	assert.Equal(t, "kept", loadedMessage(t, r))
}

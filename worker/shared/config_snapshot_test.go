package shared

// Replay-determinism tests for SnapshotConfig.
//
// The mechanism under test: a workflow that shapes its command sequence from a
// process-local setting (a viper flag, a YAML file) must resolve that setting
// once and record it in history. Otherwise a worker that restarts with new
// settings replays the history against a different command sequence and the
// execution is wedged with a nondeterminism error.
//
// TestSnapshotConfigReplaysUnderChangedSetting records two histories with the
// setting at 2, changes it to 5, and replays both: the snapshotting workflow
// must replay clean, and its live-reading twin must fail. The twin is the
// control — without it a passing test could just mean the replayer never
// noticed the difference.
//
// These tests need a Temporal server. They skip (never fail) when one is
// unavailable; PUCKDB_TEST_TEMPORAL_HOSTPORT points at an existing server
// instead of starting an in-process dev server.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	// envTestTemporalHostPort names an already-running Temporal server to use
	// instead of starting an in-process dev server.
	envTestTemporalHostPort = "PUCKDB_TEST_TEMPORAL_HOSTPORT"

	// devServerStartTimeout bounds the first-run download of the Temporal CLI
	// binary (~50MB) plus server startup.
	devServerStartTimeout = 90 * time.Second

	// workflowRunTimeout bounds waiting for a probe workflow to finish. The
	// probe activities are no-ops, so anything beyond this is a stuck worker.
	workflowRunTimeout = 60 * time.Second

	// probeActivityName is the no-op activity the probe workflows schedule,
	// one per unit of the configured setting.
	probeActivityName = "ProbeNoOp"

	// probeActivityTimeout is generous for a no-op; it only needs to exceed
	// dev-server task dispatch latency.
	probeActivityTimeout = 10 * time.Second

	// probeRecordedSetting is the setting in force while histories are
	// recorded; probeChangedSetting is what the process reads afterwards.
	probeRecordedSetting = 2
	probeChangedSetting  = 5

	snapshotProbeWorkflowName = "snapshotProbe"
	liveReadProbeWorkflowName = "liveReadProbe"
	valueOnlyProbeWorkflow    = "snapshotValueOnlyProbe"

	// Marker names the Go SDK records for GetVersion and SideEffect
	// (internal/internal_command_state_machine.go).
	versionMarkerName    = "Version"
	sideEffectMarkerName = "SideEffect"
)

// probeSetting stands in for a process-local configuration value (a viper flag
// or a parsed YAML file) that can change between a recording and a replay.
var probeSetting atomic.Int64

// probeLoadCalls counts loadProbeSetting invocations so a test can assert the
// loader runs exactly once per execution.
var probeLoadCalls atomic.Int64

func loadProbeSetting() int {
	probeLoadCalls.Add(1)
	return int(probeSetting.Load())
}

// snapshotProbeWorkflow resolves the setting through SnapshotConfig, so the
// value is recorded in history and reused on replay.
func snapshotProbeWorkflow(ctx workflow.Context) (int, error) {
	n, err := SnapshotConfig(ctx, loadProbeSetting)
	if err != nil {
		return 0, err
	}
	return n, runProbeActivities(ctx, n)
}

// liveReadProbeWorkflow is the pre-snapshot bug: it reads the setting live on
// every execution, including replays.
func liveReadProbeWorkflow(ctx workflow.Context) (int, error) {
	n := loadProbeSetting()
	return n, runProbeActivities(ctx, n)
}

// snapshotValueOnlyProbeWorkflow exercises SnapshotConfig without scheduling
// activities, for the plain test-environment assertions.
func snapshotValueOnlyProbeWorkflow(ctx workflow.Context) (int, error) {
	return SnapshotConfig(ctx, loadProbeSetting)
}

// runProbeActivities schedules n no-op activities up front and waits for them,
// so the setting is visible in history as a count of scheduled activities.
func runProbeActivities(ctx workflow.Context, n int) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: probeActivityTimeout,
	})
	futures := make([]workflow.Future, 0, n)
	for i := 0; i < n; i++ {
		futures = append(futures, workflow.ExecuteActivity(ctx, probeActivityName, i))
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func probeNoOpActivity(_ context.Context, _ int) error { return nil }

func TestSnapshotConfigReplaysUnderChangedSetting(t *testing.T) {
	c, stopServer := startTemporalForSnapshotTest(t)
	defer stopServer()

	taskQueue := uniqueName("snapshot-probe-tq")
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(snapshotProbeWorkflow, workflow.RegisterOptions{Name: snapshotProbeWorkflowName})
	w.RegisterWorkflowWithOptions(liveReadProbeWorkflow, workflow.RegisterOptions{Name: liveReadProbeWorkflowName})
	w.RegisterActivityWithOptions(probeNoOpActivity, activity.RegisterOptions{Name: probeActivityName})
	require.NoError(t, w.Start())
	defer w.Stop()

	probeSetting.Store(probeRecordedSetting)
	snapshotHist := runProbeWorkflow(t, c, taskQueue, snapshotProbeWorkflowName)
	liveHist := runProbeWorkflow(t, c, taskQueue, liveReadProbeWorkflowName)

	// The snapshot is recorded exactly once, behind exactly one version gate.
	requireMarkerCount(t, snapshotHist, versionMarkerName, 1)
	requireMarkerCount(t, snapshotHist, sideEffectMarkerName, 1)
	requireScheduledCount(t, snapshotHist, probeActivityName, probeRecordedSetting)
	requireScheduledCount(t, liveHist, probeActivityName, probeRecordedSetting)

	// A history recorded before snapshotting existed carries no version marker
	// — exactly the live twin's history. The snapshotting code must take the
	// DefaultVersion branch for it and reproduce the live read it was recorded
	// with; this is what keeps in-flight executions replayable across the deploy.
	requireMarkerCount(t, liveHist, versionMarkerName, 0)
	legacyReplayer := worker.NewWorkflowReplayer()
	legacyReplayer.RegisterWorkflowWithOptions(snapshotProbeWorkflow, workflow.RegisterOptions{Name: liveReadProbeWorkflowName})
	require.NoError(t, legacyReplayer.ReplayWorkflowHistory(nil, liveHist),
		"a pre-snapshot history must replay through the DefaultVersion fallback")

	// Everything below replays against the NEW setting.
	probeSetting.Store(probeChangedSetting)

	snapshotReplayer := worker.NewWorkflowReplayer()
	snapshotReplayer.RegisterWorkflowWithOptions(snapshotProbeWorkflow, workflow.RegisterOptions{Name: snapshotProbeWorkflowName})
	require.NoError(t, snapshotReplayer.ReplayWorkflowHistory(nil, snapshotHist),
		"a snapshotted setting must replay to the same command sequence after the setting changes")

	liveReplayer := worker.NewWorkflowReplayer()
	liveReplayer.RegisterWorkflowWithOptions(liveReadProbeWorkflow, workflow.RegisterOptions{Name: liveReadProbeWorkflowName})
	err := liveReplayer.ReplayWorkflowHistory(nil, liveHist)
	require.Error(t, err, "control: a live config read must break replay once the setting changes")
	require.Contains(t, strings.ToLower(err.Error()), "nondeterministic",
		"control failure should be a determinism mismatch, got: %v", err)
}

func TestSnapshotConfigLoadsOnce(t *testing.T) {
	probeSetting.Store(probeRecordedSetting)
	probeLoadCalls.Store(0)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(snapshotValueOnlyProbeWorkflow, workflow.RegisterOptions{Name: valueOnlyProbeWorkflow})

	env.ExecuteWorkflow(valueOnlyProbeWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got int
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, probeRecordedSetting, got, "SnapshotConfig must return the loaded value")
	require.EqualValues(t, 1, probeLoadCalls.Load(), "load must run exactly once per execution")
}

// startTemporalForSnapshotTest returns a Temporal client and a cleanup func.
// With PUCKDB_TEST_TEMPORAL_HOSTPORT set it dials that server; otherwise it
// starts an in-process dev server (downloading the Temporal CLI on first run).
// Either failure skips the test rather than failing it: a machine without a
// Temporal server has not opted into these tests.
func startTemporalForSnapshotTest(t *testing.T) (client.Client, func()) {
	t.Helper()
	if hp := os.Getenv(envTestTemporalHostPort); hp != "" {
		c, err := client.Dial(client.Options{HostPort: hp})
		if err != nil {
			t.Skipf("%s=%q dial failed: %v", envTestTemporalHostPort, hp, err)
		}
		return c, func() { c.Close() }
	}
	ctx, cancel := context.WithTimeout(context.Background(), devServerStartTimeout)
	defer cancel()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
	if err != nil {
		t.Skipf("DevServer start failed: %v (set %s to use an external Temporal)", err, envTestTemporalHostPort)
	}
	return srv.Client(), func() { _ = srv.Stop() }
}

// runProbeWorkflow runs a probe workflow to completion and returns its history.
func runProbeWorkflow(t *testing.T, c client.Client, taskQueue, workflowName string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workflowRunTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        uniqueName(workflowName),
		TaskQueue: taskQueue,
	}, workflowName)
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil), "probe workflow %s must complete", workflowName)

	return fetchHistory(t, c, run.GetID(), run.GetRunID())
}

// fetchHistory reads a completed execution's full history into a History proto,
// which is what WorkflowReplayer.ReplayWorkflowHistory consumes.
func fetchHistory(t *testing.T, c client.Client, workflowID, runID string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workflowRunTimeout)
	defer cancel()

	iter := c.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	hist := &historypb.History{}
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		hist.Events = append(hist.Events, event)
	}
	require.NotEmpty(t, hist.Events, "history for %s should not be empty", workflowID)
	return hist
}

func requireMarkerCount(t *testing.T, hist *historypb.History, markerName string, want int) {
	t.Helper()
	got := 0
	for _, e := range hist.Events {
		if e.GetEventType() == enumspb.EVENT_TYPE_MARKER_RECORDED &&
			e.GetMarkerRecordedEventAttributes().GetMarkerName() == markerName {
			got++
		}
	}
	require.Equal(t, want, got, "%s markers in history", markerName)
}

func requireScheduledCount(t *testing.T, hist *historypb.History, activityType string, want int) {
	t.Helper()
	require.Equal(t, want, countScheduled(hist, activityType), "%s activities scheduled", activityType)
}

func countScheduled(hist *historypb.History, activityType string) int {
	count := 0
	for _, e := range hist.Events {
		if e.GetEventType() == enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED &&
			e.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName() == activityType {
			count++
		}
	}
	return count
}

// uniqueName keeps workflow IDs and task queues from colliding across runs
// against a long-lived external Temporal server.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

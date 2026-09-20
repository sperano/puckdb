package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMonitorConfig shrinks the polling knobs so monitor loops run in
// milliseconds instead of the production seconds.
func testMonitorConfig() monitorConfig {
	return monitorConfig{
		startupDelay: 0,
		pollInterval: time.Millisecond,
		maxBackoff:   4 * time.Millisecond,
		maxFailures:  3,
	}
}

// monitorTestTimeout bounds every monitor-loop test. The loop-termination
// tests are regression tests for former infinite-poll bugs; without a
// deadline, a reintroduced bug would wedge the whole package until the go
// test timeout instead of failing the right test in seconds.
const monitorTestTimeout = 10 * time.Second

func monitorTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), monitorTestTimeout)
	t.Cleanup(cancel)
	return ctx
}

// newTestSpinner returns a started spinner writing to io.Discard. Starting it
// matters: Stop and Cancel block on the render goroutine's exit.
func newTestSpinner() *spinner {
	sp := newSpinner(io.Discard, "test")
	sp.Start()
	return sp
}

func statusOf(s model.TemporalWorkflowStatus) *WorkflowStatus {
	return &WorkflowStatus{Result: &model.WorkflowResult{Status: s}}
}

// TestClassifyWorkflowStatus_CoversEveryStatus checks the classification of
// every TemporalWorkflowStatus value. Iterating AllTemporalWorkflowStatus
// makes the test fail when a new status is added to the schema without an
// explicit classification decision here.
func TestClassifyWorkflowStatus_CoversEveryStatus(t *testing.T) {
	t.Parallel()

	type expectation struct {
		terminal bool
		errText  string // "" means no error expected
	}
	expectations := map[model.TemporalWorkflowStatus]expectation{
		model.TemporalWorkflowStatusUnspecified:    {terminal: false},
		model.TemporalWorkflowStatusRunning:        {terminal: false},
		model.TemporalWorkflowStatusContinuedAsNew: {terminal: false},
		model.TemporalWorkflowStatusCompleted:      {terminal: true},
		model.TemporalWorkflowStatusFailed:         {terminal: true, errText: "workflow failed"},
		model.TemporalWorkflowStatusCanceled:       {terminal: true, errText: "workflow was canceled"},
		model.TemporalWorkflowStatusTerminated:     {terminal: true, errText: "workflow was terminated"},
		model.TemporalWorkflowStatusTimedOut:       {terminal: true, errText: "workflow timed out"},
	}

	for _, status := range model.AllTemporalWorkflowStatus {
		exp, ok := expectations[status]
		require.Truef(t, ok,
			"no expectation for status %s — classify it in classifyWorkflowStatus and add it here", status)

		terminal, err := classifyWorkflowStatus(statusOf(status))
		assert.Equalf(t, exp.terminal, terminal, "status %s: terminal", status)
		if exp.errText == "" {
			assert.NoErrorf(t, err, "status %s", status)
		} else {
			assert.ErrorContainsf(t, err, exp.errText, "status %s", status)
		}
	}
}

// TestClassifyWorkflowStatus_FailedIncludesReason verifies the failure reason
// is carried into the error when the workflow reports one.
func TestClassifyWorkflowStatus_FailedIncludesReason(t *testing.T) {
	t.Parallel()

	reason := "activity exploded"
	status := statusOf(model.TemporalWorkflowStatusFailed)
	status.Result.FailureReason = &reason

	terminal, err := classifyWorkflowStatus(status)
	assert.True(t, terminal)
	assert.ErrorContains(t, err, "activity exploded")
}

// sequencedFetcher returns each status in order, repeating the last one once
// the sequence is exhausted. Calls happen on the monitor goroutine only, so
// plain fields are safe.
type sequencedFetcher struct {
	calls    int
	sequence []func() (*WorkflowStatus, error)
}

func (f *sequencedFetcher) fetch(_ context.Context) (*WorkflowStatus, error) {
	idx := f.calls
	if idx >= len(f.sequence) {
		idx = len(f.sequence) - 1
	}
	f.calls++
	return f.sequence[idx]()
}

func succeedWith(s model.TemporalWorkflowStatus) func() (*WorkflowStatus, error) {
	return func() (*WorkflowStatus, error) { return statusOf(s), nil }
}

func failWith(err error) func() (*WorkflowStatus, error) {
	return func() (*WorkflowStatus, error) { return nil, err }
}

// TestMonitorWorkflows_TerminalFailureStates verifies the parallel monitor
// returns an error for every unsuccessful terminal status — including
// TIMED_OUT and TERMINATED, which the pre-unification monitor left polling
// forever — and names the failing workflow.
func TestMonitorWorkflows_TerminalFailureStates(t *testing.T) {
	for _, status := range []model.TemporalWorkflowStatus{
		model.TemporalWorkflowStatusFailed,
		model.TemporalWorkflowStatusCanceled,
		model.TemporalWorkflowStatusTerminated,
		model.TemporalWorkflowStatusTimedOut,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			healthy := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
				succeedWith(model.TemporalWorkflowStatusRunning),
			}}
			doomed := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
				succeedWith(status),
			}}
			runners := []workflowRunner{
				{workflowType: workflowInitialize, getStatus: healthy.fetch},
				{workflowType: workflowFetchSeasons, getStatus: doomed.fetch},
			}

			err := testMonitorConfig().monitorWorkflows(monitorTestContext(t), newTestSpinner(), runners)

			require.Error(t, err)
			assert.ErrorContains(t, err, workflowFetchSeasons.String())
			_, wantErr := classifyWorkflowStatus(statusOf(status))
			assert.ErrorContains(t, err, wantErr.Error())
		})
	}
}

// TestMonitorWorkflows_AllComplete verifies the happy path: the monitor
// returns nil once every runner reports COMPLETED, tolerating runners that
// finish at different times.
func TestMonitorWorkflows_AllComplete(t *testing.T) {
	t.Parallel()

	fast := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		succeedWith(model.TemporalWorkflowStatusCompleted),
	}}
	slow := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		succeedWith(model.TemporalWorkflowStatusRunning),
		succeedWith(model.TemporalWorkflowStatusRunning),
		succeedWith(model.TemporalWorkflowStatusCompleted),
	}}
	runners := []workflowRunner{
		{workflowType: workflowInitialize, getStatus: fast.fetch},
		{workflowType: workflowYahooPlayers, getStatus: slow.fetch},
	}

	err := testMonitorConfig().monitorWorkflows(monitorTestContext(t), newTestSpinner(), runners)

	require.NoError(t, err)
	assert.Equal(t, 1, fast.calls, "completed runner must not be polled again")
	assert.Equal(t, 3, slow.calls)
}

// TestMonitorWorkflows_PerRunnerFailureLimit verifies one runner reaches
// maxFailures although queries to the other runner keep succeeding. With the
// pre-unification shared counter, the healthy runner's successes reset the
// count each iteration and this loop never terminated.
func TestMonitorWorkflows_PerRunnerFailureLimit(t *testing.T) {
	t.Parallel()

	apiDown := errors.New("api down")
	broken := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		failWith(apiDown),
	}}
	healthy := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		succeedWith(model.TemporalWorkflowStatusRunning),
	}}
	runners := []workflowRunner{
		{workflowType: workflowInitialize, getStatus: broken.fetch},
		{workflowType: workflowYahooPlayers, getStatus: healthy.fetch},
	}
	cfg := testMonitorConfig()

	err := cfg.monitorWorkflows(monitorTestContext(t), newTestSpinner(), runners)

	require.Error(t, err)
	assert.ErrorIs(t, err, apiDown)
	assert.ErrorContains(t, err, workflowInitialize.String())
	assert.ErrorContains(t, err, "3 times consecutively")
	assert.Equal(t, cfg.maxFailures, broken.calls)
	// The limit is hit before the healthy runner's poll in the final
	// iteration, so it sees one poll fewer — but it did keep succeeding
	// while the broken runner's counter climbed.
	assert.Equal(t, cfg.maxFailures-1, healthy.calls, "healthy runner keeps being polled while the other fails")
}

// TestMonitorWorkflows_SuccessResetsOnlyOwnCounter verifies a runner's own
// success resets its counter: a runner that fails intermittently — never
// maxFailures times in a row — must not abort the run.
func TestMonitorWorkflows_SuccessResetsOnlyOwnCounter(t *testing.T) {
	t.Parallel()

	flaky := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		failWith(errors.New("blip 1")),
		failWith(errors.New("blip 2")),
		succeedWith(model.TemporalWorkflowStatusRunning), // resets flaky's counter
		failWith(errors.New("blip 3")),
		failWith(errors.New("blip 4")),
		succeedWith(model.TemporalWorkflowStatusCompleted),
	}}
	steady := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		succeedWith(model.TemporalWorkflowStatusRunning),
		succeedWith(model.TemporalWorkflowStatusRunning),
		succeedWith(model.TemporalWorkflowStatusCompleted),
	}}
	runners := []workflowRunner{
		{workflowType: workflowInitialize, getStatus: flaky.fetch},
		{workflowType: workflowYahooPlayers, getStatus: steady.fetch},
	}

	// maxFailures is 3; flaky never fails 3 times consecutively.
	err := testMonitorConfig().monitorWorkflows(monitorTestContext(t), newTestSpinner(), runners)

	require.NoError(t, err)
	assert.Equal(t, len(flaky.sequence), flaky.calls)
}

// TestMonitorWorkflow_TerminalStates verifies the single-workflow monitor
// shares the classification: success on COMPLETED, error otherwise.
func TestMonitorWorkflow_TerminalStates(t *testing.T) {
	t.Parallel()

	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		fetcher := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
			succeedWith(model.TemporalWorkflowStatusRunning),
			succeedWith(model.TemporalWorkflowStatusCompleted),
		}}
		err := testMonitorConfig().monitorWorkflow(monitorTestContext(t), newTestSpinner(), fetcher.fetch, workflowInitialize)
		require.NoError(t, err)
		assert.Equal(t, 2, fetcher.calls)
	})

	t.Run("timed out", func(t *testing.T) {
		t.Parallel()
		fetcher := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
			succeedWith(model.TemporalWorkflowStatusTimedOut),
		}}
		err := testMonitorConfig().monitorWorkflow(monitorTestContext(t), newTestSpinner(), fetcher.fetch, workflowInitialize)
		assert.ErrorContains(t, err, "workflow timed out")
	})
}

// TestMonitorWorkflow_FailureLimit verifies the single-workflow monitor stops
// after maxFailures consecutive query errors.
func TestMonitorWorkflow_FailureLimit(t *testing.T) {
	t.Parallel()

	apiDown := errors.New("api down")
	fetcher := &sequencedFetcher{sequence: []func() (*WorkflowStatus, error){
		failWith(apiDown),
	}}
	cfg := testMonitorConfig()

	err := cfg.monitorWorkflow(monitorTestContext(t), newTestSpinner(), fetcher.fetch, workflowInitialize)

	require.Error(t, err)
	assert.ErrorIs(t, err, apiDown)
	assert.ErrorContains(t, err, "3 times consecutively")
	assert.Equal(t, cfg.maxFailures, fetcher.calls)
}

// TestMaxActiveFailures verifies the shared poll loop backs off on the worst
// still-running counter and ignores completed runners.
func TestMaxActiveFailures(t *testing.T) {
	t.Parallel()

	states := []runnerPollState{
		{consecutiveFailures: 1},
		{consecutiveFailures: 7, done: true}, // completed — must be ignored
		{consecutiveFailures: 4},
	}
	assert.Equal(t, 4, maxActiveFailures(states))
	assert.Equal(t, 0, maxActiveFailures(nil))
}

// TestCombineStatusMessages verifies the display-combination rules: messages
// join with newlines, the Yahoo token warning is appended at most once, and
// only fresh statuses of still-running workflows can contribute it —
// completed workflows keep cached statuses whose token info may be stale.
func TestCombineStatusMessages(t *testing.T) {
	t.Parallel()

	tokenMissing := func(done bool) runnerPollState {
		status := statusOf(model.TemporalWorkflowStatusRunning)
		status.YahooTokenMissing = true
		status.YahooLoginURL = "http://login"
		return runnerPollState{status: status, done: done}
	}
	warning := formatYahooTokenWarning("http://login")

	t.Run("joins messages without warning", func(t *testing.T) {
		t.Parallel()
		states := []runnerPollState{{status: statusOf(model.TemporalWorkflowStatusRunning)}}
		got := combineStatusMessages([]string{"line a", "line b"}, states)
		assert.Equal(t, "line a\nline b", got)
	})

	t.Run("fresh missing-token status appends warning once", func(t *testing.T) {
		t.Parallel()
		states := []runnerPollState{tokenMissing(false), tokenMissing(false)}
		got := combineStatusMessages([]string{"line a"}, states)
		assert.Equal(t, "line a\n"+warning, got)
		assert.Equal(t, 1, strings.Count(got, warning), "warning must appear once even when several statuses report it")
	})

	t.Run("stale status of completed workflow is ignored", func(t *testing.T) {
		t.Parallel()
		states := []runnerPollState{tokenMissing(true)}
		got := combineStatusMessages([]string{"line a"}, states)
		assert.NotContains(t, got, warning)
	})

	t.Run("nil status is skipped", func(t *testing.T) {
		t.Parallel()
		states := []runnerPollState{{}, tokenMissing(false)}
		got := combineStatusMessages([]string{"line a"}, states)
		assert.Contains(t, got, warning)
	})
}

// TestPollDelay verifies the exponential backoff on consecutive failures.
func TestPollDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		consecutiveFailures int
		want                time.Duration
	}{
		{"no failures returns base interval", 0, config.DefaultWorkflowPollInterval},
		{"first failure doubles interval", 1, config.DefaultWorkflowPollInterval * 2},
		{"second failure quadruples interval", 2, config.DefaultWorkflowPollInterval * 4},
		{"third failure 8x interval", 3, config.DefaultWorkflowPollInterval * 8},
		{"high failure count caps at max backoff", 10, config.MaxWorkflowPollBackoff},
		{"very high failure count still caps at max", 20, config.MaxWorkflowPollBackoff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := defaultMonitorConfig().pollDelay(tt.consecutiveFailures)
			if got != tt.want {
				t.Errorf("pollDelay(%d) = %v, want %v", tt.consecutiveFailures, got, tt.want)
			}
		})
	}
}

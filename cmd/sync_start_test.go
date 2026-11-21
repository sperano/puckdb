package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alreadyRunningError is the error the client returns for a start mutation
// the API refused because the workflow is already running.
func alreadyRunningError() error {
	return &graphQLError{
		Message:    "workflow fetch-seasons is already running (run r1)",
		Extensions: map[string]any{"code": graph.ErrorCodeWorkflowAlreadyRunning},
	}
}

// noWait is a start wait that must not be reached.
func noWait(t *testing.T) func(context.Context) error {
	return func(context.Context) error {
		t.Fatal("waited although the workflow was not already running")
		return nil
	}
}

// startRunner runs r.start in a fresh state, as run and runParallel do, and
// returns the state, the global log and its error.
func startRunner(t *testing.T, r workflowRunner, wait func(context.Context) error) (*syncState, *bytes.Buffer, error) {
	t.Helper()
	logs := captureLog(t)
	state := &syncState{}
	err := r.start(t.Context(), state, wait)
	return state, logs, err
}

// triggerResults returns a trigger answering results in order, and a
// pointer to how many times it was called.
func triggerResults(t *testing.T, results ...error) (func() (bool, error), *int) {
	calls := 0
	return func() (bool, error) {
		require.Less(t, calls, len(results), "trigger called more often than expected")
		err := results[calls]
		calls++
		return err == nil, err
	}, &calls
}

const alreadyRunningWarning = "Workflow already running, waiting for that run before starting this sync's (an interrupt leaves it running)"

func TestWorkflowRunnerStart_AlreadyRunningWaitsThenStartsItsOwnRun(t *testing.T) {
	trigger, calls := triggerResults(t, alreadyRunningError(), nil)
	var state *syncState
	var activeWhileWaiting []workflowType
	waits := 0
	wait := func(context.Context) error {
		waits++
		activeWhileWaiting = append([]workflowType{}, state.current...)
		return nil
	}
	logs := captureLog(t)
	state = &syncState{}

	err := workflowRunner{workflowType: workflowFetchSeasons, trigger: trigger}.start(t.Context(), state, wait)

	require.NoError(t, err)
	assert.Equal(t, 2, *calls, "this sync's run is started after the other one")
	assert.Equal(t, 1, waits)
	assert.Empty(t, activeWhileWaiting, "an interrupt must not cancel a run this sync did not start")
	assert.Equal(t, []workflowType{workflowFetchSeasons}, state.current, "this sync's own run is cancelled on interrupt")
	assert.Equal(t, []map[string]any{{
		"level": "warn", "workflow": workflowFetchSeasons.String(), "message": alreadyRunningWarning,
	}}, logLines(t, logs))
}

func TestWorkflowRunnerStart_StillAlreadyRunningAfterTheWaitsFails(t *testing.T) {
	results := make([]error, config.MaxAlreadyRunningWaits+1)
	for i := range results {
		results[i] = alreadyRunningError()
	}
	trigger, calls := triggerResults(t, results...)
	waits := 0

	state, _, err := startRunner(t, workflowRunner{workflowType: workflowFetchSeasons, trigger: trigger},
		func(context.Context) error { waits++; return nil })

	require.ErrorIs(t, err, errStillAlreadyRunning)
	assert.Equal(t, config.MaxAlreadyRunningWaits+1, *calls)
	assert.Equal(t, config.MaxAlreadyRunningWaits, waits)
	assert.Empty(t, state.current)
}

func TestWorkflowRunnerStart_InterruptedWaitFails(t *testing.T) {
	trigger, calls := triggerResults(t, alreadyRunningError())

	state, _, err := startRunner(t, workflowRunner{workflowType: workflowFetchSeasons, trigger: trigger},
		func(context.Context) error { return context.Canceled })

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, *calls)
	assert.Empty(t, state.current)
}

func TestWorkflowRunnerStart_OtherErrorFails(t *testing.T) {
	triggerErr := &graphQLError{Message: "boom", Extensions: map[string]any{"code": "OTHER"}}
	state, _, err := startRunner(t, workflowRunner{
		workflowType: workflowFetchSeasons,
		trigger:      func() (bool, error) { return false, triggerErr },
	}, noWait(t))

	require.ErrorIs(t, err, triggerErr)
	assert.Equal(t, []workflowType{workflowFetchSeasons}, state.current)
}

func TestWorkflowRunnerStart_NotStartedWarns(t *testing.T) {
	state, logs, err := startRunner(t, workflowRunner{
		workflowType: workflowImportSeasons,
		trigger:      func() (bool, error) { return false, nil },
	}, noWait(t))

	require.NoError(t, err)
	assert.Equal(t, []workflowType{workflowImportSeasons}, state.current)
	assert.Equal(t, []map[string]any{{
		"level": "warn", "workflow": workflowImportSeasons.String(), "message": "Workflow was not started",
	}}, logLines(t, logs))
}

func TestWorkflowRunnerStart_StartedIsSilent(t *testing.T) {
	state, logs, err := startRunner(t, workflowRunner{
		workflowType: workflowImportSeasons,
		trigger:      func() (bool, error) { return true, nil },
	}, noWait(t))

	require.NoError(t, err)
	assert.Equal(t, []workflowType{workflowImportSeasons}, state.current)
	assert.Empty(t, logs.String())
}

// TestStartRunners_WaitingRunnerLeavesUnstartedSiblingsInactive is the
// regression test for runParallel marking every runner active before
// starting any: an interrupt while the first waited cancelled the second's
// workflow, which this sync had not started.
func TestStartRunners_WaitingRunnerLeavesUnstartedSiblingsInactive(t *testing.T) {
	captureLog(t)
	first, _ := triggerResults(t, alreadyRunningError(), nil)
	second, secondCalls := triggerResults(t, nil)
	runners := []workflowRunner{
		{workflowType: workflowFetchSeasons, trigger: first},
		{workflowType: workflowImportSeasons, trigger: second},
	}
	state := &syncState{}
	var activeWhileWaiting []workflowType
	waits := 0
	waitFor := func(r workflowRunner) func(context.Context) error {
		return func(context.Context) error {
			require.Equal(t, workflowFetchSeasons, r.workflowType, "only the refused runner waits")
			waits++
			activeWhileWaiting = append([]workflowType{}, state.current...)
			return nil
		}
	}

	require.NoError(t, startRunners(t.Context(), state, runners, waitFor))

	assert.Equal(t, 1, waits)
	assert.Empty(t, activeWhileWaiting, "an interrupt while waiting must cancel no workflow")
	assert.Equal(t, 1, *secondCalls)
	assert.Equal(t, []workflowType{workflowFetchSeasons, workflowImportSeasons}, state.current)
}

func TestStartRunners_StopsAtTheFirstFailure(t *testing.T) {
	triggerErr := &graphQLError{Message: "boom", Extensions: map[string]any{"code": "OTHER"}}
	runners := []workflowRunner{
		{workflowType: workflowFetchSeasons, trigger: func() (bool, error) { return false, triggerErr }},
		{workflowType: workflowImportSeasons, trigger: func() (bool, error) {
			t.Fatal("started a runner after an earlier one failed")
			return false, nil
		}},
	}
	state := &syncState{}

	require.ErrorIs(t, startRunners(t.Context(), state, runners,
		func(workflowRunner) func(context.Context) error { return noWait(t) }), triggerErr)
	assert.Equal(t, []workflowType{workflowFetchSeasons}, state.current)
}

func TestWorkflowRunnerWaitForRun_OtherRunsFailureIsNotThisSyncs(t *testing.T) {
	logs := captureLog(t)
	r := workflowRunner{
		workflowType: workflowFetchSeasons,
		getStatus: func(context.Context) (*WorkflowStatus, error) {
			return statusOf(model.TemporalWorkflowStatusFailed), nil
		},
	}

	require.NoError(t, r.waitForRun(monitorTestContext(t), io.Discard, testMonitorConfig()))

	assert.Equal(t, []map[string]any{{
		"level": "warn", "workflow": workflowFetchSeasons.String(), "error": "workflow failed",
		"message": "The run waited for did not succeed; starting this sync's run",
	}}, logLines(t, logs))
}

func TestWorkflowRunnerWaitForRun_CompletedIsSilent(t *testing.T) {
	logs := captureLog(t)
	r := workflowRunner{
		workflowType: workflowFetchSeasons,
		getStatus: func(context.Context) (*WorkflowStatus, error) {
			return statusOf(model.TemporalWorkflowStatusCompleted), nil
		},
	}

	require.NoError(t, r.waitForRun(monitorTestContext(t), io.Discard, testMonitorConfig()))
	assert.Empty(t, logs.String())
}

func TestWorkflowRunnerWaitForRun_InterruptFails(t *testing.T) {
	ctx, cancel := context.WithCancel(monitorTestContext(t))
	r := workflowRunner{
		workflowType: workflowFetchSeasons,
		getStatus: func(context.Context) (*WorkflowStatus, error) {
			cancel()
			return statusOf(model.TemporalWorkflowStatusRunning), nil
		},
	}

	require.ErrorIs(t, r.waitForRun(ctx, io.Discard, testMonitorConfig()), context.Canceled)
}

// TestIsWorkflowAlreadyRunning_FromTheWire checks the code survives the
// client's decoding of a GraphQL error response.
func TestIsWorkflowAlreadyRunning_FromTheWire(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"already running": {`{"errors":[{"message":"workflow fetch-seasons is already running","extensions":{"code":"` +
			graph.ErrorCodeWorkflowAlreadyRunning + `"}}],"data":null}`, true},
		"other code": {`{"errors":[{"message":"bad input","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`, false},
		"no code":    {`{"errors":[{"message":"boom"}],"data":null}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := NewGraphQLClient(srv.URL).FetchSeasons(t.Context(), &model.SeasonsInput{})

			require.Error(t, err)
			assert.Equal(t, tc.want, isWorkflowAlreadyRunning(err))
		})
	}
}

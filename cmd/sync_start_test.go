package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

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

// startRunner runs r.start on a plain spinner with r active in a fresh
// state, as run and runParallel do, and returns the state, the global log
// and its error.
func startRunner(t *testing.T, r workflowRunner) (*syncState, *bytes.Buffer, error) {
	t.Helper()
	logs := captureLog(t)
	var out bytes.Buffer
	sp := newSpinner(&out, "Starting...")
	sp.SetPlain(plainTestEvery)
	state := &syncState{}
	state.setActive(r.workflowType)
	err := r.start(sp, state)
	return state, logs, err
}

func TestWorkflowRunnerStart_AlreadyRunningWaitsWithoutOwningTheRun(t *testing.T) {
	state, logs, err := startRunner(t, workflowRunner{
		workflowType: workflowFetchSeasons,
		trigger:      func() (bool, error) { return false, alreadyRunningError() },
	})

	require.NoError(t, err)
	assert.Empty(t, state.current, "an interrupt must not cancel a run this sync did not start")
	assert.Equal(t, []map[string]any{{
		"level": "warn", "workflow": workflowFetchSeasons.String(),
		"message": "Workflow already running, waiting for that run (an interrupt leaves it running)",
	}}, logLines(t, logs))
}

func TestWorkflowRunnerStart_OtherErrorFails(t *testing.T) {
	triggerErr := &graphQLError{Message: "boom", Extensions: map[string]any{"code": "OTHER"}}
	state, _, err := startRunner(t, workflowRunner{
		workflowType: workflowFetchSeasons,
		trigger:      func() (bool, error) { return false, triggerErr },
	})

	require.ErrorIs(t, err, triggerErr)
	assert.Equal(t, []workflowType{workflowFetchSeasons}, state.current)
}

func TestWorkflowRunnerStart_NotStartedWarns(t *testing.T) {
	state, logs, err := startRunner(t, workflowRunner{
		workflowType: workflowImportSeasons,
		trigger:      func() (bool, error) { return false, nil },
	})

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
	})

	require.NoError(t, err)
	assert.Equal(t, []workflowType{workflowImportSeasons}, state.current)
	assert.Empty(t, logs.String())
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

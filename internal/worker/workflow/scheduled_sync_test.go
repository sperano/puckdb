package workflow

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	// scheduledSyncSeason is the season scheduledSyncNow resolves to.
	scheduledSyncSeason = 2026
)

var (
	scheduledSyncNow = time.Date(2026, time.November, 20, 11, 0, 0, 0, time.UTC)
	// scheduledSyncSpringNow is in March 2027, still season 2026.
	scheduledSyncSpringNow = time.Date(2027, time.March, 3, 11, 0, 0, 0, time.UTC)

	errScheduledFetch  = errors.New("fetch failed")
	errScheduledImport = errors.New("import failed")
)

// syncStepRecorder records, in order, the workflow ID each mocked step ran
// under and the season input it received.
type syncStepRecorder struct {
	ids    []string
	inputs map[string]*model.SeasonsInput
}

func (r *syncStepRecorder) record(ctx workflow.Context, input *model.SeasonsInput) {
	id := workflow.GetInfo(ctx).WorkflowExecution.ID
	r.ids = append(r.ids, id)
	if r.inputs == nil {
		r.inputs = map[string]*model.SeasonsInput{}
	}
	r.inputs[id] = input
}

func newScheduledSyncEnv(t *testing.T, now time.Time) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(now)
	env.RegisterWorkflow(ScheduledSeasonsSyncWorkflow)
	env.RegisterWorkflow(ScheduledPlayersSyncWorkflow)
	for _, step := range append(playersSyncSteps(nil),
		syncStep{workflow: FetchSeasonsWorkflow}, syncStep{workflow: ImportSeasonsWorkflow}) {
		env.RegisterWorkflow(step.workflow)
	}
	return env
}

// mockSeasonsStep mocks a seasons step returning err.
func mockSeasonsStep(env *testsuite.TestWorkflowEnvironment, rec *syncStepRecorder, fn any, err error) {
	env.OnWorkflow(fn, mock.Anything, mock.Anything).Return(func(ctx workflow.Context, input *model.SeasonsInput) error {
		rec.record(ctx, input)
		return err
	}).Maybe()
}

func alreadyRunning() error {
	return &temporal.ChildWorkflowExecutionAlreadyStartedError{}
}

func scheduledSyncResult(t *testing.T, env *testsuite.TestWorkflowEnvironment) ScheduledSyncResult {
	t.Helper()
	var result ScheduledSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	return result
}

func seasonInputOf(season int) *model.SeasonsInput {
	return &model.SeasonsInput{StartSeason: &season, EndSeason: &season}
}

func TestScheduledSeasonsSyncRunsFetchThenImportForCurrentSeason(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, nil)
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, nil)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, []string{shared.WorkflowIDFetchSeasons, shared.WorkflowIDImportSeasons}, rec.ids,
		"each step runs under the ID a manual sync uses")
	assert.Equal(t, seasonInputOf(scheduledSyncSeason), rec.inputs[shared.WorkflowIDFetchSeasons])
	assert.Equal(t, seasonInputOf(scheduledSyncSeason), rec.inputs[shared.WorkflowIDImportSeasons])
	assert.Equal(t, ScheduledSyncResult{Season: scheduledSyncSeason,
		Ran: []string{shared.WorkflowIDFetchSeasons, shared.WorkflowIDImportSeasons}}, scheduledSyncResult(t, env))
}

func TestScheduledSeasonsSyncBeforeRolloverKeepsPriorSeason(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncSpringNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, nil)
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, nil)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, seasonInputOf(scheduledSyncSeason), rec.inputs[shared.WorkflowIDFetchSeasons])
}

func TestScheduledSeasonsSyncSkipsWhileFetchRuns(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, alreadyRunning())
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, nil)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, []string{shared.WorkflowIDFetchSeasons}, rec.ids, "the import is left to the running sync")
	assert.Equal(t, ScheduledSyncResult{Season: scheduledSyncSeason, SkippedRunning: shared.WorkflowIDFetchSeasons},
		scheduledSyncResult(t, env))
}

func TestScheduledSeasonsSyncSkipsImportWhileItRuns(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, nil)
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, alreadyRunning())

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, ScheduledSyncResult{Season: scheduledSyncSeason, Ran: []string{shared.WorkflowIDFetchSeasons},
		SkippedRunning: shared.WorkflowIDImportSeasons}, scheduledSyncResult(t, env))
}

func TestScheduledSeasonsSyncImportsAfterFailedFetch(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, errScheduledFetch)
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, nil)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	assert.Equal(t, []string{shared.WorkflowIDFetchSeasons, shared.WorkflowIDImportSeasons}, rec.ids)
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), errScheduledFetch.Error())
	assert.NotContains(t, err.Error(), errScheduledImport.Error())
}

func TestScheduledSeasonsSyncReportsBothFailures(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, errScheduledFetch)
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, errScheduledImport)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), errScheduledFetch.Error())
	assert.Contains(t, err.Error(), errScheduledImport.Error())
}

func TestScheduledSeasonsSyncCancelledFetchSkipsImport(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockSeasonsStep(env, rec, FetchSeasonsWorkflow, temporal.NewCanceledError())
	mockSeasonsStep(env, rec, ImportSeasonsWorkflow, nil)

	env.ExecuteWorkflow(ScheduledSeasonsSyncWorkflow)

	assert.Equal(t, []string{shared.WorkflowIDFetchSeasons}, rec.ids, "a cancelled fetch must not start the import")
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.True(t, temporal.IsCanceledError(err), "got %v", err)
}

// mockPlayersSteps mocks every players step: the step with ID failID
// returns failErr, the others succeed.
func mockPlayersSteps(env *testsuite.TestWorkflowEnvironment, rec *syncStepRecorder, failID string, failErr error) {
	result := func(ctx workflow.Context) error {
		if workflow.GetInfo(ctx).WorkflowExecution.ID == failID {
			return failErr
		}
		return nil
	}
	env.OnWorkflow(FetchYahooPlayersWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, _ *FetchYahooPlayersInput) error { rec.record(ctx, nil); return result(ctx) }).Maybe()
	env.OnWorkflow(FetchPlayerLandingsWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, _ *FetchPlayerLandingsInput) (*FetchPlayerLandingsResult, error) {
			rec.record(ctx, nil)
			return &FetchPlayerLandingsResult{}, result(ctx)
		}).Maybe()
	env.OnWorkflow(ProcessPlayersWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, _ *ProcessPlayersInput) (*ProcessPlayersResult, error) {
			rec.record(ctx, nil)
			return &ProcessPlayersResult{}, result(ctx)
		}).Maybe()
	for _, fn := range []any{ExtractBoxscorePlayersWorkflow, FetchPlayerLogsWorkflow, ImportPlayerLogsWorkflow} {
		env.OnWorkflow(fn, mock.Anything, mock.Anything).Return(
			func(ctx workflow.Context, input *model.SeasonsInput) error {
				rec.record(ctx, input)
				return result(ctx)
			}).Maybe()
	}
}

var playersStepIDs = []string{
	WorkflowIDFetchYahooPlayers, WorkflowIDExtractBoxscorePlayers, WorkflowIDFetchPlayerLandings,
	shared.WorkflowIDFetchPlayerLogs, WorkflowIDProcessPlayers, shared.WorkflowIDImportPlayerLogs,
}

func TestScheduledPlayersSyncRunsEveryStepInOrder(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockPlayersSteps(env, rec, "", nil)

	env.ExecuteWorkflow(ScheduledPlayersSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, playersStepIDs, rec.ids)
	for _, id := range []string{WorkflowIDExtractBoxscorePlayers, shared.WorkflowIDFetchPlayerLogs, shared.WorkflowIDImportPlayerLogs} {
		assert.Equal(t, seasonInputOf(scheduledSyncSeason), rec.inputs[id], id)
	}
	assert.Equal(t, ScheduledSyncResult{Season: scheduledSyncSeason, Ran: playersStepIDs}, scheduledSyncResult(t, env))
}

func TestScheduledPlayersSyncStopsAtFirstFailure(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockPlayersSteps(env, rec, WorkflowIDFetchPlayerLandings, errScheduledFetch)

	env.ExecuteWorkflow(ScheduledPlayersSyncWorkflow)

	assert.Equal(t, playersStepIDs[:3], rec.ids)
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), errScheduledFetch.Error())
}

func TestScheduledPlayersSyncSkipsWhileAStepRuns(t *testing.T) {
	env := newScheduledSyncEnv(t, scheduledSyncNow)
	rec := &syncStepRecorder{}
	mockPlayersSteps(env, rec, WorkflowIDProcessPlayers, alreadyRunning())

	env.ExecuteWorkflow(ScheduledPlayersSyncWorkflow)

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, playersStepIDs[:5], rec.ids, "no step starts after the running one")
	assert.Equal(t, ScheduledSyncResult{Season: scheduledSyncSeason, Ran: playersStepIDs[:4],
		SkippedRunning: WorkflowIDProcessPlayers}, scheduledSyncResult(t, env))
}

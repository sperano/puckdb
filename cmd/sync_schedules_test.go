package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	puckdbtemporal "github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

const (
	syncTestSeasonsCron = "0 6 * * *"
	syncTestPlayersCron = "0 8 * * 1"
	syncTestTimeZone    = "America/Toronto"
)

// scheduleRecorder is a schedule client holding one fake handle per
// schedule ID; createErr makes every Create fail with it.
type scheduleRecorder struct {
	client.ScheduleClient
	handles   map[string]*fakeScheduleHandle
	creates   []client.ScheduleOptions
	createErr error
}

func newScheduleRecorder() *scheduleRecorder {
	return &scheduleRecorder{handles: map[string]*fakeScheduleHandle{}}
}

func (r *scheduleRecorder) handle(id string) *fakeScheduleHandle {
	if r.handles[id] == nil {
		r.handles[id] = &fakeScheduleHandle{}
	}
	return r.handles[id]
}

func (r *scheduleRecorder) GetHandle(_ context.Context, id string) client.ScheduleHandle {
	return r.handle(id)
}

func (r *scheduleRecorder) Create(_ context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	r.creates = append(r.creates, options)
	if r.createErr != nil {
		return nil, r.createErr
	}
	return r.handle(options.ID), nil
}

func bothSyncSchedules() syncSchedulesConfig {
	return syncSchedulesConfig{Seasons: syncTestSeasonsCron, Players: syncTestPlayersCron, TimeZone: syncTestTimeZone}
}

func TestEnsureSyncSchedulesCreatesBoth(t *testing.T) {
	r := newScheduleRecorder()
	require.NoError(t, ensureSyncSchedules(t.Context(), r, bothSyncSchedules()))

	require.Len(t, r.creates, 2)
	for i, want := range []struct{ scheduleID, cron, workflowID string }{
		{shared.ScheduleIDSeasonsSync, syncTestSeasonsCron, shared.WorkflowIDScheduledSeasonsSync},
		{shared.ScheduleIDPlayersSync, syncTestPlayersCron, shared.WorkflowIDScheduledPlayersSync},
	} {
		got := r.creates[i]
		assert.Equal(t, want.scheduleID, got.ID)
		assert.Equal(t, client.ScheduleSpec{CronExpressions: []string{want.cron}, TimeZoneName: syncTestTimeZone}, got.Spec)
		assert.Equal(t, enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, got.Overlap)
		assert.Equal(t, 12*time.Hour, got.CatchupWindow)
		action, ok := got.Action.(*client.ScheduleWorkflowAction)
		require.True(t, ok)
		assert.Equal(t, want.workflowID, action.ID)
		assert.Equal(t, puckdbtemporal.QueueTasks, action.TaskQueue)
		assert.Empty(t, action.Args, "the workflow picks the current season at each run")
	}
	assert.Equal(t, "ScheduledSeasonsSyncWorkflow", workflowName(t, r.creates[0].Action))
	assert.Equal(t, "ScheduledPlayersSyncWorkflow", workflowName(t, r.creates[1].Action))
}

// workflowName is the Temporal name of a schedule action's workflow (a
// workflow function is named like an activity function).
func workflowName(t *testing.T, action client.ScheduleAction) string {
	t.Helper()
	workflowAction, ok := action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	return temporalActivityName(workflowAction.Workflow)
}

func TestEnsureSyncSchedulesEmptyRemovesBoth(t *testing.T) {
	r := newScheduleRecorder()
	// The time zone is not checked when no schedule uses it.
	require.NoError(t, ensureSyncSchedules(t.Context(), r, syncSchedulesConfig{TimeZone: "Not/AZone"}))

	assert.Empty(t, r.creates)
	assert.True(t, r.handle(shared.ScheduleIDSeasonsSync).deleteCalled)
	assert.True(t, r.handle(shared.ScheduleIDPlayersSync).deleteCalled)
}

func TestEnsureSyncSchedulesOnlySeasons(t *testing.T) {
	r := newScheduleRecorder()
	cfg := bothSyncSchedules()
	cfg.Players = ""
	require.NoError(t, ensureSyncSchedules(t.Context(), r, cfg))

	require.Len(t, r.creates, 1)
	assert.Equal(t, shared.ScheduleIDSeasonsSync, r.creates[0].ID)
	assert.False(t, r.handle(shared.ScheduleIDSeasonsSync).deleteCalled)
	assert.True(t, r.handle(shared.ScheduleIDPlayersSync).deleteCalled)
}

func TestEnsureSyncSchedulesRejectsUnknownTimeZone(t *testing.T) {
	r := newScheduleRecorder()
	cfg := bothSyncSchedules()
	cfg.TimeZone = "Not/AZone"
	err := ensureSyncSchedules(t.Context(), r, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `sync schedule time zone "Not/AZone"`)
	assert.Empty(t, r.creates)
}

func TestEnsureSyncSchedulesErrorsNameTheSchedule(t *testing.T) {
	r := newScheduleRecorder()
	r.createErr = assert.AnError
	err := ensureSyncSchedules(t.Context(), r, bothSyncSchedules())
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, "set seasons sync schedule: "+assert.AnError.Error(), err.Error())

	r = newScheduleRecorder()
	r.handle(shared.ScheduleIDPlayersSync).deleteErr = assert.AnError
	cfg := bothSyncSchedules()
	cfg.Players = ""
	err = ensureSyncSchedules(t.Context(), r, cfg)
	assert.Equal(t, "remove players sync schedule: "+assert.AnError.Error(), err.Error())
}

func TestEnsureSyncSchedulesUpdatesExistingSchedule(t *testing.T) {
	r := newScheduleRecorder()
	r.createErr = temporal.ErrScheduleAlreadyRunning
	require.NoError(t, ensureSyncSchedules(t.Context(), r, bothSyncSchedules()))

	handle := r.handle(shared.ScheduleIDSeasonsSync)
	require.True(t, handle.updateCalled)
	existing := client.ScheduleUpdateInput{Description: client.ScheduleDescription{Schedule: client.Schedule{
		Spec:   &client.ScheduleSpec{CronExpressions: []string{"0 4 * * *"}},
		Action: &client.ScheduleWorkflowAction{ID: "old"},
		Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL, CatchupWindow: time.Minute, PauseOnFailure: true},
	}}}
	update, err := handle.updateOptions.DoUpdate(existing)

	require.NoError(t, err)
	assert.Equal(t, &client.ScheduleSpec{CronExpressions: []string{syncTestSeasonsCron}, TimeZoneName: syncTestTimeZone}, update.Schedule.Spec)
	action, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	assert.Equal(t, shared.WorkflowIDScheduledSeasonsSync, action.ID)
	assert.Equal(t, &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: 12 * time.Hour,
		PauseOnFailure: true}, update.Schedule.Policy, "overlap and catch-up are reset, other policies kept")
}

func TestUpdatedPolicyZeroCatchupKeepsExisting(t *testing.T) {
	got := updatedPolicy(&client.SchedulePolicies{CatchupWindow: time.Hour}, 0)
	assert.Equal(t, &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: time.Hour}, got)
	assert.Equal(t, &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP}, updatedPolicy(nil, 0))
}

// captureLog sends the global logger to a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = previous })
	return &buf
}

// logLines decodes each JSON log line in buf.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		lines = append(lines, entry)
	}
	return lines
}

func TestEnsureSyncSchedulesLogsCronAndRemoval(t *testing.T) {
	buf := captureLog(t)
	cfg := bothSyncSchedules()
	cfg.Players = ""
	require.NoError(t, ensureSyncSchedules(t.Context(), newScheduleRecorder(), cfg))

	lines := logLines(t, buf)
	require.Len(t, lines, 2)
	assert.Equal(t, map[string]any{
		"level": "info", "message": "Schedule set", "schedule": "seasons sync",
		"schedule_id": shared.ScheduleIDSeasonsSync, "cron": []any{syncTestSeasonsCron}, "time_zone": syncTestTimeZone,
	}, lines[0])
	assert.Equal(t, map[string]any{
		"level": "info", "message": "Schedule removed", "schedule": "players sync",
		"schedule_id": shared.ScheduleIDPlayersSync,
	}, lines[1])
}

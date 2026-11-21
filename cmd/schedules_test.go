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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

const (
	testScheduleID       = "test-schedule"
	testScheduleName     = "test"
	testScheduleCron     = "0 6 * * *"
	testScheduleTimeZone = "America/Toronto"
	testScheduleAction   = "test-workflow"
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

// cronSchedule is a schedule set to testScheduleCron.
func cronSchedule() scheduleSpec {
	return scheduleSpec{
		Name:   testScheduleName,
		ID:     testScheduleID,
		Spec:   &client.ScheduleSpec{CronExpressions: []string{testScheduleCron}, TimeZoneName: testScheduleTimeZone},
		Action: &client.ScheduleWorkflowAction{ID: testScheduleAction, TaskQueue: puckdbtemporal.QueueTasks},
	}
}

// removedSchedule is cronSchedule with no spec: its removal.
func removedSchedule() scheduleSpec {
	s := cronSchedule()
	s.Spec = nil
	return s
}

func TestEnsureScheduleCreates(t *testing.T) {
	r := newScheduleRecorder()
	require.NoError(t, ensureSchedule(t.Context(), r, cronSchedule()))

	require.Len(t, r.creates, 1)
	got := r.creates[0]
	assert.Equal(t, testScheduleID, got.ID)
	assert.Equal(t, client.ScheduleSpec{CronExpressions: []string{testScheduleCron}, TimeZoneName: testScheduleTimeZone}, got.Spec)
	assert.Equal(t, enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, got.Overlap)
	assert.Equal(t, cronSchedule().Action, got.Action)
	assert.False(t, r.handle(testScheduleID).deleteCalled)
}

func TestEnsureScheduleNilSpecRemoves(t *testing.T) {
	r := newScheduleRecorder()
	require.NoError(t, ensureSchedule(t.Context(), r, removedSchedule()))

	assert.Empty(t, r.creates)
	assert.True(t, r.handle(testScheduleID).deleteCalled)
}

func TestEnsureScheduleErrorsNameTheSchedule(t *testing.T) {
	r := newScheduleRecorder()
	r.createErr = assert.AnError
	err := ensureSchedule(t.Context(), r, cronSchedule())
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, "set test schedule: "+assert.AnError.Error(), err.Error())

	r = newScheduleRecorder()
	r.handle(testScheduleID).deleteErr = assert.AnError
	err = ensureSchedule(t.Context(), r, removedSchedule())
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, "remove test schedule: "+assert.AnError.Error(), err.Error())

	r = newScheduleRecorder()
	r.createErr = temporal.ErrScheduleAlreadyRunning
	r.handle(testScheduleID).updateErr = assert.AnError
	err = ensureSchedule(t.Context(), r, cronSchedule())
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, "set test schedule: "+assert.AnError.Error(), err.Error())
}

func TestEnsureScheduleUpdatesExistingSchedule(t *testing.T) {
	r := newScheduleRecorder()
	r.createErr = temporal.ErrScheduleAlreadyRunning
	require.NoError(t, ensureSchedule(t.Context(), r, cronSchedule()))

	handle := r.handle(testScheduleID)
	require.True(t, handle.updateCalled)
	existing := client.ScheduleUpdateInput{Description: client.ScheduleDescription{Schedule: client.Schedule{
		Spec:   &client.ScheduleSpec{CronExpressions: []string{"0 4 * * *"}},
		Action: &client.ScheduleWorkflowAction{ID: "old"},
		Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL, CatchupWindow: time.Minute, PauseOnFailure: true},
	}}}
	update, err := handle.updateOptions.DoUpdate(existing)

	require.NoError(t, err)
	assert.Equal(t, cronSchedule().Spec, update.Schedule.Spec)
	assert.Equal(t, cronSchedule().Action, update.Schedule.Action)
	assert.Equal(t, &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: time.Minute,
		PauseOnFailure: true}, update.Schedule.Policy, "overlap is reset, other policies kept")
}

func TestUpdatedPolicyWithoutExistingPolicy(t *testing.T) {
	assert.Equal(t, &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP}, updatedPolicy(nil))
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

func TestEnsureScheduleLogsCronAndRemoval(t *testing.T) {
	buf := captureLog(t)
	r := newScheduleRecorder()
	require.NoError(t, ensureSchedule(t.Context(), r, cronSchedule()))
	require.NoError(t, ensureSchedule(t.Context(), r, removedSchedule()))

	lines := logLines(t, buf)
	require.Len(t, lines, 2)
	assert.Equal(t, map[string]any{
		"level": "info", "message": "Schedule set", "schedule": testScheduleName,
		"schedule_id": testScheduleID, "cron": []any{testScheduleCron}, "time_zone": testScheduleTimeZone,
	}, lines[0])
	assert.Equal(t, map[string]any{
		"level": "info", "message": "Schedule removed", "schedule": testScheduleName,
		"schedule_id": testScheduleID,
	}, lines[1])
}

func TestEnsureScheduleAbsentScheduleIsNotLoggedAsRemoved(t *testing.T) {
	buf := captureLog(t)
	r := newScheduleRecorder()
	r.handle(testScheduleID).deleteErr = serviceerror.NewNotFound("no schedule")
	require.NoError(t, ensureSchedule(t.Context(), r, removedSchedule()))

	assert.Equal(t, []map[string]any{{
		"level": "debug", "message": "Schedule absent, nothing to remove", "schedule": testScheduleName,
		"schedule_id": testScheduleID,
	}}, logLines(t, buf))
}

package cmd

import (
	"context"
	"testing"
	"time"

	puckdbtemporal "github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// fakeScheduleHandle overrides only Delete and Update; every other
// client.ScheduleHandle method panics if called (the embedded nil interface).
type fakeScheduleHandle struct {
	client.ScheduleHandle
	deleteErr     error
	deleteCalled  bool
	updateErr     error
	updateCalled  bool
	updateOptions client.ScheduleUpdateOptions
}

func (h *fakeScheduleHandle) Delete(context.Context) error {
	h.deleteCalled = true
	return h.deleteErr
}

func (h *fakeScheduleHandle) Update(_ context.Context, options client.ScheduleUpdateOptions) error {
	h.updateCalled = true
	h.updateOptions = options
	return h.updateErr
}

// fakeScheduleClient overrides only Create and GetHandle.
type fakeScheduleClient struct {
	client.ScheduleClient
	handle        *fakeScheduleHandle
	createErr     error
	createCalled  bool
	createOptions client.ScheduleOptions
}

func (c *fakeScheduleClient) GetHandle(context.Context, string) client.ScheduleHandle {
	return c.handle
}

func (c *fakeScheduleClient) Create(_ context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	c.createCalled = true
	c.createOptions = options
	if c.createErr != nil {
		return nil, c.createErr
	}
	return c.handle, nil
}

func TestEnsureNewsSchedule_ZeroMinutesDeletesSchedule(t *testing.T) {
	handle := &fakeScheduleHandle{}
	fakeClient := &fakeScheduleClient{handle: handle}

	err := ensureNewsSchedule(t.Context(), fakeClient, 0)

	require.NoError(t, err)
	assert.True(t, handle.deleteCalled)
	assert.False(t, fakeClient.createCalled)
}

func TestEnsureNewsSchedule_DeleteNotFoundIsIgnored(t *testing.T) {
	handle := &fakeScheduleHandle{deleteErr: serviceerror.NewNotFound("no schedule")}
	fakeClient := &fakeScheduleClient{handle: handle}

	err := ensureNewsSchedule(t.Context(), fakeClient, 0)

	require.NoError(t, err)
}

func TestEnsureNewsSchedule_DeleteOtherErrorPropagates(t *testing.T) {
	handle := &fakeScheduleHandle{deleteErr: assert.AnError}
	fakeClient := &fakeScheduleClient{handle: handle}

	err := ensureNewsSchedule(t.Context(), fakeClient, 0)

	require.Error(t, err)
}

const newsTestScheduleMinutes = 15

func TestEnsureNewsSchedule_PositiveMinutesCreatesSchedule(t *testing.T) {
	handle := &fakeScheduleHandle{}
	fakeClient := &fakeScheduleClient{handle: handle}

	err := ensureNewsSchedule(t.Context(), fakeClient, newsTestScheduleMinutes)

	require.NoError(t, err)
	assert.True(t, fakeClient.createCalled)
	assert.Equal(t, shared.ScheduleIDRefreshNews, fakeClient.createOptions.ID)
	require.Len(t, fakeClient.createOptions.Spec.Intervals, 1)
	assert.Equal(t, newsTestScheduleMinutes*time.Minute, fakeClient.createOptions.Spec.Intervals[0].Every)
	assert.Equal(t, enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, fakeClient.createOptions.Overlap)
	action, ok := fakeClient.createOptions.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	assert.Equal(t, shared.WorkflowIDRefreshNews, action.ID)
	assert.Equal(t, puckdbtemporal.QueueTasks, action.TaskQueue)
}

func TestEnsureNewsSchedule_CreateAlreadyRunningUpdatesInstead(t *testing.T) {
	handle := &fakeScheduleHandle{}
	fakeClient := &fakeScheduleClient{handle: handle, createErr: temporal.ErrScheduleAlreadyRunning}

	err := ensureNewsSchedule(t.Context(), fakeClient, newsTestScheduleMinutes)

	require.NoError(t, err)
	assert.True(t, handle.updateCalled)
}

func TestEnsureNewsSchedule_DoUpdateSetsNewSpecAndAction(t *testing.T) {
	handle := &fakeScheduleHandle{}
	fakeClient := &fakeScheduleClient{handle: handle, createErr: temporal.ErrScheduleAlreadyRunning}
	require.NoError(t, ensureNewsSchedule(t.Context(), fakeClient, newsTestScheduleMinutes))
	require.NotNil(t, handle.updateOptions.DoUpdate)

	input := client.ScheduleUpdateInput{Description: client.ScheduleDescription{Schedule: client.Schedule{
		Spec:   &client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Hour}}},
		Action: &client.ScheduleWorkflowAction{ID: "old"},
	}}}
	update, err := handle.updateOptions.DoUpdate(input)

	require.NoError(t, err)
	require.NotNil(t, update.Schedule)
	require.Len(t, update.Schedule.Spec.Intervals, 1)
	assert.Equal(t, newsTestScheduleMinutes*time.Minute, update.Schedule.Spec.Intervals[0].Every)
	action, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	assert.Equal(t, shared.WorkflowIDRefreshNews, action.ID)
}

// ────────────────────────────────────────────────────────────────────────────
// Sync step / workflow type registration
// ────────────────────────────────────────────────────────────────────────────

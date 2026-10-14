package shared

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// testProgress is the heartbeat details the activities under test report.
const testProgress = "team:7"

// heartbeatRecorder records the details of every heartbeat that reaches the
// SDK, before the SDK batches them. List it before HeartbeatDetailsInterceptor
// so it sees what that interceptor passes on.
type heartbeatRecorder struct {
	interceptor.WorkerInterceptorBase
	mu      sync.Mutex
	details [][]any
}

func (r *heartbeatRecorder) InterceptActivity(
	_ context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	return &recorderInbound{ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next}, r: r}
}

func (r *heartbeatRecorder) all() [][]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.details
}

type recorderInbound struct {
	interceptor.ActivityInboundInterceptorBase
	r *heartbeatRecorder
}

func (i *recorderInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	return i.Next.Init(&recorderOutbound{ActivityOutboundInterceptorBase: interceptor.ActivityOutboundInterceptorBase{Next: outbound}, r: i.r})
}

type recorderOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
	r *heartbeatRecorder
}

func (o *recorderOutbound) RecordHeartbeat(ctx context.Context, details ...any) {
	o.r.mu.Lock()
	o.r.details = append(o.r.details, details)
	o.r.mu.Unlock()
	o.Next.RecordHeartbeat(ctx, details...)
}

// runActivity runs fn as an activity on a test worker with interceptors and
// returns what the recorder saw.
func runActivity(t *testing.T, fn func(ctx context.Context) error, interceptors ...interceptor.WorkerInterceptor) [][]any {
	t.Helper()
	recorder := &heartbeatRecorder{}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: append([]interceptor.WorkerInterceptor{recorder}, interceptors...)})
	env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: "activity"})
	_, err := env.ExecuteActivity("activity")
	require.NoError(t, err)
	return recorder.all()
}

func TestRepeatHeartbeat_RepeatsLatestDetails(t *testing.T) {
	t.Parallel()
	got := runActivity(t, func(ctx context.Context) error {
		activity.RecordHeartbeat(ctx, testProgress)
		RepeatHeartbeat(ctx)
		return nil
	}, &HeartbeatDetailsInterceptor{})

	assert.Equal(t, [][]any{{testProgress}, {testProgress}}, got)
}

func TestRepeatHeartbeat_SendsNoDetailsWithoutInterceptor(t *testing.T) {
	t.Parallel()
	got := runActivity(t, func(ctx context.Context) error {
		activity.RecordHeartbeat(ctx, testProgress)
		RepeatHeartbeat(ctx)
		return nil
	})

	require.Len(t, got, 2)
	assert.Empty(t, got[1])
}

func TestWaitForToken_KeepsActivityProgressWhileWaiting(t *testing.T) {
	t.Parallel()
	limiter := drained(t, testHeartbeatWait)
	got := runActivity(t, func(ctx context.Context) error {
		activity.RecordHeartbeat(ctx, testProgress)
		return waitForToken(ctx, limiter, testHeartbeatInterval)
	}, &HeartbeatDetailsInterceptor{})

	require.Greater(t, len(got), 1, "the wait must heartbeat")
	for _, details := range got {
		assert.Equal(t, []any{testProgress}, details)
	}
}

package workflow

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// activityOptionsRecorder records, per activity type, whether each call was
// scheduled with WaitForCancellation. The test environment resolves a
// cancelled activity at once whatever the option, so the option itself is
// what a test can check.
type activityOptionsRecorder struct {
	interceptor.WorkerInterceptorBase
	mu   sync.Mutex
	wait map[string][]bool
}

func (r *activityOptionsRecorder) InterceptWorkflow(_ workflow.Context,
	next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &activityOptionsInbound{WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next}, r: r}
}

type activityOptionsInbound struct {
	interceptor.WorkflowInboundInterceptorBase
	r *activityOptionsRecorder
}

func (i *activityOptionsInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&activityOptionsOutbound{WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound}, r: i.r})
}

type activityOptionsOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
	r *activityOptionsRecorder
}

func (o *activityOptionsOutbound) ExecuteActivity(ctx workflow.Context, activityType string, args ...any) workflow.Future {
	o.r.mu.Lock()
	o.r.wait[activityType] = append(o.r.wait[activityType], workflow.GetActivityOptions(ctx).WaitForCancellation)
	o.r.mu.Unlock()
	return o.Next.ExecuteActivity(ctx, activityType, args...)
}

func TestFetchYahooPlayersWorkflow_PageAndCommitWaitForCancellation(t *testing.T) {
	f := twoDueSeasonFakes()
	f.seasons = f.seasons[:1]
	recorder := &activityOptionsRecorder{wait: map[string][]bool{}}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{recorder}})
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	f.register(env)

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, nil)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, map[string][]bool{
		"ListYahooNHLGameSeasons":   {false},
		"PlanYahooGamePlayerList":   {false},
		"FetchYahooGamePlayersPage": {true, true},
		"CommitYahooGamePlayerList": {true},
	}, recorder.wait, "a cancelled page or commit ends before the discard reads the manifest")
}

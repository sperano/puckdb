package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/maurice"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// MockTemporalClient is a mock implementation of client.Client
type MockTemporalClient struct {
	mock.Mock
	client.Client
}

// MockWorkflowRun is a mock implementation of client.WorkflowRun
type MockWorkflowRun struct {
	mock.Mock
	client.WorkflowRun
}

func (m *MockTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	callArgs := m.Called(ctx, options, workflow, args)
	run := callArgs.Get(0)
	if run == nil {
		return nil, callArgs.Error(1)
	}
	return run.(client.WorkflowRun), callArgs.Error(1)
}

func (m *MockTemporalClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	args := m.Called(ctx, workflowID, runID)
	return args.Error(0)
}

// TestFetchAssetsResolver_DispatchesOnAssetQueue verifies that the fetchAssets
// resolver routes the workflow start to puckdb-asset-tasks. Without this, the
// asset worker would never pick up the run.
//
// ExecuteWorkflow returns an error so executeAssetWorkflow short-circuits
// before the post-success Redis cleanup; the test's nil RedisClient is fine
// in that path. The mock matcher is what verifies queue routing.
func TestFetchAssetsResolver_DispatchesOnAssetQueue(t *testing.T) {
	t.Parallel()
	mc := &MockTemporalClient{}
	mc.On("ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == shared.WorkflowIDFetchAssets &&
				opts.TaskQueue == shared.TaskQueueAssets &&
				opts.WorkflowTaskTimeout > 0
		}),
		mock.Anything, mock.Anything,
	).Return(nil, errors.New("test-only abort"))

	r := &Resolver{TemporalClient: mc}
	ok, err := r.fetchAssets(context.Background(), &model.FetchAssetsInput{})
	require.Error(t, err)
	require.False(t, ok)
	mc.AssertExpectations(t)
}

// TestCancelFetchAssetsResolver pins the schema.resolvers.go CancelFetchAssets
// contract: it must cancel the well-known fetchAssets workflow ID. There is
// no dedicated resolver.go cancelFetchAssets wrapper (removed as a pure
// passthrough in the T3-A dedup pass) — CancelFetchAssets calls the shared
// cancelWorkflow helper directly with shared.WorkflowIDFetchAssets.
func TestCancelFetchAssetsResolver(t *testing.T) {
	t.Parallel()
	mc := &MockTemporalClient{}
	mc.On("CancelWorkflow", mock.Anything, shared.WorkflowIDFetchAssets, "").Return(nil)

	r := &Resolver{TemporalClient: mc}
	ok, err := r.cancelWorkflow(context.Background(), shared.WorkflowIDFetchAssets)
	require.NoError(t, err)
	require.True(t, ok)
	mc.AssertExpectations(t)
}

// TestIsTerminalFailureStatus pins B8: FailureReason must be fetched for every
// terminal non-success state (Failed, TimedOut, Terminated, Canceled), not
// just Failed. Success / in-flight / continued states must not.
func TestIsTerminalFailureStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status model.TemporalWorkflowStatus
		want   bool
	}{
		{model.TemporalWorkflowStatusFailed, true},
		{model.TemporalWorkflowStatusTimedOut, true},
		{model.TemporalWorkflowStatusTerminated, true},
		{model.TemporalWorkflowStatusCanceled, true},
		{model.TemporalWorkflowStatusCompleted, false},
		{model.TemporalWorkflowStatusRunning, false},
		{model.TemporalWorkflowStatusContinuedAsNew, false},
		{model.TemporalWorkflowStatusUnspecified, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isTerminalFailureStatus(tc.status), "status=%v", tc.status)
	}
}

// TestTemporalStatusMap_CoversAllKnownStatuses guards the B8 comma-ok lookup:
// every Temporal enum the SDK can report must map to a concrete GQL status, so
// getWorkflowResult only takes the "unknown status" error branch on a genuinely
// new enum rather than silently returning the zero value.
func TestTemporalStatusMap_CoversAllKnownStatuses(t *testing.T) {
	t.Parallel()
	known := []temporalEnums.WorkflowExecutionStatus{
		temporalEnums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_FAILED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_CANCELED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
	}
	for _, s := range known {
		_, ok := temporalStatusToGQL[s]
		assert.True(t, ok, "status %v must be mapped", s)
	}
}

// fakeMaurice records the user and request of each Maurice call.
type fakeMaurice struct {
	maurice.Service
	chat     maurice.ChatRequest
	listUser string
	getUser  string
	delUser  string
}

func (f *fakeMaurice) Chat(_ context.Context, req maurice.ChatRequest) (*maurice.ChatResponse, error) {
	f.chat = req
	return &maurice.ChatResponse{ConversationID: "c1", MessageID: "m1", Content: "hi"}, nil
}

func (f *fakeMaurice) ListConversations(_ context.Context, userID string, _ int) ([]*maurice.Conversation, error) {
	f.listUser = userID
	return nil, nil
}

func (f *fakeMaurice) GetConversation(_ context.Context, userID, id string) (*maurice.Conversation, []*maurice.Message, error) {
	f.getUser = userID
	return &maurice.Conversation{ID: id}, nil, nil
}

func (f *fakeMaurice) DeleteConversation(_ context.Context, userID, _ string) error {
	f.delUser = userID
	return nil
}

// Every Maurice operation needs the request's PuckDB user and is scoped by it.
func TestMauriceResolvers_RequireAndScopeByUser(t *testing.T) {
	svc := &fakeMaurice{}
	r := &Resolver{MauriceService: svc}

	_, err := r.mauriceChat(context.Background(), nil, "hi", nil)
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	_, err = r.mauriceConversations(context.Background(), nil)
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)

	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1", SessionID: "s"})
	key := "retry-key"
	resp, err := r.mauriceChat(ctx, nil, "hi", &key)
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
	assert.Equal(t, []string{}, resp.ToolsUsed)
	assert.Equal(t, maurice.ChatRequest{UserID: "user-1", Message: "hi", IdempotencyKey: key}, svc.chat)

	_, err = r.mauriceConversations(ctx, nil)
	require.NoError(t, err)
	_, err = r.mauriceConversation(ctx, "c1")
	require.NoError(t, err)
	_, err = r.mauriceDeleteConversation(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, []string{"user-1", "user-1", "user-1"}, []string{svc.listUser, svc.getUser, svc.delUser})
}

func TestMauriceResolvers_NotConfigured(t *testing.T) {
	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1"})
	_, err := (&Resolver{}).mauriceChat(ctx, nil, "hi", nil)
	require.ErrorIs(t, err, errMauriceNotConfigured)
}

func TestYahooTokenStatus_LoginURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		publicURL string
		want      string
	}{
		{name: "public URL configured", publicURL: "https://puck.example", want: "https://puck.example/yahoo/login"},
		{name: "no public URL", want: "/yahoo/login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := miniredis.RunT(t)
			redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = redisClient.Close() })

			r := &Resolver{RedisClient: redisClient, PublicURL: tt.publicURL}
			status, err := r.yahooTokenStatus(context.Background())
			require.NoError(t, err)
			assert.False(t, status.Valid, "no token is stored")
			assert.Equal(t, tt.want, status.LoginURL)
		})
	}
}

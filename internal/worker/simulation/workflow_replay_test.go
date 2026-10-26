package simulation

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	simReplayTemporalHostEnv = "PUCKDB_TEST_TEMPORAL_HOSTPORT"
	simReplayServerTimeout   = 90 * time.Second
	simReplayRunTimeout      = 30 * time.Second
	simReplayProgressSave    = "Save"
	simReplaySideEffect      = "SideEffect"
	simReplayPoolID          = int32(41)
	simReplayMainCommit      = "de79859"
	simReplayMainHistoryFile = "testdata/sim_pool_main_history.json"
)

// These types reproduce the nested candidate payload from main before its
// historical field names were made explicit with JSON tags.
type legacySkaterDraftCandidate struct {
	PlayerID int64
	Name     string
	Position string
	PriorG   int
	PriorA   int
}

type legacyGoalieDraftCandidate struct {
	PlayerID int64
	Name     string
	Position string
	PriorW   int
	PriorGA  int
}

type legacyDraftCandidatesResult struct {
	Skaters []legacySkaterDraftCandidate `json:"skaters"`
	Goalies []legacyGoalieDraftCandidate `json:"goalies"`
}

func TestSimPoolWorkflowReplaysMainHistory(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(SimPoolWorkflow, workflow.RegisterOptions{Name: SimPoolWorkflowName})
	require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, simReplayMainHistoryFile),
		"history recorded from main at %s", simReplayMainCommit)
}

func TestSimPoolWorkflowReplaysDeterministicDraftPayloads(t *testing.T) {
	temporalClient, stopServer := startSimReplayServer(t)
	defer stopServer()

	taskQueue := simReplayUniqueName("simulation-replay")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	RegisterSimPoolWorkflow(w)
	registerSimReplayActivities(w)
	require.NoError(t, w.Start())
	defer w.Stop()

	history := runSimReplayWorkflow(t, temporalClient, taskQueue)
	assertSimReplayHistory(t, history)

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(SimPoolWorkflow, workflow.RegisterOptions{Name: SimPoolWorkflowName})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, history))
}

func registerSimReplayActivities(w worker.Worker) {
	registerSimReplayActivity(w, activityNameLoadPoolState, simReplayLoadPoolState)
	registerSimReplayActivity(w, activityNameLoadDraftCandidates, simReplayLoadCandidates)
	registerSimReplayActivity(w, activityNamePickTeamName, simReplayPickTeamName)
	registerSimReplayActivity(w, activityNameRecordDraftOrder, func(context.Context, RecordDraftOrderInput) error { return nil })
	registerSimReplayActivity(w, activityNameDraftPick, simReplayDraftPick)
	registerSimReplayActivity(w, activityNameSetPoolStatus, func(context.Context, SetPoolStatusInput) error { return nil })
	registerSimReplayActivity(w, simReplayProgressSave, func(context.Context, string, string, []byte) error { return nil })
}

func registerSimReplayActivity(w worker.Worker, name string, fn any) {
	w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
}

func simReplayLoadPoolState(context.Context, LoadPoolStateInput) (LoadPoolStateResult, error) {
	date := pgtype.Date{Time: time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	return LoadPoolStateResult{
		PoolConfig:      PoolConfig{Season: 20262027, NumTeams: 3, DraftRounds: 1, StopAfter: StopAfterDraft},
		AgentIDs:        []int32{7, 8, 9},
		Agents:          []AgentConfig{{Model: "one"}, {Model: "two"}, {Model: "three"}},
		AgentNotes:      []string{"", "", ""},
		SeasonStartDate: date,
		SeasonEndDate:   date,
		PoolStatus:      PoolStatusDraft,
	}, nil
}

func simReplayLoadCandidates(context.Context, LoadDraftCandidatesInput) (legacyDraftCandidatesResult, error) {
	return legacyDraftCandidatesResult{Skaters: []legacySkaterDraftCandidate{
		{PlayerID: 103, Name: "Three", Position: "C", PriorG: 3},
		{PlayerID: 101, Name: "One", Position: "C", PriorG: 1},
		{PlayerID: 102, Name: "Two", Position: "C", PriorG: 2},
	}}, nil
}

func simReplayPickTeamName(context.Context, PickTeamNameInput) (PickTeamNameResult, error) {
	return PickTeamNameResult{Name: "Replay"}, nil
}

func simReplayDraftPick(_ context.Context, in DraftPickInput) (DraftPickResult, error) {
	if len(in.AvailableIDs) == 0 {
		return DraftPickResult{}, nil
	}
	return DraftPickResult{PlayerID: in.AvailableIDs[0]}, nil
}

func runSimReplayWorkflow(t *testing.T, temporalClient client.Client, taskQueue string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), simReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: simReplayUniqueName(SimPoolWorkflowName), TaskQueue: taskQueue,
	}, SimPoolWorkflowName, SimPoolWorkflowInput{PoolID: simReplayPoolID})
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))
	return readSimReplayHistory(t, temporalClient, run.GetID(), run.GetRunID())
}

func assertSimReplayHistory(t *testing.T, history *historypb.History) {
	t.Helper()
	wantActivities := []string{
		activityNameLoadPoolState,
		activityNamePickTeamName, activityNamePickTeamName, activityNamePickTeamName,
		activityNameLoadDraftCandidates, activityNameRecordDraftOrder,
		activityNameDraftPick, activityNameDraftPick, activityNameDraftPick,
		activityNameSetPoolStatus,
	}
	var gotActivities []string
	var draftInputs []DraftPickInput
	markers := 0
	for _, event := range history.Events {
		if event.GetEventType() == enumspb.EVENT_TYPE_MARKER_RECORDED &&
			event.GetMarkerRecordedEventAttributes().GetMarkerName() == simReplaySideEffect {
			markers++
		}
		if event.GetEventType() != enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
			continue
		}
		attrs := event.GetActivityTaskScheduledEventAttributes()
		name := attrs.GetActivityType().GetName()
		gotActivities = append(gotActivities, name)
		if name == activityNameDraftPick {
			var input DraftPickInput
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(attrs.Input, &input))
			draftInputs = append(draftInputs, input)
		}
	}
	require.Equal(t, wantActivities, gotActivities)
	require.Equal(t, 1, markers, "draft shuffle seed must be recorded exactly once")
	require.Len(t, draftInputs, 3)
	for index, input := range draftInputs {
		assert.Equal(t, simReplayPoolID, input.PoolID)
		assert.Equal(t, index, len(input.Taken))
		assert.True(t, slices.IsSorted(input.Taken), "pick %d taken payload", index+1)
		require.Len(t, input.RankedSkaters, 3, "legacy candidate payload must decode")
	}
}

func startSimReplayServer(t *testing.T) (client.Client, func()) {
	t.Helper()
	if hostPort := os.Getenv(simReplayTemporalHostEnv); hostPort != "" {
		temporalClient, err := client.Dial(client.Options{HostPort: hostPort})
		if err != nil {
			t.Skipf("%s=%q dial failed: %v", simReplayTemporalHostEnv, hostPort, err)
		}
		return temporalClient, func() { temporalClient.Close() }
	}
	ctx, cancel := context.WithTimeout(context.Background(), simReplayServerTimeout)
	defer cancel()
	server, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
	if err != nil {
		t.Skipf("Temporal dev server failed: %v", err)
	}
	return server.Client(), func() { _ = server.Stop() }
}

func readSimReplayHistory(t *testing.T, temporalClient client.Client, workflowID, runID string) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), simReplayRunTimeout)
	defer cancel()
	iter := temporalClient.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	history := &historypb.History{}
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		history.Events = append(history.Events, event)
	}
	require.NotEmpty(t, history.Events)
	return history
}

func simReplayUniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

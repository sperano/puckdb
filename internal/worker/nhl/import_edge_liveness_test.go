package nhl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// =============================================================================
// Heartbeats are captured where the activity records them: an outbound
// interceptor sees every activity.RecordHeartbeat call, before the SDK's
// heartbeat batching. That batching (a window of 80% of HeartbeatTimeout, or
// 30s without one) holds back every call after the first in a window and
// drops the held one when the activity succeeds first, so a heartbeat
// listener sees only some of them; the test environment ignores
// worker.Options' throttle settings, so the window cannot be shortened.
//
// Since SDK 1.52 the test environment enforces HeartbeatTimeout like a real
// server: an activity whose delivered heartbeats lapse longer than it fails
// with a heartbeat timeout. The liveness test runs with HeartbeatTimeout =
// simulatedHeartbeatTimeout, so its success proves delivery; the content
// tests leave it unset (unenforcedHeartbeatTimeout).
// =============================================================================

const (
	// unenforcedHeartbeatTimeout leaves the heartbeat timeout unset, for the
	// tests that check heartbeat content only.
	unenforcedHeartbeatTimeout time.Duration = 0

	// slowUpsertDelay simulates a slow per-item database upsert.
	slowUpsertDelay = 20 * time.Millisecond

	// simulatedHeartbeatTimeout is the business value under test: with
	// Temporal's heartbeat timeout this long, does our heartbeating keep the
	// activity alive? Delivered heartbeats arrive about every 80% of it (the
	// SDK's batching window), so it must stay well above slowUpsertDelay for
	// a margin against scheduling jitter under -race/parallel load.
	simulatedHeartbeatTimeout = 200 * time.Millisecond

	// livenessRosterSize * slowUpsertDelay must comfortably exceed
	// simulatedHeartbeatTimeout so the import genuinely outlives it.
	livenessRosterSize       = 20
	testLivenessSkaterIDBase = int64(8490000)

	activityStartToCloseTimeout = 30 * time.Second
	cancelAfterSkaterCount      = 2
)

// heartbeatCapture records each recorded heartbeat's detail and time under a
// mutex, as a worker interceptor around the activities the test runs.
type heartbeatCapture struct {
	interceptor.WorkerInterceptorBase
	t       *testing.T
	mu      sync.Mutex
	details []string
	at      []time.Time
}

func newHeartbeatCapture(t *testing.T) *heartbeatCapture {
	return &heartbeatCapture{t: t}
}

func (h *heartbeatCapture) InterceptActivity(_ context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	return &heartbeatInbound{ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next}, capture: h}
}

func (h *heartbeatCapture) record(details []any) {
	if len(details) != 1 {
		h.t.Errorf("heartbeat with %d details, want 1", len(details))
		return
	}
	s, ok := details[0].(string)
	if !ok {
		h.t.Errorf("heartbeat detail %T, want string", details[0])
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.details = append(h.details, s)
	h.at = append(h.at, time.Now())
}

func (h *heartbeatCapture) snapshot() ([]string, []time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.details), slices.Clone(h.at)
}

type heartbeatInbound struct {
	interceptor.ActivityInboundInterceptorBase
	capture *heartbeatCapture
}

func (i *heartbeatInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	return i.Next.Init(&heartbeatOutbound{ActivityOutboundInterceptorBase: interceptor.ActivityOutboundInterceptorBase{Next: outbound}, capture: i.capture})
}

type heartbeatOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
	capture *heartbeatCapture
}

func (o *heartbeatOutbound) RecordHeartbeat(ctx context.Context, details ...any) {
	o.capture.record(details)
	o.Next.RecordHeartbeat(ctx, details...)
}

// executeViaWorkflow runs activityFn(input) through a workflow with the given
// HeartbeatTimeout (enforced by the test environment when positive),
// capturing its heartbeats in hb.
func executeViaWorkflow(t *testing.T, env *testsuite.TestWorkflowEnvironment, hb *heartbeatCapture, heartbeatTimeout time.Duration, activityFn, input any) error {
	t.Helper()
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{hb}})
	wf := func(ctx workflow.Context) error {
		actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: activityStartToCloseTimeout,
			HeartbeatTimeout:    heartbeatTimeout,
		})
		return workflow.ExecuteActivity(actx, activityFn, input).Get(actx, nil)
	}
	env.RegisterWorkflow(wf)
	env.ExecuteWorkflow(wf)
	require.True(t, env.IsWorkflowCompleted())
	return env.GetWorkflowError()
}

// =============================================================================
// Per-team heartbeat tests (item 2): heartbeats fire for every roster
// player, including ones with no cached detail file.
// =============================================================================

type ImportEdgeHeartbeatSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestImportEdgeHeartbeatSuite(t *testing.T) {
	suite.Run(t, new(ImportEdgeHeartbeatSuite))
}

func (s *ImportEdgeHeartbeatSuite) TestTeamSkaters_HeartbeatsIncludeUncachedPlayer() {
	mem := store.NewMemStorage()
	scope := testScope()
	// testSkaterID1 is cached; testSkaterID2 has no cached detail file at all.
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(testSkaterID1, testSkaterID2))
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID1, scope.season, scope.gameType, baseSkaterDetail())

	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.RegisterActivity(act.ImportEdgeTeamSkaters)

	err := executeViaWorkflow(s.T(), env, hb, unenforcedHeartbeatTimeout, act.ImportEdgeTeamSkaters, testTeamInput())
	s.Require().NoError(err)

	details, _ := hb.snapshot()
	s.Equal([]string{
		fmt.Sprintf("import-edge-%s:%s:%d", edgeKindSkater, testEdgeTeamAbbrev, testSkaterID1),
		fmt.Sprintf("import-edge-%s:%s:%d", edgeKindSkater, testEdgeTeamAbbrev, testSkaterID2),
	}, details)
}

func (s *ImportEdgeHeartbeatSuite) TestTeamGoalies_HeartbeatsIncludeUncachedPlayer() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithGoalies(testGoalieID1, testGoalieID2))
	seedEdgeGoalieDetail(s.T(), mem, testGoalieID1, scope.season, scope.gameType, baseGoalieDetail())

	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.RegisterActivity(act.ImportEdgeTeamGoalies)

	err := executeViaWorkflow(s.T(), env, hb, unenforcedHeartbeatTimeout, act.ImportEdgeTeamGoalies, testTeamInput())
	s.Require().NoError(err)

	details, _ := hb.snapshot()
	s.Equal([]string{
		fmt.Sprintf("import-edge-%s:%s:%d", edgeKindGoalie, testEdgeTeamAbbrev, testGoalieID1),
		fmt.Sprintf("import-edge-%s:%s:%d", edgeKindGoalie, testEdgeTeamAbbrev, testGoalieID2),
	}, details)
}

func (s *ImportEdgeHeartbeatSuite) TestTeam_HeartbeatsForStatsAndZoneTime() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedEdgeTeamDetail(s.T(), mem, testEdgeTeamID, scope.season, scope.gameType, baseTeamDetail())
	seedEdgeTeamZoneTimeDetail(s.T(), mem, testEdgeTeamID, scope.season, scope.gameType, baseTeamZoneTimeDetail())

	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.RegisterActivity(act.ImportEdgeTeam)

	err := executeViaWorkflow(s.T(), env, hb, unenforcedHeartbeatTimeout, act.ImportEdgeTeam, testTeamInput())
	s.Require().NoError(err)

	details, _ := hb.snapshot()
	s.Equal([]string{
		fmt.Sprintf("import-edge-%s:%s", edgeKindTeam, testEdgeTeamAbbrev),
		fmt.Sprintf("import-edge-%s:%s", edgeKindTeamZoneTime, testEdgeTeamAbbrev),
	}, details)
}

// =============================================================================
// Slow-import liveness test (item 3).
// =============================================================================

type ImportEdgeLivenessSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestImportEdgeLivenessSuite(t *testing.T) {
	suite.Run(t, new(ImportEdgeLivenessSuite))
}

func (s *ImportEdgeLivenessSuite) TestTeamSkaters_HeartbeatsStayLiveDuringSlowImport() {
	mem := store.NewMemStorage()
	scope := testScope()
	ids := make([]int64, livenessRosterSize)
	for i := range ids {
		ids[i] = testLivenessSkaterIDBase + int64(i)
	}
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(ids...))
	for _, id := range ids {
		seedEdgeSkaterDetail(s.T(), mem, id, scope.season, scope.gameType, baseSkaterDetail())
	}

	fake := newFakeEdgeUpserter()
	fake.setDelay(slowUpsertDelay)
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.RegisterActivity(act.ImportEdgeTeamSkaters)

	start := time.Now()
	err := executeViaWorkflow(s.T(), env, hb, simulatedHeartbeatTimeout, act.ImportEdgeTeamSkaters, testTeamInput())
	elapsed := time.Since(start)
	s.Require().NoError(err)

	s.Greater(elapsed, simulatedHeartbeatTimeout,
		"importing %d players at %s each should exceed the simulated heartbeat timeout", livenessRosterSize, slowUpsertDelay)

	_, at := hb.snapshot()
	s.Require().NotEmpty(at)
	s.Less(at[0].Sub(start), simulatedHeartbeatTimeout, "first heartbeat should arrive well before the simulated timeout")
	for i := 1; i < len(at); i++ {
		gap := at[i].Sub(at[i-1])
		s.Less(gap, simulatedHeartbeatTimeout, "gap between heartbeats %d and %d exceeded the simulated timeout", i-1, i)
	}
}

// =============================================================================
// Cancellation test (item 4). TestActivityEnvironment cannot cancel an
// activity mid-execution, so we derive a cancellable child context inside a
// small test-only activity and cancel it ourselves via the fake's hook.
// =============================================================================

type ImportEdgeCancellationSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportEdgeCancellationSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportEdgeCancellationSuite(t *testing.T) {
	suite.Run(t, new(ImportEdgeCancellationSuite))
}

func (s *ImportEdgeCancellationSuite) TestMidRosterCancellation_StopsAfterConfiguredCount() {
	mem := store.NewMemStorage()
	scope := testScope()
	ids := []int64{testSkaterID1, testSkaterID2, testSkaterID3, testSkaterID4}
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(ids...))
	for _, id := range ids {
		seedEdgeSkaterDetail(s.T(), mem, id, scope.season, scope.gameType, baseSkaterDetail())
	}

	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}
	team := edgeTeam{id: nhlapi.TeamID(testEdgeTeamID), abbrev: testEdgeTeamAbbrev}

	var processed int
	var rawErr error
	testAct := func(ctx context.Context) error {
		cctx, cancel := context.WithCancel(ctx)
		fake.setOnSkaterStats(func(int64) {
			processed++
			if processed == cancelAfterSkaterCount {
				cancel()
			}
		})
		_, err := act.importEdgeTeamSkaters(cctx, scope, team)
		rawErr = err
		return err
	}
	s.env.RegisterActivity(testAct)

	_, _ = s.env.ExecuteActivity(testAct)

	s.Require().Error(rawErr)
	s.True(errors.Is(rawErr, context.Canceled), "expected the raw error to wrap context.Canceled, got %v", rawErr)
	s.Len(fake.callsFor("UpsertEdgeSkaterStats"), cancelAfterSkaterCount)
}

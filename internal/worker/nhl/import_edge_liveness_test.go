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
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// =============================================================================
// Empirical finding (see task write-up): testsuite.TestActivityEnvironment
// hard-codes HeartbeatTimeout=0 for ExecuteActivity, so the SDK always uses
// its default 30s heartbeat-throttle window there — every RecordHeartbeat
// call within that window after the first is buffered and, on success,
// silently discarded (only flushed if the activity ultimately errors). This
// was confirmed empirically: an activity heartbeating 5 times with 200ms
// gaps delivered only the first call to the listener on success, and both
// the first and last on failure.
//
// Driving the activity through a TestWorkflowEnvironment instead lets us set
// workflow.ActivityOptions.HeartbeatTimeout, which the SDK uses to size that
// throttle window (80% of the configured value). Setting it short — via
// testActivityHeartbeatTimeout below — makes the window shorter than our
// fake's per-upsert delay, so the test genuinely observes (approximately)
// every heartbeat instead of us faking that behavior.
// =============================================================================

const (
	// heartbeatSpacingDelay is applied to a single upsert call so the next
	// heartbeat call in a two-item scenario is guaranteed to land after the
	// previous throttle window has closed (testActivityHeartbeatTimeout's
	// 80% window is orders of magnitude smaller than this).
	heartbeatSpacingDelay = 100 * time.Millisecond

	// testActivityHeartbeatTimeout is test-infrastructure only: it configures
	// the SDK's internal batching window (~80% of this value), not the
	// business value under test. It must stay well under slowUpsertDelay so
	// the liveness test observes (approximately) every heartbeat.
	testActivityHeartbeatTimeout = 5 * time.Millisecond

	// slowUpsertDelay simulates a slow per-item database upsert.
	slowUpsertDelay = 20 * time.Millisecond

	// simulatedHeartbeatTimeout is the business value under test: "if
	// Temporal's real heartbeat timeout were this long, would our
	// heartbeating keep the activity alive?" It must be >= 5x slowUpsertDelay
	// for a generous margin against scheduling jitter under -race/parallel load.
	simulatedHeartbeatTimeout = 100 * time.Millisecond

	// livenessRosterSize * slowUpsertDelay must comfortably exceed
	// simulatedHeartbeatTimeout so the import genuinely outlives it.
	livenessRosterSize       = 12
	testLivenessSkaterIDBase = int64(8490000)

	activityStartToCloseTimeout = 30 * time.Second
	cancelAfterSkaterCount      = 2
)

// heartbeatCapture records delivered heartbeat details and their arrival
// time under a mutex; the SDK may call the listener from a background
// batch-flush goroutine concurrently with the activity's own goroutine.
type heartbeatCapture struct {
	t       *testing.T
	mu      sync.Mutex
	details []string
	at      []time.Time
}

func newHeartbeatCapture(t *testing.T) *heartbeatCapture {
	return &heartbeatCapture{t: t}
}

func (h *heartbeatCapture) listener() func(*activity.Info, converter.EncodedValues) {
	return func(_ *activity.Info, details converter.EncodedValues) {
		var s string
		if err := details.Get(&s); err != nil {
			h.t.Errorf("decode heartbeat detail: %v", err)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.details = append(h.details, s)
		h.at = append(h.at, time.Now())
	}
}

func (h *heartbeatCapture) snapshot() ([]string, []time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.details), slices.Clone(h.at)
}

// executeViaWorkflow runs activityFn(input) through a workflow with a short
// HeartbeatTimeout, so the SDK's heartbeat-batching window stays shorter
// than the delays our fakes introduce (see the empirical finding above).
func executeViaWorkflow(t *testing.T, env *testsuite.TestWorkflowEnvironment, heartbeatTimeout time.Duration, activityFn, input any) error {
	t.Helper()
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
	// testSkaterID1 is cached (and slow, to space out the next heartbeat);
	// testSkaterID2 has no cached detail file at all.
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(testSkaterID1, testSkaterID2))
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID1, scope.season, scope.gameType, baseSkaterDetail())

	fake := newFakeEdgeUpserter()
	fake.setDelay(heartbeatSpacingDelay)
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.SetOnActivityHeartbeatListener(hb.listener())
	env.RegisterActivity(act.ImportEdgeTeamSkaters)

	err := executeViaWorkflow(s.T(), env, testActivityHeartbeatTimeout, act.ImportEdgeTeamSkaters, testTeamInput())
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
	fake.setDelay(heartbeatSpacingDelay)
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.SetOnActivityHeartbeatListener(hb.listener())
	env.RegisterActivity(act.ImportEdgeTeamGoalies)

	err := executeViaWorkflow(s.T(), env, testActivityHeartbeatTimeout, act.ImportEdgeTeamGoalies, testTeamInput())
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
	fake.setDelay(heartbeatSpacingDelay)
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}

	env := s.NewTestWorkflowEnvironment()
	hb := newHeartbeatCapture(s.T())
	env.SetOnActivityHeartbeatListener(hb.listener())
	env.RegisterActivity(act.ImportEdgeTeam)

	err := executeViaWorkflow(s.T(), env, testActivityHeartbeatTimeout, act.ImportEdgeTeam, testTeamInput())
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
	env.SetOnActivityHeartbeatListener(hb.listener())
	env.RegisterActivity(act.ImportEdgeTeamSkaters)

	start := time.Now()
	err := executeViaWorkflow(s.T(), env, testActivityHeartbeatTimeout, act.ImportEdgeTeamSkaters, testTeamInput())
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

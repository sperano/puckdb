package simulation

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// SimPoolWorkflowTestSuite — covers signal state machine, ContinueAsNew,
// cancel handling, Phase 1 signal handling, and the shouldProcessNow
// pure helper.
//
// Activities are mocked via env.OnActivity. The actual activity
// behavior is unit-tested separately (draft_activity_test.go,
// manage_activity_test.go, collect_activity_test.go,
// waivers_activity_test.go); this suite tests workflow plumbing
// in isolation.
// ============================================================================

type SimPoolWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env  *testsuite.TestWorkflowEnvironment
	acts *Activities
}

func (s *SimPoolWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	// Register the activity STRUCT — every method becomes an
	// activity. Tests call OnActivity to script behavior per
	// activity type.
	s.acts = &Activities{}
	s.env.RegisterActivity(s.acts.LoadPoolState)
	s.env.RegisterActivity(s.acts.LoadDraftCandidates)
	s.env.RegisterActivity(s.acts.DraftPick)
	s.env.RegisterActivity(s.acts.ManageRoster)
	s.env.RegisterActivity(s.acts.BuildFreeAgentPool)
	s.env.RegisterActivity(s.acts.ProcessWaivers)
	s.env.RegisterActivity(s.acts.CollectDayStats)
	s.env.RegisterActivity(s.acts.UpdateStandings)
	s.env.RegisterActivity(s.acts.RecordDayDuration)
	s.env.RegisterActivity(s.acts.SetPoolStatus)
	s.env.RegisterActivity(s.acts.RecordDraftOrder)
	s.env.RegisterActivity(s.acts.PickTeamName)
	s.env.RegisterActivity(s.acts.BuildManageRosterContext)
}

func TestSimPoolWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(SimPoolWorkflowTestSuite))
}

// ============================================================================
// Pure-function tests — StopAfter enum round-trip
// ============================================================================

func TestStopAfter_StringRoundTrip(t *testing.T) {
	cases := []struct {
		val   StopAfter
		label string
	}{
		{StopAfterNever, "never"},
		{StopAfterTeamName, "team_name"},
		{StopAfterDraft, "draft"},
		{StopAfterSeason, "season"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.label, tc.val.String())
			parsed, err := ParseStopAfter(tc.label)
			assert.NoError(t, err)
			assert.Equal(t, tc.val, parsed)
		})
	}
}

func TestParseStopAfter_DefaultsAndErrors(t *testing.T) {
	got, err := ParseStopAfter("")
	assert.NoError(t, err)
	assert.Equal(t, StopAfterNever, got, "empty string defaults to never")

	_, err = ParseStopAfter("garbage")
	assert.Error(t, err, "invalid value must error")
}

// ============================================================================
// Helper builders for test setup
// ============================================================================

// minState returns a LoadPoolStateResult tight enough to drive the
// workflow without exercising every activity. NumTeams=2 so the
// draft is short; DraftRounds=1 means 2 picks total. Season window
// is 3 days so the day loop terminates quickly.
func minState() LoadPoolStateResult {
	return LoadPoolStateResult{
		PoolConfig: PoolConfig{
			Season:               20242025,
			NumTeams:             2,
			DraftRounds:          1,
			MaxLLMCostUsdPerPool: 200,
			WaiverDays:           2,
		},
		AgentIDs: []int32{1, 2},
		Agents: []AgentConfig{
			{Provider: "anthropic", Model: "claude-haiku-4-5"},
			{Provider: "anthropic", Model: "claude-haiku-4-5"},
		},
		SeasonStartDate: pgDate(must, "2024-10-08"),
		SeasonEndDate:   pgDate(must, "2024-10-10"),
	}
}

// must is a sentinel TestingT for use in test fixture builders that
// can't take *testing.T directly. pgDate's signature wants
// require.TestingT, which *testing.T satisfies but the builders
// here are called from non-method scope. Substituting a permissive
// satisfier keeps the helper signatures clean.
type permissiveT struct{}

func (permissiveT) Errorf(string, ...any) {}
func (permissiveT) FailNow()              {}

var must permissiveT

// stubActivityResults wires the boot-time + per-day activity mocks
// that EVERY test needs. ManageRoster is intentionally LEFT OUT —
// tests that want to count ManageRoster calls register their own
// mock first, since testify mock semantics give the first matching
// expectation priority and a dual registration would shadow.
//
// .Maybe() on each call so a test that doesn't reach that activity
// (e.g., LoadPoolState fails) doesn't get spurious unmatched-call
// warnings.
func (s *SimPoolWorkflowTestSuite) stubActivityResults(state LoadPoolStateResult) {
	// Progress tracker's Save/Load activities — the workflow uses
	// shared.InitTracker which fires these via local activities.
	// Without mocks they'd panic on a nil RedisClient.
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Save,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load,
		mock.Anything, mock.Anything).Return([]byte(nil), nil).Maybe()

	s.env.OnActivity(s.acts.LoadPoolState, mock.Anything, mock.Anything).
		Return(state, nil).Maybe()
	s.env.OnActivity(s.acts.LoadDraftCandidates, mock.Anything, mock.Anything).
		Return(LoadDraftCandidatesResult{}, nil).Maybe()
	s.env.OnActivity(s.acts.DraftPick, mock.Anything, mock.Anything).
		Return(DraftPickResult{PlayerID: 1, CostUsd: 0.01}, nil).Maybe()
	s.env.OnActivity(s.acts.ProcessWaivers, mock.Anything, mock.Anything).
		Return(ProcessWaiversResult{Skipped: true}, nil).Maybe()
	s.env.OnActivity(s.acts.BuildFreeAgentPool, mock.Anything, mock.Anything).
		Return(BuildFreeAgentPoolResult{}, nil).Maybe()
	s.env.OnActivity(s.acts.CollectDayStats, mock.Anything, mock.Anything).
		Return(CollectDayStatsResult{Skipped: true, SkipReason: SkipReasonNoGames}, nil).Maybe()
	s.env.OnActivity(s.acts.UpdateStandings, mock.Anything, mock.Anything).
		Return(UpdateStandingsResult{}, nil).Maybe()
	s.env.OnActivity(s.acts.RecordDayDuration, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	s.env.OnActivity(s.acts.SetPoolStatus, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	s.env.OnActivity(s.acts.RecordDraftOrder, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	s.env.OnActivity(s.acts.PickTeamName, mock.Anything, mock.Anything).
		Return(PickTeamNameResult{Name: "Mock Team"}, nil).Maybe()
	s.env.OnActivity(s.acts.BuildManageRosterContext, mock.Anything, mock.Anything).
		Return(ManageRosterInput{}, nil).Maybe()
}

// stubManageRosterPassThrough is the default ManageRoster mock for
// tests that don't care about call counting — registers a "always
// returns Passed=true" handler. Tests that care about counts
// register their own .Run-bearing mock BEFORE calling
// stubActivityResults, since testify gives priority to the first
// registered match.
func (s *SimPoolWorkflowTestSuite) stubManageRosterPassThrough() {
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).
		Return(ManageRosterResult{Passed: true}, nil).Maybe()
}

// ============================================================================
// Initial start — Phase 1 (draft) runs, Phase 2 pauses (PLAN.md
// "set status=paused after draft").
// ============================================================================

// Happy path: workflow runs to season end without any runtime
// signals, exits cleanly with no error.
func (s *SimPoolWorkflowTestSuite) TestInitialStart_RunsToEnd() {
	t := s.T()
	state := minState()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
}

// StopAfterTeamName: workflow exits cleanly after Phase 0; draft and
// season activities never fire.
func (s *SimPoolWorkflowTestSuite) TestStopAfterTeamName_SkipsDraftAndSeason() {
	t := s.T()
	state := minState()
	state.PoolConfig.StopAfter = StopAfterTeamName

	draftCount := 0
	s.env.OnActivity(s.acts.DraftPick, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		draftCount++
	}).Return(DraftPickResult{}, nil).Maybe()
	manageCount := 0
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		manageCount++
	}).Return(ManageRosterResult{Passed: true}, nil).Maybe()
	s.stubActivityResults(state)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 0, draftCount, "draft must be skipped")
	assert.Equal(t, 0, manageCount, "season must be skipped")
}

// StopAfterDraft: workflow exits cleanly after draft; season activities
// never fire.
func (s *SimPoolWorkflowTestSuite) TestStopAfterDraft_SkipsSeason() {
	t := s.T()
	state := minState()
	state.PoolConfig.StopAfter = StopAfterDraft

	manageCount := 0
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		manageCount++
	}).Return(ManageRosterResult{Passed: true}, nil).Maybe()
	s.stubActivityResults(state)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 0, manageCount, "season must be skipped")
}

// MaxSeasonDays caps the day-loop. With cap=1 and minState's 3-day
// season window, the workflow exits after 1 day of activities (2 agents).
func (s *SimPoolWorkflowTestSuite) TestMaxSeasonDays_CapsDayLoop() {
	t := s.T()
	state := minState()
	state.PoolConfig.MaxSeasonDays = 1

	manageCount := 0
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		manageCount++
	}).Return(ManageRosterResult{Passed: true}, nil).Maybe()
	s.stubActivityResults(state)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 2, manageCount, "exactly 1 day × 2 agents")
}

// ContinueAsNew resume — SimDate set on input → team-name + draft
// skipped, straight to season.
func (s *SimPoolWorkflowTestSuite) TestContinueAsNewResume_SkipsDraft() {
	t := s.T()
	state := minState()
	draftCount := 0
	s.env.OnActivity(s.acts.DraftPick, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		draftCount++
	}).Return(DraftPickResult{}, nil).Maybe()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, "2024-10-09"),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 0, draftCount, "Phase 1 must be skipped on CAN resume")
}

// daysElapsed: whole-day delta between two sim dates, 0 on invalid.
func TestDaysElapsed(t *testing.T) {
	cases := []struct {
		name         string
		start, cur   string
		startInvalid bool
		curInvalid   bool
		want         int
	}{
		{name: "same day", start: "2024-10-08", cur: "2024-10-08", want: 0},
		{name: "one day", start: "2024-10-08", cur: "2024-10-09", want: 1},
		{name: "across CAN threshold", start: "2024-10-08", cur: "2024-11-27", want: 50},
		{name: "invalid start", curInvalid: false, startInvalid: true, cur: "2024-10-09", want: 0},
		{name: "invalid current", start: "2024-10-08", curInvalid: true, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := pgtype.Date{}
			if !tc.startInvalid {
				start = pgDate(t, tc.start)
			}
			cur := pgtype.Date{}
			if !tc.curInvalid {
				cur = pgDate(t, tc.cur)
			}
			assert.Equal(t, tc.want, daysElapsed(start, cur))
		})
	}
}

// Finding #4: a MaxSeasonDays cap larger than ContinueAsNewDayThreshold
// must still trip. in.DayCount resets to 0 on every ContinueAsNew, so
// the cap is measured against absolute days elapsed from SeasonStartDate.
//
// Simulate a CAN-resumed execution: SimDate is already 49 days into the
// season (DayCount=0, as CAN always resets it). With cap=50 the loop
// must run exactly one more day then stop — NOT run to the season-end
// date 60 days out, which is what the old in.DayCount check produced
// (0 never reaches 50).
func (s *SimPoolWorkflowTestSuite) TestMaxSeasonDays_CapTripsAfterContinueAsNew() {
	t := s.T()
	const (
		capDays     = 50
		resumeDay   = 49 // absolute days elapsed at CAN resume
		seasonStart = "2024-10-08"
		// resume date = seasonStart + resumeDay
		resumeDate = "2024-11-26"
		// season end far enough out that only the cap can stop the loop
		seasonEnd = "2025-01-08"
	)
	state := minState()
	state.PoolConfig.MaxSeasonDays = capDays
	state.SeasonStartDate = pgDate(t, seasonStart)
	state.SeasonEndDate = pgDate(t, seasonEnd)

	manageCount := 0
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		manageCount++
	}).Return(ManageRosterResult{Passed: true}, nil).Maybe()
	s.stubActivityResults(state)

	// CAN resume: SimDate set (skips team-name + draft), DayCount=0.
	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, resumeDate),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	// capDays - resumeDay = 1 day of activity × 2 agents.
	assert.Equal(t, (capDays-resumeDay)*2, manageCount,
		"cap must trip 1 day after a CAN resume, not run to season end")
}

// Finding #3: a cost-cap pause during the season must NOT complete the
// pool. The cost-cap activity already flipped status to 'paused'; the
// workflow must exit without calling SetPoolStatus(complete), which
// would clobber that 'paused' row.
func (s *SimPoolWorkflowTestSuite) TestSeasonCostCapPause_DoesNotComplete() {
	t := s.T()
	state := minState()

	var statuses []PoolStatus
	s.env.OnActivity(s.acts.SetPoolStatus, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(SetPoolStatusInput)
			statuses = append(statuses, in.Status)
		}).Return(nil).Maybe()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	// Queue the cost-cap pause signal before the first day's post-day
	// checkPause() runs. The signal is delivered to the workflow's pause
	// channel; checkPause()'s ReceiveAsync picks it up after day 1.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalPause, nil)
	}, time.Millisecond)

	// CAN resume input so we go straight to the season day loop.
	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.NotContains(t, statuses, PoolStatusComplete,
		"cost-cap season pause must not overwrite 'paused' with 'complete'")
}

// ============================================================================
// Cancel handling
// ============================================================================

func (s *SimPoolWorkflowTestSuite) TestCancel_ExitsCleanly() {
	t := s.T()
	state := minState()
	// Long-running ManageRoster that blocks until ctx fires — gives
	// the cancel something to interrupt. Without this, mocked
	// activities return instantly and the workflow finishes before
	// the delayed cancel can fire.
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		}).
		Return(ManageRosterResult{}, context.Canceled).Maybe()
	s.stubActivityResults(state)

	s.env.RegisterDelayedCallback(func() {
		s.env.CancelWorkflow()
	}, 50*time.Millisecond)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	require.Error(t, err)
	var canceledErr *temporal.CanceledError
	assert.True(t, asTemporalCanceledError(err, &canceledErr) || isCanceledMessage(err),
		"expected cancellation error, got: %v", err)
}

// ============================================================================
// Activity error propagation
// ============================================================================

func (s *SimPoolWorkflowTestSuite) TestLoadPoolStateError_AbortsWorkflow() {
	t := s.T()
	// LoadPoolState returns an error → workflow aborts before
	// reaching tracker setup, so progress activities are unused.
	s.env.OnActivity(s.acts.LoadPoolState, mock.Anything, mock.Anything).
		Return(LoadPoolStateResult{}, assert.AnError)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.Error(t, s.env.GetWorkflowError())
}

// ============================================================================
// Helpers
// ============================================================================

// asTemporalCanceledError peeks past testsuite's wrapping to find
// the underlying CanceledError. The testsuite test environment may
// surface canceled-from-host as a chain wrap; this helper unwraps.
func asTemporalCanceledError(err error, target **temporal.CanceledError) bool {
	if err == nil {
		return false
	}
	if cerr, ok := err.(*temporal.CanceledError); ok {
		*target = cerr
		return true
	}
	return false
}

// isCanceledMessage is a string-match fallback for cancel detection.
// Some SDK versions wrap CanceledError in workflow.Error which
// doesn't expose As-friendly types; the message contains "canceled"
// in either case.
func isCanceledMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, frag := range []string{"canceled", "cancelled", "Canceled"} {
		if contains(msg, frag) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ============================================================================
// Low-3: draft loop must stop after the first cost-cap trip. Before the
// fix, every remaining pick independently called runCostCapBranch,
// flooding the audit table with duplicate cost_cap_reached rows.
// ============================================================================

// When DraftPick returns SkipReasonCostCapReached on pick k, the
// workflow must not schedule any further DraftPick activities.
// minState has 2 teams × 1 round = 2 picks total; cap trips on pick 1
// → only 1 DraftPick call must be observed.
func (s *SimPoolWorkflowTestSuite) TestDraftCostCap_StopsAfterFirstTrip() {
	t := s.T()
	state := minState()

	pickCount := 0
	s.env.OnActivity(s.acts.DraftPick, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { pickCount++ }).
		Return(DraftPickResult{Skipped: true, SkipReason: SkipReasonCostCapReached}, nil).
		Maybe()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 1, pickCount,
		"cost-cap exit after pick 1 must suppress all remaining draft picks")
}

// ============================================================================
// T1-B: the per-agent daily loop must stop after the first cost-cap trip.
// Before the fix, every remaining agent still ran BuildManageRosterContext
// + ManageRoster, each re-tripping the cap → duplicate cost_cap_reached
// rows + redundant pause signals.
// ============================================================================

// minState has 2 agents. With MaxSeasonDays=1 exactly one day runs.
// ManageRoster returns SkipReasonCostCapReached for the first agent, so
// the daily loop must break: only ONE ManageRoster (and one
// BuildManageRosterContext) call for that day, not two.
func (s *SimPoolWorkflowTestSuite) TestDailyCostCap_StopsAfterFirstAgent() {
	t := s.T()
	state := minState()
	state.PoolConfig.MaxSeasonDays = 1

	manageCount := 0
	s.env.OnActivity(s.acts.ManageRoster, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { manageCount++ }).
		Return(ManageRosterResult{Skipped: true, SkipReason: SkipReasonCostCapReached}, nil).
		Maybe()
	contextCount := 0
	s.env.OnActivity(s.acts.BuildManageRosterContext, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { contextCount++ }).
		Return(ManageRosterInput{}, nil).Maybe()
	s.stubActivityResults(state)

	// CAN-resume input so we go straight to the season day loop.
	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())
	assert.Equal(t, 1, manageCount,
		"daily loop must stop after the first agent trips the cost cap")
	assert.Equal(t, 1, contextCount,
		"remaining agents must not build daily context after the cap trips")
}

// ============================================================================
// T1-D part 1: shouldMarkCancelled classifies the cancel-cleanup gate.
// A genuine cancellation → true; nil, ContinueAsNew, and any other error
// → false (only a real cancel flips the pool to 'cancelled').
// ============================================================================

func TestShouldMarkCancelled(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil (clean completion)", err: nil, want: false},
		{name: "real cancellation", err: temporal.NewCanceledError(), want: true},
		{name: "generic activity error", err: assert.AnError, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldMarkCancelled(tc.err))
		})
	}
}

// ============================================================================
// T1-D part 2: a cancel that lands during RecordDayDuration stops the
// season loop and the pool is marked cancelled (not left running).
// ============================================================================

func (s *SimPoolWorkflowTestSuite) TestCancel_DuringRecordDayDuration_StopsLoop() {
	t := s.T()
	state := minState()

	var statuses []PoolStatus
	s.env.OnActivity(s.acts.SetPoolStatus, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(SetPoolStatusInput)
			statuses = append(statuses, in.Status)
		}).Return(nil).Maybe()
	// RecordDayDuration blocks until the workflow ctx is cancelled — gives
	// the delayed cancel a seam to land in, exercising the post-activity
	// ctx.Err() re-check.
	s.env.OnActivity(s.acts.RecordDayDuration, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		}).Return(context.Canceled).Maybe()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.RegisterDelayedCallback(func() {
		s.env.CancelWorkflow()
	}, 50*time.Millisecond)

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{
		PoolID:  1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.True(t, s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	require.Error(t, err)
	assert.True(t, isCanceledMessage(err), "expected cancellation error, got: %v", err)
	assert.Contains(t, statuses, PoolStatusCancelled,
		"a cancel during RecordDayDuration must flip the pool to 'cancelled'")
}

// ============================================================================
// T1-E: pre-season StopAfter exits must NOT complete the never-started
// Season progress group (group 1). Before the fix, completePool called
// CompleteGroup(ctx, 1) → a bogus "Season complete" message + a read of
// an unstarted group timer.
// ============================================================================

func (s *SimPoolWorkflowTestSuite) TestStopAfterTeamName_DoesNotCompleteSeasonGroup() {
	s.assertSeasonGroupNotCompletedOnStop(StopAfterTeamName)
}

func (s *SimPoolWorkflowTestSuite) TestStopAfterDraft_DoesNotCompleteSeasonGroup() {
	s.assertSeasonGroupNotCompletedOnStop(StopAfterDraft)
}

func (s *SimPoolWorkflowTestSuite) assertSeasonGroupNotCompletedOnStop(stop StopAfter) {
	t := s.T()
	state := minState()
	state.PoolConfig.StopAfter = stop
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())

	val, err := s.env.QueryWorkflow(shared.ProgressReportQueryName)
	require.NoError(t, err)
	var report shared.ProgressReport
	require.NoError(t, val.Get(&report))
	require.Len(t, report.Groups, 2, "report has Draft + Season groups")

	season := report.Groups[1]
	assert.Empty(t, season.CompletedMsg,
		"pre-season stop must not emit a Season-complete message")
	assert.Zero(t, season.CompletedAt,
		"pre-season stop must not complete the never-started Season group")
}

// ============================================================================
// Low-4: QuerySummary must return a non-empty Status and a non-zero
// TotalLLMCostUsd after at least one phase has run.
// ============================================================================

// Execute a full workflow run then query the summary from the
// workflow result; the test environment's QueryWorkflow lets us
// interrogate the handler's closure state after the workflow exits.
// PickTeamName returns a non-zero cost so TotalLLMCostUsd must be > 0.
func (s *SimPoolWorkflowTestSuite) TestQuerySummary_PopulatesStatusAndCost() {
	t := s.T()
	state := minState()

	const teamNameCost = 0.05

	// Override PickTeamName to return a non-zero cost. Must be
	// registered BEFORE stubActivityResults because testify mock gives
	// priority to the first registered expectation.
	s.env.OnActivity(s.acts.PickTeamName, mock.Anything, mock.Anything).
		Return(PickTeamNameResult{Name: "Oilers", CostUsd: teamNameCost}, nil).
		Maybe()
	s.stubActivityResults(state)
	s.stubManageRosterPassThrough()

	s.env.ExecuteWorkflow(SimPoolWorkflow, SimPoolWorkflowInput{PoolID: 1})
	require.True(t, s.env.IsWorkflowCompleted())
	require.NoError(t, s.env.GetWorkflowError())

	val, err := s.env.QueryWorkflow(QuerySummary)
	require.NoError(t, err)
	var summary PoolSummary
	require.NoError(t, val.Get(&summary))

	assert.NotEmpty(t, summary.Status, "QuerySummary must populate Status")
	// Two agents × teamNameCost = 0.10 total at minimum.
	assert.Greater(t, summary.TotalLLMCostUsd, 0.0,
		"QuerySummary must reflect accumulated LLM cost")
}

// silence unused-import linter when sqlcdb is referenced only via the
// activities' input/result types.
var _ = sqlcdb.SimPool{}
var _ = pgtype.Date{}

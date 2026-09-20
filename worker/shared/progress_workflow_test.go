package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// testContext returns a context that's cancelled when the test ends.
// SaveActivityProgress's signature requires a context but doesn't use it
// for cancellation in its happy path; passing test-bound cancellation
// keeps any future timeout-aware refactor honest.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// newSaveActivityRedis returns an in-process Redis and a client bound to it
// for the SaveActivityProgress tests. Callers that need the failure path
// call srv.SetError.
func newSaveActivityRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return srv, client
}

// gobEncodeReport is a small helper for encoding a report to the bytes that
// the Load activity normally returns from Redis. Used by LoadReportTracker
// round-trip tests.
func gobEncodeReport(t *testing.T, report *ProgressReport) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(report))
	return buf.Bytes()
}

// errSimulatedRedis is a sentinel returned by the mocked Load
// activity to exercise the error-propagation branch in LoadReportTracker.
var errSimulatedRedis = errors.New("simulated redis error")

// ReportTrackerSuite tests the workflow-context-dependent ReportTracker
// methods: InitTracker, StartGroup, CompleteGroup, IncrementBar, GetElapsed,
// AddBarsForSeasons. Each method calls Save internally (which spins up a
// local activity), so tests register a no-op mock for that activity to
// avoid needing a real Redis connection.

type ReportTrackerSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ReportTrackerSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	// Save is invoked on every group start/complete/increment. Mock it as
	// a no-op so tests don't need a real Redis client. The Load mock is
	// only needed when a test exercises LoadReportTracker.
	s.env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *ReportTrackerSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestReportTrackerSuite(t *testing.T) {
	suite.Run(t, new(ReportTrackerSuite))
}

// freshReport returns a 2-group, 1-bar-each progress report shape used by
// most tests. Each test mutates its own copy via the tracker.
func freshReport() *ProgressReport {
	return &ProgressReport{
		Total: 5,
		Groups: []ProgressGroup{
			{Header: "First", Bars: []ProgressBar{{Total: 3}}},
			{Header: "Second", Bars: []ProgressBar{{Total: 2}}},
		},
	}
}

func (s *ReportTrackerSuite) TestInitTracker_ReturnsTrackerAndRegistersQuery() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		report := freshReport()
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		s.Require().NotNil(tracker)
		// The tracker wraps the same *ProgressReport pointer that was
		// passed in — mutations through the tracker are visible to the
		// caller's reference. Pin that contract.
		tracker.SetBarTotal(0, 0, 999)
		s.Equal(999, report.Groups[0].Bars[0].Total)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

// Save must stamp the report with the execution's run identity so the
// API's run-aware cleanup can keep this run's report. The test env leaves
// FirstRunID empty, so the stamp resolves to the current RunID here; pin
// that the activity receives progressRunStamp's answer and never "".
func (s *ReportTrackerSuite) TestSave_StampsRunIdentity() {
	env := s.NewTestWorkflowEnvironment()
	var wantRunID string
	env.OnActivity(((*ProgressActivities)(nil)).Save,
		mock.Anything, "default-test-workflow-id", mock.MatchedBy(func(runID string) bool {
			return runID != "" && runID == wantRunID
		}), mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		wantRunID = progressRunStamp(workflow.GetInfo(ctx))
		tracker, err := InitTracker(ctx, freshReport())
		s.Require().NoError(err)
		tracker.Save(ctx)
		return nil
	})
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

// FirstRunID identifies the whole ContinueAsNew chain and wins when set;
// the current RunID is only the fallback for environments that don't
// populate it.
func TestProgressRunStamp(t *testing.T) {
	t.Parallel()
	chained := &workflow.Info{
		WorkflowExecution: workflow.Execution{ID: "wf", RunID: "run-3"},
		FirstRunID:        "run-1",
	}
	assert.Equal(t, "run-1", progressRunStamp(chained), "continued run stamps with the chain's first run")

	unset := &workflow.Info{WorkflowExecution: workflow.Execution{ID: "wf", RunID: "run-1"}}
	assert.Equal(t, "run-1", progressRunStamp(unset), "falls back to the current run, never empty")
}

func (s *ReportTrackerSuite) TestStartGroup_SetsStartedAt() {
	report := freshReport()
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		s.Equal(int64(0), report.Groups[0].StartedAt, "StartedAt is zero before StartGroup")
		tracker.StartGroup(ctx, 0)
		s.NotZero(report.Groups[0].StartedAt, "StartGroup must set StartedAt to a non-zero unix-milli")
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestCompleteGroup_FillsBarsAndUpdatesCompleted() {
	report := freshReport()
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		tracker.CompleteGroup(ctx, 0, "done in 5s")
		// All bars in group 0 should be at full.
		s.Equal(report.Groups[0].Bars[0].Total, report.Groups[0].Bars[0].Current)
		s.NotZero(report.Groups[0].CompletedAt)
		s.Equal("done in 5s", report.Groups[0].CompletedMsg)
		// Completed is recomputed from all bars across all groups; group 1
		// hasn't been touched, so total Completed equals group 0's bar.
		s.Equal(3, report.Completed)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestIncrementBar_IncrementsCurrent() {
	report := freshReport()
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		tracker.IncrementBar(ctx, 0, 0)
		tracker.IncrementBar(ctx, 0, 0)
		tracker.IncrementBar(ctx, 1, 0)
		s.Equal(2, report.Groups[0].Bars[0].Current)
		s.Equal(1, report.Groups[1].Bars[0].Current)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestGetElapsed_EmptyBeforeStart() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, freshReport())
		s.Require().NoError(err)
		// StartedAt is zero, so GetElapsed returns "" (not "0ms").
		s.Equal("", tracker.GetElapsed(ctx, 0))
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestGetElapsed_NonEmptyAfterStart() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, freshReport())
		s.Require().NoError(err)
		tracker.StartGroup(ctx, 0)
		// In the test workflow environment, time is virtual but
		// workflow.Now still returns a real-shape value. The elapsed
		// reading is "0ms" or so since no time has been simulated, but
		// the format passes through FormatDuration so the prefix is
		// known.
		got := tracker.GetElapsed(ctx, 0)
		s.NotEmpty(got, "GetElapsed must return a non-empty string after StartGroup")
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestAddBarsForSeasons_BuildsBarPerSeason() {
	report := &ProgressReport{
		Groups: []ProgressGroup{{Header: "Fetching seasons...", Bars: []ProgressBar{}}},
	}
	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022)},
		{ID: nhl.NewSeason(2023)},
		{ID: nhl.NewSeason(2024)},
	}
	const fixedCount = 7
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		counter := func(_ workflow.Context, _ nhl.SeasonInfo) (int, error) { return fixedCount, nil }
		keyFn := func(year int) string { return "src-" + nhl.NewSeason(year).String() }

		barIdx, err := tracker.AddBarsForSeasons(ctx, 0, seasons, counter, keyFn)
		s.Require().NoError(err)

		s.Require().Len(report.Groups[0].Bars, len(seasons))
		// Each season produces one bar with Total=fixedCount and the
		// label set to season.Label() (e.g., "2022-23").
		for i, season := range seasons {
			s.Equal(fixedCount, report.Groups[0].Bars[i].Total)
			s.Equal(season.Label(), report.Groups[0].Bars[i].Label)
			s.Equal(keyFn(season.ID.StartYear()), report.Groups[0].Bars[i].ProgressSourceKey)
		}
		// barIdx maps startYear -> position in bars slice.
		s.Equal(0, barIdx[2022])
		s.Equal(1, barIdx[2023])
		s.Equal(2, barIdx[2024])
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *ReportTrackerSuite) TestAddBarsForSeasons_NilKeyFnSkipsSourceKey() {
	report := &ProgressReport{
		Groups: []ProgressGroup{{Header: "x", Bars: []ProgressBar{}}},
	}
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		counter := func(_ workflow.Context, _ nhl.SeasonInfo) (int, error) { return 1, nil }

		_, err = tracker.AddBarsForSeasons(ctx, 0, []nhl.SeasonInfo{{ID: nhl.NewSeason(2024)}}, counter, nil)
		s.Require().NoError(err)
		s.Empty(report.Groups[0].Bars[0].ProgressSourceKey, "nil keyFn must leave ProgressSourceKey empty")
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

// LoadReportTrackerSuite is its own suite because Load is mocked at
// SetupTest time with a specific return value per test, which doesn't
// fit the shared ReportTrackerSuite's no-op Save mock.

type LoadReportTrackerSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestLoadReportTrackerSuite(t *testing.T) {
	suite.Run(t, new(LoadReportTrackerSuite))
}

func (s *LoadReportTrackerSuite) TestNoReportInRedis_ReturnsError() {
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(((*ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return([]byte(nil), nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := LoadReportTracker(ctx)
		// nil bytes => "no progress report found in redis" error.
		s.Require().Error(err)
		s.Require().Nil(tracker)
		return nil
	})
	s.NoError(env.GetWorkflowError())
}

func (s *LoadReportTrackerSuite) TestRoundTrip() {
	// Encode a known report, return its bytes from Load, and verify the
	// returned tracker wraps the decoded report. This pins the gob
	// round-trip contract used after ContinueAsNew.
	original := freshReport()
	original.Completed = 2
	encoded := gobEncodeReport(s.T(), original)

	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(((*ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(encoded, nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := LoadReportTracker(ctx)
		s.Require().NoError(err)
		s.Require().NotNil(tracker)
		// Mutate via tracker; the report inside must be the decoded one.
		tracker.SetBarLabel(0, 0, "after-load")
		return nil
	})
	s.NoError(env.GetWorkflowError())
}

// --- SaveActivityProgress (regular activity, no testsuite needed) ---

// SaveActivityProgress encodes a transient single-bar report and pushes it
// to Redis. It's invoked from inside long-running activities to surface
// progress to the resolver between activity completions.

func TestSaveActivityProgress_Success(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	_, client := newSaveActivityRedis(t)

	err := SaveActivityProgress(ctx, client, "wf-1", 1700000000000, 5, 10)
	require.NoError(t, err)

	// Round-trips through the cache layer as a decodable single-bar report.
	data, err := cache.LoadProgressReport(ctx, client, "wf-1")
	require.NoError(t, err)
	var got ProgressReport
	require.NoError(t, gob.NewDecoder(bytes.NewReader(data)).Decode(&got))
	assert.Equal(t, 5, got.Completed)
	assert.Equal(t, 10, got.Total)
	require.Len(t, got.Groups, 1)
	assert.Equal(t, int64(1700000000000), got.Groups[0].StartedAt)
}

func TestSaveActivityProgress_RedisError(t *testing.T) {
	t.Parallel()
	srv, client := newSaveActivityRedis(t)
	srv.SetError("connection lost")

	err := SaveActivityProgress(testContext(t), client, "wf-2", 1700000000000, 0, 5)

	require.Error(t, err)
}

func (s *LoadReportTrackerSuite) TestActivityError_PropagatesError() {
	env := s.NewTestWorkflowEnvironment()
	env.OnActivity(((*ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return([]byte(nil), errSimulatedRedis)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := LoadReportTracker(ctx)
		s.Require().Error(err)
		return nil
	})
	s.NoError(env.GetWorkflowError())
}

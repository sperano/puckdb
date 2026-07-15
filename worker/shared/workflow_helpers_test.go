package shared

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestViperIntOrDefault(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	const testKey = "test.viper.int.or.default"

	t.Run("viper returns zero so fallback is used", func(t *testing.T) {
		result := ViperIntOrDefault(testKey+"_unset", 99)
		assert.Equal(t, 99, result)
	})

	t.Run("viper returns positive value so viper value is used", func(t *testing.T) {
		key := testKey + "_positive"
		viper.Set(key, 42)
		t.Cleanup(func() { viper.Set(key, 0) })

		result := ViperIntOrDefault(key, 99)
		assert.Equal(t, 42, result)
	})

	t.Run("viper returns negative value so fallback is used", func(t *testing.T) {
		key := testKey + "_negative"
		viper.Set(key, -5)
		t.Cleanup(func() { viper.Set(key, 0) })

		result := ViperIntOrDefault(key, 99)
		assert.Equal(t, 99, result)
	})
}

// EffectiveEndDateSuite tests EffectiveEndDate using the Temporal test environment.
type EffectiveEndDateSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *EffectiveEndDateSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *EffectiveEndDateSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestEffectiveEndDateSuite(t *testing.T) {
	suite.Run(t, new(EffectiveEndDateSuite))
}

func (s *EffectiveEndDateSuite) TestFutureEndDate_ReturnsYesterday() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		now := workflow.Now(ctx)
		futureDate := now.Add(30 * 24 * time.Hour)
		result := EffectiveEndDate(ctx, futureDate)
		yesterday := now.AddDate(0, 0, -1)

		// Result should be approximately yesterday (within a few seconds of test execution).
		diff := result.Sub(yesterday).Abs()
		s.Less(diff, time.Second, "result should be yesterday")
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *EffectiveEndDateSuite) TestPastEndDate_ReturnsUnchanged() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		pastDate := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
		result := EffectiveEndDate(ctx, pastDate)
		s.Equal(pastDate, result)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *EffectiveEndDateSuite) TestToday_CappedToYesterday() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		now := workflow.Now(ctx)
		result := EffectiveEndDate(ctx, now)
		yesterday := now.AddDate(0, 0, -1)

		diff := result.Sub(yesterday).Abs()
		s.Less(diff, time.Second, "today should be capped to yesterday")
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

// CountDaysInSeasonSuite tests CountDaysInSeason using the Temporal test environment.
type CountDaysInSeasonSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *CountDaysInSeasonSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *CountDaysInSeasonSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestCountDaysInSeasonSuite(t *testing.T) {
	suite.Run(t, new(CountDaysInSeasonSuite))
}

func (s *CountDaysInSeasonSuite) TestSeasonFullyInPast_ReturnsCorrectDayCount() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		// Build a SeasonInfo for a 10-day window entirely in the past.
		start := nhl.NewDate(2020, time.January, 1)
		end := nhl.NewDate(2020, time.January, 10)
		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2019),
			StandingsStart: start,
			StandingsEnd:   end,
		}

		count, err := CountDaysInSeason(ctx, season)
		s.NoError(err)
		// CountDays is inclusive: Jan 1 through Jan 10 = 10 days.
		s.Equal(10, count)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *CountDaysInSeasonSuite) TestSeasonWithFutureEndDate_CappedAtYesterday() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		now := workflow.Now(ctx)
		// Start three days ago, end two days from now.
		startTime := now.AddDate(0, 0, -3)
		endTime := now.AddDate(0, 0, 2)

		start := nhl.DateFromTime(startTime)
		end := nhl.DateFromTime(endTime)

		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2025),
			StandingsStart: start,
			StandingsEnd:   end,
		}

		count, err := CountDaysInSeason(ctx, season)
		s.NoError(err)
		// The end is capped to ~yesterday, so count should be roughly 3
		// (3 days ago + 2 days ago + yesterday). Allow ±1 for clock boundaries.
		s.GreaterOrEqual(count, 2)
		s.LessOrEqual(count, 4)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func (s *CountDaysInSeasonSuite) TestSingleDaySeason_ReturnsOne() {
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		d := nhl.NewDate(2019, time.October, 1)
		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2019),
			StandingsStart: d,
			StandingsEnd:   d,
		}

		count, err := CountDaysInSeason(ctx, season)
		s.NoError(err)
		s.Equal(1, count)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

func TestGetDayConcurrency(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("returns default when viper value is zero", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, 0)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, config.DefaultDayConcurrency, result)
	})

	t.Run("returns configured value when viper value is positive", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, 7)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, 7, result)
	})

	t.Run("returns default when viper value is negative", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, -3)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, config.DefaultDayConcurrency, result)
	})
}

func TestDefaultActivityOptions(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("uses defaults when viper is not configured", func(t *testing.T) {
		opts := DefaultActivityOptions()

		expectedStartToClose := time.Duration(config.DefaultActivityStartToCloseTimeout) * time.Minute
		expectedHeartbeat := time.Duration(config.DefaultActivityHeartbeatTimeout) * time.Second
		expectedInitial := time.Duration(config.DefaultTemporalRetryInitialInterval) * time.Second
		expectedMax := time.Duration(config.DefaultTemporalRetryMaxInterval) * time.Second

		assert.Equal(t, expectedStartToClose, opts.StartToCloseTimeout)
		assert.Equal(t, expectedHeartbeat, opts.HeartbeatTimeout)
		require.NotNil(t, opts.RetryPolicy)
		assert.Equal(t, expectedInitial, opts.RetryPolicy.InitialInterval)
		assert.Equal(t, expectedMax, opts.RetryPolicy.MaximumInterval)
		assert.Equal(t, int32(config.DefaultTemporalRetryMaxAttempts), opts.RetryPolicy.MaximumAttempts)
		assert.Equal(t, config.DefaultBackoffCoefficient, opts.RetryPolicy.BackoffCoefficient)
	})

	t.Run("uses viper values when configured", func(t *testing.T) {
		viper.Set(config.FlagActivityStartToCloseTimeout, 30)
		viper.Set(config.FlagActivityHeartbeatTimeout, 120)
		t.Cleanup(func() {
			viper.Set(config.FlagActivityStartToCloseTimeout, 0)
			viper.Set(config.FlagActivityHeartbeatTimeout, 0)
		})

		opts := DefaultActivityOptions()

		assert.Equal(t, 30*time.Minute, opts.StartToCloseTimeout)
		assert.Equal(t, 120*time.Second, opts.HeartbeatTimeout)
	})
}

// --- IsCurrentSeason ---

// IsCurrentSeason returns true iff the start year matches nhl.Current().
// The "current" branch is exercised dynamically (the value derived from
// nhl.Current() at test time keeps this correct as the calendar advances).
// The "not current" branch uses year 0 — guaranteed never to be the
// current NHL season, regardless of when the test runs.
func TestIsCurrentSeason(t *testing.T) {
	t.Parallel()

	assert.True(t, IsCurrentSeason(nhl.Current().StartYear()))
	assert.False(t, IsCurrentSeason(0))
}

// --- FetchDayActivityOptions ---

// FetchDayActivityOptions returns DefaultActivityOptions with a longer
// StartToCloseTimeout suited for FetchDayActivity (which downloads many
// files with rate-limit pauses). Verify the timeout differs from the
// default and respects viper overrides.
func TestFetchDayActivityOptions_Default(t *testing.T) {
	// Not parallel: viper.Set is global state.
	viper.Set(config.FlagFetchDayActivityTimeout, 0)
	t.Cleanup(func() { viper.Set(config.FlagFetchDayActivityTimeout, 0) })

	opts := FetchDayActivityOptions()
	expected := time.Duration(config.DefaultFetchDayActivityTimeout) * time.Minute

	assert.Equal(t, expected, opts.StartToCloseTimeout)
	require.NotNil(t, opts.RetryPolicy, "RetryPolicy must be inherited from DefaultActivityOptions")
}

func TestFetchDayActivityOptions_ViperOverride(t *testing.T) {
	viper.Set(config.FlagFetchDayActivityTimeout, 45)
	t.Cleanup(func() { viper.Set(config.FlagFetchDayActivityTimeout, 0) })

	opts := FetchDayActivityOptions()
	assert.Equal(t, 45*time.Minute, opts.StartToCloseTimeout)
}

// --- WithChildOptions (workflow-context dependent) ---

type WithChildOptionsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *WithChildOptionsSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *WithChildOptionsSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestWithChildOptionsSuite(t *testing.T) {
	suite.Run(t, new(WithChildOptionsSuite))
}

func (s *WithChildOptionsSuite) TestReturnsNonNilContext() {
	// WithChildOptions injects ChildWorkflowOptions into ctx via
	// workflow.WithChildOptions. We can't directly read those options
	// back without invoking a child workflow, but a non-nil return from
	// inside a workflow is sufficient to exercise the code path; the
	// option values themselves are constants from viper/config and
	// covered by TestDefaultActivityOptions et al.
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		childCtx := WithChildOptions(ctx, "test-child-id")
		s.NotNil(childCtx)
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
}

package workflow

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

type FetchAssetsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *FetchAssetsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	// Register the parent and every child workflow it can dispatch. Without
	// these, ExecuteChildWorkflow fails to resolve the workflow type at replay.
	s.env.RegisterWorkflow(FetchAssetsWorkflow)
	for _, e := range parentAssetClasses {
		s.env.RegisterWorkflow(e.Workflow)
	}

	// Register one no-op activity per class under each count activity's
	// registered name. The bodies never run — OnActivity-by-name intercepts
	// every call before dispatch — but registration must succeed so the
	// parent's ExecuteActivity-by-name resolves.
	noop := func(context.Context) (int, error) { return 0, nil }
	for _, e := range parentAssetClasses {
		s.env.RegisterActivityWithOptions(noop, activity.RegisterOptions{Name: e.CountName})
	}
}

func (s *FetchAssetsWorkflowTestSuite) AfterTest(_, _ string) {
	s.env.AssertExpectations(s.T())
}

func TestFetchAssetsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(FetchAssetsWorkflowTestSuite))
}

// mockAllCounts mocks every count activity to return the same row count. Used
// when the test doesn't care about per-class sizing.
func (s *FetchAssetsWorkflowTestSuite) mockAllCounts(rows int) {
	for _, e := range parentAssetClasses {
		s.env.OnActivity(e.CountName, mock.Anything).Return(rows, nil)
	}
}

// mockAllChildren mocks every entry-point workflow to return the same counts.
// Useful when the test cares about parent aggregation, not per-class behavior.
func (s *FetchAssetsWorkflowTestSuite) mockAllChildren(counts core.OriginCounts) {
	for _, e := range parentAssetClasses {
		s.env.OnWorkflow(e.Workflow, mock.Anything, mock.Anything).Return(counts, nil)
	}
}

// TestFetchAssetsWorkflow_AggregatesCounts verifies the parent sums OriginCounts
// across all class children.
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_AggregatesCounts() {
	s.mockAllCounts(50)
	// Each child reports 7 NHL CDN downloads + 3 file-system hits → N*7
	// remote, N*3 file-system, after aggregation, where N = number of classes.
	s.mockAllChildren(core.OriginCounts{
		core.OriginRemoteNHLCDN: 7,
		core.OriginFileSystem:   3,
	})

	s.env.ExecuteWorkflow(FetchAssetsWorkflow, &FetchAssetsInput{})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	n := len(parentAssetClasses)
	s.Equal(n*7, counts[core.OriginRemoteNHLCDN])
	s.Equal(n*3, counts[core.OriginFileSystem])
	s.Equal(n*10, counts.Total())
}

// TestFetchAssetsWorkflow_NilInput verifies a nil pointer input is treated as
// an empty input (RefreshCurrent=false, default concurrency).
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_NilInput() {
	s.mockAllCounts(0)
	s.mockAllChildren(core.OriginCounts{})

	s.env.ExecuteWorkflow(FetchAssetsWorkflow, (*FetchAssetsInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestFetchAssetsWorkflow_RefreshCurrentPropagates verifies the parent forwards
// the RefreshCurrent flag to every child workflow's FetchAssetsInput.
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_RefreshCurrentPropagates() {
	s.mockAllCounts(0)

	// Capture the input each child receives. Indexed by class slug.
	captured := make(map[string]FetchAssetsInput)
	for _, e := range parentAssetClasses {
		s.env.OnWorkflow(e.Workflow,
			mock.Anything,
			mock.MatchedBy(func(in FetchAssetsInput) bool {
				captured[e.Slug] = in
				return true
			}),
		).Return(core.OriginCounts{}, nil)
	}

	refresh := true
	s.env.ExecuteWorkflow(FetchAssetsWorkflow, &FetchAssetsInput{RefreshCurrent: &refresh})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Every child must have seen RefreshCurrent=true.
	s.Len(captured, len(parentAssetClasses))
	for slug, in := range captured {
		s.Require().NotNil(in.RefreshCurrent, "child %s saw nil RefreshCurrent", slug)
		s.True(*in.RefreshCurrent, "child %s saw RefreshCurrent=false", slug)
	}
}

// TestFetchAssetsWorkflow_CountErrorFailsParent verifies that an error from any
// count activity aborts the parent before any children spawn.
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_CountErrorFailsParent() {
	// Eight counts succeed; one fails.
	for i, e := range parentAssetClasses {
		if i == 0 {
			s.env.OnActivity(e.CountName, mock.Anything).Return(0, errTest("db unavailable"))
			continue
		}
		s.env.OnActivity(e.CountName, mock.Anything).Return(0, nil)
	}
	// No children should be invoked. Don't register child mocks; AfterTest's
	// AssertExpectations will catch any unexpected workflow dispatches.

	s.env.ExecuteWorkflow(FetchAssetsWorkflow, &FetchAssetsInput{})

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// TestLoadFetchAssetsConfig verifies the parent's config snapshot resolves
// BatchSize (no override at the parent level) and MaxClassConcurrency
// (overridable) from viper and defaults.
func TestLoadFetchAssetsConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("defaults when nothing configured", func(t *testing.T) {
		got := loadFetchAssetsConfig(nil, nil)
		require.Equal(t, config.DefaultAssetBatchSize, got.BatchSize)
		require.Equal(t, config.DefaultMaxAssetClassConcurrency, got.MaxClassConcurrency)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagAssetBatchSize, 250)
		setViperInt(t, config.FlagMaxAssetClassConcurrency, 5)

		got := loadFetchAssetsConfig(nil, nil)
		require.Equal(t, 250, got.BatchSize)
		require.Equal(t, 5, got.MaxClassConcurrency)
	})

	t.Run("MaxClassConcurrency override wins", func(t *testing.T) {
		setViperInt(t, config.FlagMaxAssetClassConcurrency, 5)

		got := loadFetchAssetsConfig(nil, intPtr(2))
		require.Equal(t, 2, got.MaxClassConcurrency)
	})
}

// TestFetchAssetsWorkflow_ForwardsChildOverrides verifies the parent forwards
// ClassConcurrency and its own snapshotted batch size to every class child.
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_ForwardsChildOverrides() {
	s.mockAllCounts(0)

	captured := make(map[string]FetchAssetsInput)
	for _, e := range parentAssetClasses {
		s.env.OnWorkflow(e.Workflow,
			mock.Anything,
			mock.MatchedBy(func(in FetchAssetsInput) bool {
				captured[e.Slug] = in
				return true
			}),
		).Return(core.OriginCounts{}, nil)
	}

	setViperInt(s.T(), config.FlagAssetBatchSize, 250)
	classConcurrency := 1

	s.env.ExecuteWorkflow(FetchAssetsWorkflow, &FetchAssetsInput{ClassConcurrency: &classConcurrency})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Len(captured, len(parentAssetClasses))
	for slug, in := range captured {
		s.Require().NotNil(in.AssetBatchSize, "child %s saw nil AssetBatchSize", slug)
		s.Equal(250, *in.AssetBatchSize, "child %s saw the wrong AssetBatchSize", slug)
		s.Require().NotNil(in.ClassConcurrency, "child %s saw nil ClassConcurrency", slug)
		s.Equal(1, *in.ClassConcurrency, "child %s saw the wrong ClassConcurrency", slug)
	}
}

// TestBatchesForRows verifies the row-to-batch conversion used to size
// bar.Total. Parent and child track in batches; the count activity returns
// rows.
func TestBatchesForRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rows      int
		batchSize int
		want      int
	}{
		{"exact multiple", 300, 100, 3},
		{"remainder", 250, 100, 3},
		{"under one batch", 50, 100, 1},
		{"empty", 0, 100, 0},
		{"single row", 1, 100, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := batchesForRows(tc.rows, tc.batchSize)
			if got != tc.want {
				t.Errorf("batchesForRows(%d, %d) = %d, want %d", tc.rows, tc.batchSize, got, tc.want)
			}
		})
	}
}

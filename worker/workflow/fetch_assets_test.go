package workflow

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/core"
	"github.com/stretchr/testify/mock"
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
// across all nine children.
func (s *FetchAssetsWorkflowTestSuite) TestFetchAssetsWorkflow_AggregatesCounts() {
	s.mockAllCounts(50)
	// Each child reports 7 NHL CDN downloads + 3 file-system hits → 9*7 = 63
	// remote, 9*3 = 27 file-system, after aggregation.
	s.mockAllChildren(core.OriginCounts{
		core.OriginRemoteNHLCDN: 7,
		core.OriginFileSystem:   3,
	})

	s.env.ExecuteWorkflow(FetchAssetsWorkflow, &FetchAssetsInput{})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	s.Equal(63, counts[core.OriginRemoteNHLCDN])
	s.Equal(27, counts[core.OriginFileSystem])
	s.Equal(90, counts.Total())
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

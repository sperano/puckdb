package shared

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	// barRangesFailingItem is the work item barRangeActivity fails.
	barRangesFailingItem = 1
	// barRangesResizedTotal is what the handler resizes the first bar to.
	barRangesResizedTotal = 9
	barRangesTimeout      = time.Minute
)

// barRangeActivity succeeds for every item except barRangesFailingItem.
func barRangeActivity(_ context.Context, index int) (int, error) {
	if index == barRangesFailingItem {
		return 0, temporal.NewNonRetryableApplicationError("item failed", "test", nil)
	}
	return index, nil
}

type BarRangesSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *BarRangesSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.RegisterActivity(barRangeActivity)
}

func TestBarRangesSuite(t *testing.T) {
	suite.Run(t, new(BarRangesSuite))
}

// threeBarReport is one group of three bars, totals 2, 3, 4.
func threeBarReport() *ProgressReport {
	return &ProgressReport{Total: 9, Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: 2}, {Total: 3}, {Total: 4}}}}}
}

func (s *BarRangesSuite) TestIncrementBarsAndTotals() {
	report := threeBarReport()
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		tracker.IncrementBars(ctx, 0, []int{0, 2})
		s.Equal([]int{2, 3, 4}, tracker.GroupBarTotals(0))
		s.Equal(ProgressBar{Current: 1, Total: 4}, tracker.Bar(0, 2))
		return nil
	})
	s.NoError(s.env.GetWorkflowError())
	s.Equal([]int{1, 0, 1}, []int{report.Groups[0].Bars[0].Current, report.Groups[0].Bars[1].Current,
		report.Groups[0].Bars[2].Current})
}

// Item 0 owns bars 0-1 and succeeds; item 1 owns bar 2 and fails. Item 0's
// handler resizes bar 0 before completion, so it completes at the new Total.
func (s *BarRangesSuite) TestRunWorkerPoolBarRangesSuccessful() {
	report := threeBarReport()
	ranges := []BarRange{{Start: 0, Count: 2}, {Start: 2, Count: 1}}
	s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: barRangesTimeout})
		tracker, err := InitTracker(ctx, report)
		s.Require().NoError(err)
		return tracker.RunWorkerPoolBarRangesSuccessful(ctx, 0, ranges, len(ranges),
			func(ctx workflow.Context, index int) workflow.Future {
				return workflow.ExecuteActivity(ctx, barRangeActivity, index)
			},
			func(ctx workflow.Context, index int, future workflow.Future) error {
				if err := future.Get(ctx, nil); err != nil {
					return err
				}
				tracker.SetBarTotal(0, 0, barRangesResizedTotal)
				tracker.RecalcTotal()
				return nil
			})
	})
	s.Error(s.env.GetWorkflowError())
	bars := report.Groups[0].Bars
	s.Equal(ProgressBar{Current: barRangesResizedTotal, Total: barRangesResizedTotal, Started: true}, bars[0])
	s.Equal(ProgressBar{Current: 3, Total: 3, Started: true}, bars[1])
	s.Equal(ProgressBar{Current: 0, Total: 4, Started: true}, bars[2], "a failed item's bars stay incomplete")
	s.Equal(barRangesResizedTotal+3+4, report.Total)
}

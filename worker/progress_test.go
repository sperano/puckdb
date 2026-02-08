package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestNewProgressTracker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		total int
	}{
		{"zero total", 0},
		{"small total", 10},
		{"large total", 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTracker(tt.total)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.total, tracker.progress.Total)
			assert.Equal(t, 0, tracker.progress.Completed)
		})
	}
}

func TestNewProgressTrackerWithOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		batchSize  int
		offset     int
		grandTotal int
	}{
		{"first batch", 100, 0, 500},
		{"middle batch", 100, 200, 500},
		{"last batch", 100, 400, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTrackerWithOffset(tt.batchSize, tt.offset, tt.grandTotal)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.grandTotal, tracker.progress.Total)
			assert.Equal(t, tt.offset, tracker.progress.Completed)
		})
	}
}

func TestProgressTracker_Increment(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Equal(t, 0, tracker.progress.Completed)

	tracker.Increment()
	assert.Equal(t, 1, tracker.progress.Completed)

	tracker.Increment()
	tracker.Increment()
	assert.Equal(t, 3, tracker.progress.Completed)
}

func TestProgressTracker_IncrementItem(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 100},
		{ID: 2, Description: "Phase 2", Total: 200},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 0, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 1
	tracker.IncrementItem(1)
	assert.Equal(t, 1, tracker.progress.Completed)
	assert.Equal(t, 1, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 2
	tracker.IncrementItem(2)
	tracker.IncrementItem(2)
	assert.Equal(t, 3, tracker.progress.Completed)
	assert.Equal(t, 1, tracker.progress.Items[0].Completed)
	assert.Equal(t, 2, tracker.progress.Items[1].Completed)

	// Increment non-existent phase (should still increment total)
	tracker.IncrementItem(999)
	assert.Equal(t, 4, tracker.progress.Completed)
}

func TestProgressTracker_IncrementItemBy(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 1000},
		{ID: 2, Description: "Phase 2", Total: 5000},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 6000, tracker.progress.Total)

	// Increment phase 1 by batch size (50)
	tracker.IncrementItemBy(1, 50)
	assert.Equal(t, 50, tracker.progress.Completed)
	assert.Equal(t, 50, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 1 by another batch
	tracker.IncrementItemBy(1, 50)
	assert.Equal(t, 100, tracker.progress.Completed)
	assert.Equal(t, 100, tracker.progress.Items[0].Completed)

	// Increment phase 2 by larger batch
	tracker.IncrementItemBy(2, 500)
	assert.Equal(t, 600, tracker.progress.Completed)
	assert.Equal(t, 100, tracker.progress.Items[0].Completed)
	assert.Equal(t, 500, tracker.progress.Items[1].Completed)

	// Increment non-existent phase (should still increment total)
	tracker.IncrementItemBy(999, 25)
	assert.Equal(t, 625, tracker.progress.Completed)
}

func TestProgressTracker_IncrementItemBy_LastBatchSmaller(t *testing.T) {
	t.Parallel()

	// Simulate a scenario where total items = 182, batch size = 50
	// Last batch should be 182 - (3 * 50) = 32 items
	phases := []PhaseInfo{
		{ID: 1, Description: "Import players", Total: 182},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	batchSize := 50
	totalItems := 182

	// Process 3 full batches
	for i := 0; i < 3; i++ {
		tracker.IncrementItemBy(1, batchSize)
	}
	assert.Equal(t, 150, tracker.progress.Completed)
	assert.Equal(t, 150, tracker.progress.Items[0].Completed)

	// Process last batch (smaller)
	lastBatchSize := totalItems - 3*batchSize // 32
	tracker.IncrementItemBy(1, lastBatchSize)
	assert.Equal(t, 182, tracker.progress.Completed)
	assert.Equal(t, 182, tracker.progress.Items[0].Completed)
}

func TestProgressTracker_SetItemTotal(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 0}, // Unknown initially
		{ID: 2, Description: "Phase 2", Total: 100},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 100, tracker.progress.Total)
	assert.Equal(t, 0, tracker.progress.Items[0].Total)

	// Set phase 1 total after discovering it
	tracker.SetItemTotal(1, 9100)
	assert.Equal(t, 9200, tracker.progress.Total) // 9100 + 100
	assert.Equal(t, 9100, tracker.progress.Items[0].Total)

	// Update phase 1 total
	tracker.SetItemTotal(1, 9050)
	assert.Equal(t, 9150, tracker.progress.Total) // 9050 + 100
	assert.Equal(t, 9050, tracker.progress.Items[0].Total)
}

// ProgressTrackerTimestampSuite tests MarkItemStarted and MarkItemCompleted which require workflow.Context
type ProgressTrackerTimestampSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ProgressTrackerTimestampSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *ProgressTrackerTimestampSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestProgressTrackerTimestampSuite(t *testing.T) {
	suite.Run(t, new(ProgressTrackerTimestampSuite))
}

func (s *ProgressTrackerTimestampSuite) TestMarkItemStarted() {
	testWorkflow := func(ctx workflow.Context) error {
		phases := []PhaseInfo{
			{ID: 1, Description: "Phase 1", Total: 1},
			{ID: 2, Description: "Phase 2", Total: 1},
			{ID: 5, Description: "Phase 5", Total: 0},
		}
		tracker := NewProgressTrackerWithPhases(phases)

		// Verify initial state
		s.False(tracker.progress.Items[0].Started)
		s.False(tracker.progress.Items[1].Started)
		s.False(tracker.progress.Items[2].Started)

		// Mark phase 5 as started
		tracker.MarkItemStarted(ctx, 5)
		s.False(tracker.progress.Items[0].Started)
		s.False(tracker.progress.Items[1].Started)
		s.True(tracker.progress.Items[2].Started, "Phase 5 should be marked as started")
		s.NotEmpty(tracker.progress.Items[2].StartedAt)

		// Verify IsItemStarted helper
		s.False(tracker.IsItemStarted(1))
		s.False(tracker.IsItemStarted(2))
		s.True(tracker.IsItemStarted(5))
		s.False(tracker.IsItemStarted(999))

		// Mark phase 1 as started
		tracker.MarkItemStarted(ctx, 1)
		s.True(tracker.progress.Items[0].Started)

		// Mark non-existent phase (should not panic)
		tracker.MarkItemStarted(ctx, 999)

		return nil
	}

	s.env.ExecuteWorkflow(testWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ProgressTrackerTimestampSuite) TestMarkItemCompleted() {
	testWorkflow := func(ctx workflow.Context) error {
		phases := []PhaseInfo{
			{ID: 1, Description: "Phase 1", Total: 1},
		}
		tracker := NewProgressTrackerWithPhases(phases)

		// Mark started first
		tracker.MarkItemStarted(ctx, 1)
		s.NotEmpty(tracker.progress.Items[0].StartedAt)
		s.Empty(tracker.progress.Items[0].CompletedAt)

		// Mark completed
		tracker.MarkItemCompleted(ctx, 1)
		s.NotEmpty(tracker.progress.Items[0].CompletedAt)

		// Non-existent phase (should not panic)
		tracker.MarkItemCompleted(ctx, 999)

		return nil
	}

	s.env.ExecuteWorkflow(testWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestProgressTracker_SetMessage(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Empty(t, tracker.progress.Message)

	tracker.SetMessage("Processing items")
	assert.Equal(t, "Processing items", tracker.progress.Message)

	tracker.SetMessage("Done")
	assert.Equal(t, "Done", tracker.progress.Message)
}

func TestNewProgressTrackerWithPhases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		phases         []PhaseInfo
		expectedTotal  int
		expectedItems  int
		checkItemIndex bool
	}{
		{
			name:           "empty phases",
			phases:         []PhaseInfo{},
			expectedTotal:  0,
			expectedItems:  0,
			checkItemIndex: false,
		},
		{
			name: "single phase",
			phases: []PhaseInfo{
				{ID: 1, Description: "Phase 1", Total: 100},
			},
			expectedTotal:  100,
			expectedItems:  1,
			checkItemIndex: true,
		},
		{
			name: "multiple phases with different totals",
			phases: []PhaseInfo{
				{ID: 1, Description: "Phase 1", Total: 100},
				{ID: 2, Description: "Phase 2", Total: 200},
				{ID: 3, Description: "Phase 3", Total: 50},
			},
			expectedTotal:  350,
			expectedItems:  3,
			checkItemIndex: true,
		},
		{
			name: "phases with zero total",
			phases: []PhaseInfo{
				{ID: 1, Description: "Phase 1", Total: 0},
				{ID: 2, Description: "Phase 2", Total: 100},
			},
			expectedTotal:  100,
			expectedItems:  2,
			checkItemIndex: true,
		},
		{
			name: "non-sequential phase IDs",
			phases: []PhaseInfo{
				{ID: 5, Description: "Phase 5", Total: 10},
				{ID: 10, Description: "Phase 10", Total: 20},
				{ID: 15, Description: "Phase 15", Total: 30},
			},
			expectedTotal:  60,
			expectedItems:  3,
			checkItemIndex: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTrackerWithPhases(tt.phases)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.expectedTotal, tracker.progress.Total)
			assert.Equal(t, 0, tracker.progress.Completed)
			assert.Equal(t, tt.expectedItems, len(tracker.progress.Items))

			if tt.checkItemIndex {
				for i, phase := range tt.phases {
					assert.Equal(t, i, tracker.itemIndex[phase.ID], "itemIndex should map phase ID %d to index %d", phase.ID, i)
					assert.Equal(t, phase.ID, tracker.progress.Items[i].ID)
					assert.Equal(t, phase.Description, tracker.progress.Items[i].Description)
					assert.Equal(t, phase.Total, tracker.progress.Items[i].Total)
					assert.Equal(t, 0, tracker.progress.Items[i].Completed)
				}
			}
		})
	}
}

func TestNewProgressTrackerWithSeasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		seasons        []SeasonInfo
		expectedItems  int
		checkItemIndex bool
	}{
		{
			name:           "empty seasons",
			seasons:        []SeasonInfo{},
			expectedItems:  0,
			checkItemIndex: false,
		},
		{
			name: "single season",
			seasons: []SeasonInfo{
				{StartYear: 2023},
			},
			expectedItems:  1,
			checkItemIndex: true,
		},
		{
			name: "multiple seasons",
			seasons: []SeasonInfo{
				{StartYear: 2021},
				{StartYear: 2022},
				{StartYear: 2023},
			},
			expectedItems:  3,
			checkItemIndex: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTrackerWithSeasons(tt.seasons)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.expectedItems, len(tracker.progress.Items))
			assert.Equal(t, 0, tracker.progress.Completed)

			if tt.checkItemIndex {
				for i, season := range tt.seasons {
					assert.Equal(t, i, tracker.itemIndex[season.StartYear], "itemIndex should map season %d to index %d", season.StartYear, i)
					assert.Equal(t, season.StartYear, tracker.progress.Items[i].ID)
					assert.Equal(t, season.Label(), tracker.progress.Items[i].Description)
					assert.Greater(t, tracker.progress.Items[i].Total, 0, "season total should be calculated")
					assert.Equal(t, 0, tracker.progress.Items[i].Completed)
				}
			}
		})
	}
}

func TestProgressTracker_InitializeWithPhases(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Equal(t, 10, tracker.progress.Total)
	assert.Equal(t, 0, len(tracker.progress.Items))

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 100},
		{ID: 2, Description: "Phase 2", Total: 200},
	}

	tracker.InitializeWithPhases(phases)

	assert.Equal(t, 300, tracker.progress.Total)
	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 2, len(tracker.progress.Items))

	assert.Equal(t, 0, tracker.itemIndex[1])
	assert.Equal(t, 1, tracker.itemIndex[2])

	assert.Equal(t, 1, tracker.progress.Items[0].ID)
	assert.Equal(t, "Phase 1", tracker.progress.Items[0].Description)
	assert.Equal(t, 100, tracker.progress.Items[0].Total)

	assert.Equal(t, 2, tracker.progress.Items[1].ID)
	assert.Equal(t, "Phase 2", tracker.progress.Items[1].Description)
	assert.Equal(t, 200, tracker.progress.Items[1].Total)
}

func TestProgressTracker_InitializeWithSeasons(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Equal(t, 10, tracker.progress.Total)
	assert.Equal(t, 0, len(tracker.progress.Items))

	seasons := []SeasonInfo{
		{StartYear: 2022},
		{StartYear: 2023},
	}

	tracker.InitializeWithSeasons(seasons)

	assert.Greater(t, tracker.progress.Total, 0, "total should be calculated from seasons")
	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 2, len(tracker.progress.Items))

	assert.Equal(t, 0, tracker.itemIndex[2022])
	assert.Equal(t, 1, tracker.itemIndex[2023])

	assert.Equal(t, 2022, tracker.progress.Items[0].ID)
	assert.Equal(t, "2022-23", tracker.progress.Items[0].Description)
	assert.Greater(t, tracker.progress.Items[0].Total, 0)

	assert.Equal(t, 2023, tracker.progress.Items[1].ID)
	assert.Equal(t, "2023-24", tracker.progress.Items[1].Description)
	assert.Greater(t, tracker.progress.Items[1].Total, 0)
}

func TestProgressTracker_SetFutureItem(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)

	tracker.SetFutureItem(0, 100)
	tracker.SetFutureItem(1, 200)
	tracker.SetFutureItem(5, 500)

	assert.Equal(t, 100, tracker.futureToID[0])
	assert.Equal(t, 200, tracker.futureToID[1])
	assert.Equal(t, 500, tracker.futureToID[5])

	_, exists := tracker.futureToID[2]
	assert.False(t, exists, "non-set future index should not exist")
}

func TestProgressTracker_SetFutureItem_Overwrite(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)

	tracker.SetFutureItem(0, 100)
	assert.Equal(t, 100, tracker.futureToID[0])

	tracker.SetFutureItem(0, 999)
	assert.Equal(t, 999, tracker.futureToID[0], "should overwrite previous mapping")
}

func TestProgressTracker_IsItemStarted(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 10},
		{ID: 2, Description: "Phase 2", Total: 20},
		{ID: 5, Description: "Phase 5", Total: 50},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.False(t, tracker.IsItemStarted(1), "item should not be started initially")
	assert.False(t, tracker.IsItemStarted(2), "item should not be started initially")
	assert.False(t, tracker.IsItemStarted(5), "item should not be started initially")
	assert.False(t, tracker.IsItemStarted(999), "non-existent item should return false")

	// Note: We can't test MarkItemStarted here without workflow context,
	// those tests are in ProgressTrackerTimestampSuite
}

func TestProgressTracker_GetItemTotal(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 100},
		{ID: 2, Description: "Phase 2", Total: 0},
		{ID: 10, Description: "Phase 10", Total: 500},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 100, tracker.GetItemTotal(1))
	assert.Equal(t, 0, tracker.GetItemTotal(2))
	assert.Equal(t, 500, tracker.GetItemTotal(10))
	assert.Equal(t, -1, tracker.GetItemTotal(999), "non-existent item should return -1")

	tracker.SetItemTotal(2, 250)
	assert.Equal(t, 250, tracker.GetItemTotal(2), "should return updated total")
}

func TestProgressTracker_GetItemTotal_EmptyTracker(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Equal(t, -1, tracker.GetItemTotal(1), "tracker with no items should return -1")
	assert.Equal(t, -1, tracker.GetItemTotal(999), "tracker with no items should return -1")
}

func TestProgressTracker_getResultPtr(t *testing.T) {
	t.Parallel()

	t.Run("slice of ints", func(t *testing.T) {
		results := make([]int, 3)
		ptr := getResultPtr(results, 1)
		assert.NotNil(t, ptr)

		intPtr, ok := ptr.(*int)
		assert.True(t, ok, "should return *int")

		*intPtr = 42
		assert.Equal(t, 42, results[1], "should modify the correct element")
	})

	t.Run("slice of strings", func(t *testing.T) {
		results := make([]string, 5)
		ptr := getResultPtr(results, 3)
		assert.NotNil(t, ptr)

		strPtr, ok := ptr.(*string)
		assert.True(t, ok, "should return *string")

		*strPtr = "hello"
		assert.Equal(t, "hello", results[3], "should modify the correct element")
	})

	t.Run("slice of structs", func(t *testing.T) {
		type TestStruct struct {
			Value int
			Name  string
		}
		results := make([]TestStruct, 2)
		ptr := getResultPtr(results, 0)
		assert.NotNil(t, ptr)

		structPtr, ok := ptr.(*TestStruct)
		assert.True(t, ok, "should return *TestStruct")

		structPtr.Value = 100
		structPtr.Name = "test"
		assert.Equal(t, 100, results[0].Value)
		assert.Equal(t, "test", results[0].Name)
	})

	t.Run("not a slice", func(t *testing.T) {
		notSlice := 42
		ptr := getResultPtr(notSlice, 0)
		assert.Nil(t, ptr, "should return nil for non-slice")
	})

	t.Run("out of bounds index", func(t *testing.T) {
		results := make([]int, 2)

		assert.Panics(t, func() {
			getResultPtr(results, 5)
		}, "should panic for out of bounds index")
	})
}

func TestProgressTracker_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("increment non-existent item does not panic", func(t *testing.T) {
		tracker := NewProgressTracker(10)
		assert.NotPanics(t, func() {
			tracker.IncrementItem(999)
			tracker.IncrementItemBy(888, 50)
		})
		assert.Equal(t, 51, tracker.progress.Completed, "should still increment total")
	})

	// Note: "mark non-existent item does not panic" is tested in ProgressTrackerTimestampSuite
	// since MarkItemStarted and MarkItemCompleted now require workflow.Context

	t.Run("set total for non-existent item does not panic", func(t *testing.T) {
		tracker := NewProgressTracker(10)
		assert.NotPanics(t, func() {
			tracker.SetItemTotal(999, 100)
		})
		assert.Equal(t, 10, tracker.progress.Total, "total should not change")
	})

	t.Run("empty phase description", func(t *testing.T) {
		phases := []PhaseInfo{
			{ID: 1, Description: "", Total: 10},
		}
		tracker := NewProgressTrackerWithPhases(phases)
		assert.Equal(t, "", tracker.progress.Items[0].Description)
	})

	t.Run("negative total treated as is", func(t *testing.T) {
		tracker := NewProgressTracker(-5)
		assert.Equal(t, -5, tracker.progress.Total)
	})
}

// Temporal-dependent methods that require Temporal test environment:
// - RegisterQueryHandler: Requires workflow.Context, tested via integration tests
// - WaitAll: Requires workflow.Context and workflow.Future, tested via integration tests
// - WaitAllForItem: Requires workflow.Context and workflow.Future, tested via integration tests
// - WaitAllWithItems: Requires workflow.Context and workflow.Future, tested via integration tests
// - WaitAllWithResults: Requires workflow.Context and workflow.Future, tested via integration tests
// - RunWorkerPool: Requires workflow.Context and workflow.Future, tested via integration tests
// - RunWorkerPoolWithHandler: Requires workflow.Context and workflow.Future, tested via integration tests
// - RunWorkerPoolForItem: Requires workflow.Context and workflow.Future, tested via integration tests

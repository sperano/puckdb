package workflow

// initializeSingleStepGroups is the number of InitializeWorkflow groups
// (fetch and upsert franchises, fetch and upsert seasons) sized to one step;
// the season-teams group has one step per season.
const initializeSingleStepGroups = 4

// The last saved report's Total must count every group's bars, not stay at
// the constructor's zero while SetBarTotal sizes each group.
func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_SavedTotalMatchesBars() {
	seasons := initializeTestSeasons()
	s.mockInitializeSuccess(seasons)
	lastSaved := recordLastSavedReport(s.env)

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	requireTotalIsBarSum(s.T(), lastSaved(), initializeSingleStepGroups+len(seasons))
}

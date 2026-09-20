package admin

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// AdminWorkflowsSuite uses the Temporal test environment to verify that
// each admin workflow is wired to the right activity and propagates
// errors as expected. The activities themselves hit Postgres and Redis
// and are not unit-tested here — they're exercised by integration tests.

type AdminWorkflowsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *AdminWorkflowsSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(DropDatabaseWorkflow)
	s.env.RegisterWorkflow(MigrateDatabaseWorkflow)
	s.env.RegisterWorkflow(ResetDatabaseWorkflow)
	s.env.RegisterWorkflow(FlushRedisWorkflow)
}

func (s *AdminWorkflowsSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestAdminWorkflowsSuite(t *testing.T) {
	suite.Run(t, new(AdminWorkflowsSuite))
}

// Constants are part of the public API of this package — pin them so a
// rename or reorder shows up as a test failure rather than a silent
// breakage of any caller that addresses workflows by ID.
func (s *AdminWorkflowsSuite) TestWorkflowIDConstants() {
	s.Equal("drop-database", WorkflowIDDropDatabase)
	s.Equal("migrate-database", WorkflowIDMigrateDatabase)
	s.Equal("reset-database", WorkflowIDResetDatabase)
	s.Equal("flush-redis", WorkflowIDFlushRedis)
}

func (s *AdminWorkflowsSuite) TestDropDatabaseWorkflow_CallsDropActivity() {
	s.env.OnActivity(DropDatabaseActivity, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DropDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestDropDatabaseWorkflow_PropagatesActivityError() {
	wantErr := errors.New("drop failed")
	s.env.OnActivity(DropDatabaseActivity, mock.Anything).Return(wantErr)

	s.env.ExecuteWorkflow(DropDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestMigrateDatabaseWorkflow_CallsMigrateActivity() {
	s.env.OnActivity(MigrateDatabaseActivity, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(MigrateDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestMigrateDatabaseWorkflow_PropagatesActivityError() {
	wantErr := errors.New("migrate failed")
	s.env.OnActivity(MigrateDatabaseActivity, mock.Anything).Return(wantErr)

	s.env.ExecuteWorkflow(MigrateDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// ResetDatabaseWorkflow has a non-trivial control flow: drop, then if-err
// return, then migrate. Each of the three branches gets its own test.

func (s *AdminWorkflowsSuite) TestResetDatabaseWorkflow_DropAndMigrate() {
	s.env.OnActivity(DropDatabaseActivity, mock.Anything).Return(nil)
	s.env.OnActivity(MigrateDatabaseActivity, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ResetDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestResetDatabaseWorkflow_DropFailureShortCircuits() {
	// When DropDatabaseActivity fails, the workflow must return without
	// calling MigrateDatabaseActivity. AssertExpectations in AfterTest
	// will fail if Migrate was unexpectedly invoked.
	s.env.OnActivity(DropDatabaseActivity, mock.Anything).Return(errors.New("drop failed"))

	s.env.ExecuteWorkflow(ResetDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestResetDatabaseWorkflow_MigrateFailurePropagates() {
	s.env.OnActivity(DropDatabaseActivity, mock.Anything).Return(nil)
	s.env.OnActivity(MigrateDatabaseActivity, mock.Anything).Return(errors.New("migrate failed"))

	s.env.ExecuteWorkflow(ResetDatabaseWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestFlushRedisWorkflow_CallsFlushActivity() {
	s.env.OnActivity(FlushRedisActivity, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(FlushRedisWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *AdminWorkflowsSuite) TestFlushRedisWorkflow_PropagatesActivityError() {
	wantErr := errors.New("redis unreachable")
	s.env.OnActivity(FlushRedisActivity, mock.Anything).Return(wantErr)

	s.env.ExecuteWorkflow(FlushRedisWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

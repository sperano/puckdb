package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// freeAgentQueriesFake is BuildFreeAgentPool's query group.
type freeAgentQueriesFake struct {
	listFAArgs   []sqlcdb.ListSimFreeAgentCandidatesParams
	listFARows   []int64
	listFAErr    error
	listFACalled int
}

var _ freeAgentQueries = (*freeAgentQueriesFake)(nil)

func (s *freeAgentQueriesFake) ListSimFreeAgentCandidates(_ context.Context, arg sqlcdb.ListSimFreeAgentCandidatesParams) ([]int64, error) {
	s.listFACalled++
	s.listFAArgs = append(s.listFAArgs, arg)
	if s.listFAErr != nil {
		return nil, s.listFAErr
	}
	return s.listFARows, nil
}

// FreeAgentPoolTestSuite uses Temporal's TestActivityEnvironment so
// activity.GetLogger(ctx) works (it panics outside an activity ctx).
type FreeAgentPoolTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *freeAgentQueriesFake
	acts    freeAgentActivity
}

func (s *FreeAgentPoolTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &freeAgentQueriesFake{}
	s.acts = freeAgentActivity{q: s.queries}
	s.env.RegisterActivity(s.acts.BuildFreeAgentPool)
}

func TestFreeAgentPoolTestSuite(t *testing.T) {
	suite.Run(t, new(FreeAgentPoolTestSuite))
}

// ============================================================================
// BuildFreeAgentPool
// ============================================================================

func (s *FreeAgentPoolTestSuite) TestBuildFreeAgentPool_PassesParamsToQuery() {
	s.queries.listFARows = []int64{1, 2, 3}

	in := BuildFreeAgentPoolInput{
		PoolID: 7, Season: 20242025,
		SimDate:    pgDate(s.T(), "2025-11-15"),
		WaiverDays: 2,
	}
	future, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, in)
	require.NoError(s.T(), err)

	var got BuildFreeAgentPoolResult
	require.NoError(s.T(), future.Get(&got))
	assert.Equal(s.T(), []int64{1, 2, 3}, got.PlayerIDs)

	require.Equal(s.T(), 1, s.queries.listFACalled)
	require.Len(s.T(), s.queries.listFAArgs, 1)
	args := s.queries.listFAArgs[0]
	assert.Equal(s.T(), int32(7), args.PoolID)
	assert.Equal(s.T(), int32(20242025), args.Season)
	assert.Equal(s.T(), int32(2), args.Column4,
		"waiver_days must thread through to the Column4 sqlc param")
	assert.Equal(s.T(), "2025-11-15", args.GameDate.Time.Format("2006-01-02"))
}

func (s *FreeAgentPoolTestSuite) TestBuildFreeAgentPool_PropagatesQueryError() {
	// Temporal's testsuite returns the activity error directly through
	// ExecuteActivity's second return — NOT through future.Get. Pin
	// the error path here so a future "swallow underlying error in
	// the activity layer" change is loud.
	s.queries.listFAErr = errors.New("connection lost")

	_, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{PoolID: 1})
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "list free agent candidates")
	assert.Contains(s.T(), err.Error(), "connection lost")
}

func (s *FreeAgentPoolTestSuite) TestBuildFreeAgentPool_EmptyResultIsValid() {
	// Empty FA pool is a real state (early-season, before many
	// players have played their first game) — pin that the activity
	// returns an empty result without error rather than treating
	// "no rows" as a problem.
	s.queries.listFARows = []int64{}

	future, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{PoolID: 1})
	require.NoError(s.T(), err)

	var got BuildFreeAgentPoolResult
	require.NoError(s.T(), future.Get(&got))
	assert.Empty(s.T(), got.PlayerIDs)
}

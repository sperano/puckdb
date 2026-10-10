package workflow

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/store"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	// seasonFanOutTestYear is an Edge-eligible season so every fan-out
	// workflow under test starts one child for it.
	seasonFanOutTestYear        = 2023
	seasonFanOutTestConcurrency = 2
	seasonFanOutTestPlayers     = 3
)

// seasonFanOutCase registers one parent workflow and the activities and child
// workflows it reaches. wantRefresh is the RefreshCurrent value the child must
// receive, for the workflows that pass one.
type seasonFanOutCase struct {
	name     string
	workflow any
	register func(env *testsuite.TestWorkflowEnvironment, wantRefresh bool)
}

func seasonFanOutCases() []seasonFanOutCase {
	var (
		ba *worknhl.BoxscoreActivities
		pa *workplayer.Activities
	)
	return []seasonFanOutCase{
		{"FetchEdgeSeasonsWorkflow", FetchEdgeSeasonsWorkflow,
			func(env *testsuite.TestWorkflowEnvironment, wantRefresh bool) {
				env.RegisterWorkflow(FetchEdgeWorkflow)
				env.OnWorkflow(FetchEdgeWorkflow, mock.Anything, mock.MatchedBy(func(in FetchEdgeWorkflowInput) bool {
					return in.RefreshCurrent == wantRefresh
				})).Return(core.OriginCounts{}, nil).Once()
			}},
		{"ImportEdgeSeasonsWorkflow", ImportEdgeSeasonsWorkflow,
			func(env *testsuite.TestWorkflowEnvironment, _ bool) {
				env.RegisterWorkflow(ImportEdgeWorkflow)
				env.OnWorkflow(ImportEdgeWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil).Once()
			}},
		{"ImportPlayerLogsWorkflow", ImportPlayerLogsWorkflow,
			func(env *testsuite.TestWorkflowEnvironment, _ bool) {
				env.RegisterWorkflow(ImportSeasonPlayerLogsWorkflow)
				env.OnActivity(pa.CountPlayersForAllSeasons, mock.Anything, []int{seasonFanOutTestYear}).
					Return(map[int]int{seasonFanOutTestYear: seasonFanOutTestPlayers}, nil).Once()
				env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil).Once()
			}},
		{"FetchPlayerLogsWorkflow", FetchPlayerLogsWorkflow,
			func(env *testsuite.TestWorkflowEnvironment, wantRefresh bool) {
				env.RegisterWorkflow(FetchSeasonPlayerLogsWorkflow)
				env.OnActivity(pa.LoadSeasonBoxscorePlayers, mock.Anything, seasonFanOutTestYear).
					Return(make([]store.BoxscorePlayer, seasonFanOutTestPlayers), nil).Once()
				env.OnWorkflow(FetchSeasonPlayerLogsWorkflow, mock.Anything, mock.MatchedBy(func(in FetchSeasonPlayerLogsInput) bool {
					return in.RefreshCurrent == wantRefresh
				})).Return(nil).Once()
			}},
		{"ExtractBoxscorePlayersWorkflow", ExtractBoxscorePlayersWorkflow,
			func(env *testsuite.TestWorkflowEnvironment, _ bool) {
				env.OnActivity(ba.ExtractAndSaveBoxscorePlayers, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil).Once()
				env.OnActivity(pa.ConsolidateBoxscorePlayersActivity, mock.Anything, mock.Anything).
					Return(workplayer.ConsolidatePlayersResult{TotalPlayers: seasonFanOutTestPlayers, UniquePlayers: seasonFanOutTestPlayers}, nil).Once()
			}},
	}
}

// explicitSeasonsInput sets every SeasonsInput field the fan-out workflows read.
func explicitSeasonsInput() *model.SeasonsInput {
	start, end, concurrency, refresh := seasonFanOutTestYear, seasonFanOutTestYear, seasonFanOutTestConcurrency, true
	return &model.SeasonsInput{
		StartSeason:              &start,
		EndSeason:                &end,
		SeasonConcurrency:        &concurrency,
		RefreshCurrentEdge:       &refresh,
		RefreshCurrentPlayerLogs: &refresh,
	}
}

// TestSeasonFanOutWorkflowsInput runs every season fan-out parent with an
// omitted input (GraphQL leaves the optional argument nil) and with an explicit
// one. A nil dereference would surface as a workflow PanicError here; on a
// worker it fails the workflow task forever and the run never leaves "running".
func TestSeasonFanOutWorkflowsInput(t *testing.T) {
	inputs := []struct {
		name string
		// input is what the workflow receives; wantManifest is what the
		// manifest activity must receive after normalization.
		input, wantManifest *model.SeasonsInput
		wantRefresh         bool
	}{
		{"omitted", nil, &model.SeasonsInput{}, false},
		{"explicit", explicitSeasonsInput(), explicitSeasonsInput(), true},
	}
	for _, tc := range seasonFanOutCases() {
		for _, in := range inputs {
			t.Run(tc.name+"/"+in.name, func(t *testing.T) {
				var ts testsuite.WorkflowTestSuite
				env := ts.NewTestWorkflowEnvironment()
				env.RegisterWorkflow(tc.workflow)
				var sa *worknhl.SeasonsActivities
				env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, in.wantManifest).Return(worknhl.FetchSeasonsManifestResult{
					Seasons: []nhl.SeasonInfo{testSeasonW(seasonFanOutTestYear, "2023-10-10", "2023-10-12")},
					Origin:  core.OriginFileSystem,
				}, nil).Once()
				tc.register(env, in.wantRefresh)

				env.ExecuteWorkflow(tc.workflow, in.input)

				require.True(t, env.IsWorkflowCompleted())
				assert.NoError(t, env.GetWorkflowError())
				env.AssertExpectations(t)
			})
		}
	}
}

func TestNormalizeSeasonsInput(t *testing.T) {
	assert.Equal(t, &model.SeasonsInput{}, normalizeSeasonsInput(nil))
	explicit := explicitSeasonsInput()
	assert.Same(t, explicit, normalizeSeasonsInput(explicit))
}

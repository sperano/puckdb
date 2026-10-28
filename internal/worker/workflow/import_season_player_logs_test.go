package workflow

import (
	"errors"
	"testing"

	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func newImportSeasonPlayerLogsV2Env(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportSeasonPlayerLogsV2Workflow)
	return env
}

func TestImportSeasonPlayerLogsV2ImportsEveryManifestPlayerOnce(t *testing.T) {
	env := newImportSeasonPlayerLogsV2Env(t)
	saved := recordSavedReports(env)
	imported := recordImportedPlayers(env)
	ids := make([]int64, playerLogsFixtureSkaters)
	for i := range ids {
		ids[i] = playerLogsFixtureFirstSkater + int64(i)
	}

	env.ExecuteWorkflow(ImportSeasonPlayerLogsV2Workflow,
		ImportSeasonPlayerLogsInput{Season: playerLogsFixtureSeason(), PlayerIDs: ids})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	counts := imported()
	assert.Len(t, counts, len(ids))
	for _, id := range ids {
		assert.Equal(t, 1, counts[id], "player %d", id)
	}
	require.Len(t, saved.last, 1)
	var report shared.ProgressReport
	for _, r := range saved.last {
		report = r
	}
	bar := report.Groups[GroupImportPlayerLogs].Bars[0]
	assert.Equal(t, [2]int{len(ids), len(ids)}, [2]int{bar.Current, bar.Total})
}

func TestImportSeasonPlayerLogsV2NoPlayersStartsNoBatch(t *testing.T) {
	env := newImportSeasonPlayerLogsV2Env(t)

	env.ExecuteWorkflow(ImportSeasonPlayerLogsV2Workflow, ImportSeasonPlayerLogsInput{Season: playerLogsFixtureSeason()})
	require.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestImportSeasonPlayerLogsV2BatchError(t *testing.T) {
	env := newImportSeasonPlayerLogsV2Env(t)
	recordSavedReports(env)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).
		Return((*worknhl.ImportPlayerGameLogsBatchResult)(nil), errors.New("batch failed"))

	env.ExecuteWorkflow(ImportSeasonPlayerLogsV2Workflow,
		ImportSeasonPlayerLogsInput{Season: playerLogsFixtureSeason(), PlayerIDs: []int64{1, 2, 3}})
	require.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestSeasonPlayerLogTotalsSummary(t *testing.T) {
	var totals seasonPlayerLogTotals
	totals.add(worknhl.ImportPlayerGameLogsBatchResult{PlayersProcessed: 2})
	assert.Equal(t, "Imported game logs for 2 of 2 players in 1s.", totals.summary(2, "1s"))

	totals.add(worknhl.ImportPlayerGameLogsBatchResult{PlayersProcessed: 1, PlayersWithoutLog: 2, PlayersUnreadable: 1})
	assert.Equal(t, "Imported game logs for 3 of 6 players in 1s. 2 had no cached log. 1 logs were unreadable.",
		totals.summary(6, "1s"))
}

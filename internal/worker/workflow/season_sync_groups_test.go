package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	// The Yahoo child outlasts the NHL child, as in the October 2026 sync
	// (569 s against 246 s).
	groupsTestYahooDuration = time.Hour
	groupsTestNHLDuration   = 10 * time.Minute
	groupsUpcomingActivity  = "FetchUpcomingSeasonRosters"
	groupsImportUpcoming    = "ImportUpcomingSeasonRosters"
)

var (
	groupsTestStart    = time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	errGroupsYahooDown = errors.New("yahoo season fetch failed")
	errGroupsNHLDown   = errors.New("nhl season fetch failed")
)

// groupsTimeline records, on the workflow clock, when each child workflow
// started and completed and when each activity started.
type groupsTimeline struct {
	mu        sync.Mutex
	env       *testsuite.TestWorkflowEnvironment
	started   map[string]time.Time
	completed map[string]time.Time
	activity  map[string]time.Time
}

func newGroupsTimeline(env *testsuite.TestWorkflowEnvironment) *groupsTimeline {
	tl := &groupsTimeline{env: env, started: map[string]time.Time{}, completed: map[string]time.Time{},
		activity: map[string]time.Time{}}
	env.SetOnChildWorkflowStartedListener(func(info *workflow.Info, _ workflow.Context, _ converter.EncodedValues) {
		tl.record(tl.started, info.WorkflowExecution.ID)
	})
	env.SetOnChildWorkflowCompletedListener(func(info *workflow.Info, _ converter.EncodedValue, _ error) {
		tl.record(tl.completed, info.WorkflowExecution.ID)
	})
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		tl.record(tl.activity, info.ActivityType.Name)
	})
	return tl
}

func (tl *groupsTimeline) record(events map[string]time.Time, key string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if _, seen := events[key]; !seen {
		events[key] = tl.env.Now()
	}
}

// newGroupsTestEnv sets up a season sync over one started season with Yahoo
// leagues: the Yahoo child returns yahooErr after an hour, the NHL child
// returns nhlErr after ten minutes.
func newGroupsTestEnv(t *testing.T, mode seasonSyncMode, yahooErr, nhlErr error) (*testsuite.TestWorkflowEnvironment, *groupsTimeline) {
	t.Helper()
	withYahooSnapshot(t, preseasonYahooSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(groupsTestStart)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)

	yahooChild, nhlChild := any(FetchYahooSeasonWorkflow), any(FetchNHLSeasonWorkflow)
	if mode == seasonSyncImport {
		yahooChild, nhlChild = ImportYahooSeasonWorkflow, ImportNHLSeasonWorkflow
	}
	env.RegisterWorkflow(yahooChild)
	env.RegisterWorkflow(nhlChild)
	season := testSeasonW(preseasonTestYear, "2026-09-29", "2026-10-01")
	var activities *worknhl.SeasonsActivities
	var playoffActivities *worknhl.PlayoffActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, mock.Anything).
		Return(worknhl.FetchSeasonsManifestResult{Seasons: []nhl.SeasonInfo{season}}, nil)
	env.OnActivity(playoffActivities.ListSeasonTeams, mock.Anything, preseasonTestYear).Return([]string{"MTL"}, nil)
	env.OnWorkflow(yahooChild, mock.Anything, mock.Anything).
		After(groupsTestYahooDuration).Return(YahooSeasonSyncResult{}, yahooErr).Once()
	env.OnWorkflow(nhlChild, mock.Anything, mock.Anything).
		After(groupsTestNHLDuration).Return(core.OriginCounts{}, nhlErr).Once()
	return env, newGroupsTimeline(env)
}

func executeSeasonSyncParent(env *testsuite.TestWorkflowEnvironment, mode seasonSyncMode) {
	if mode == seasonSyncImport {
		env.RegisterWorkflow(ImportSeasonsWorkflow)
		env.ExecuteWorkflow(ImportSeasonsWorkflow, preseasonInput())
		return
	}
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.ExecuteWorkflow(FetchSeasonsWorkflow, preseasonInput())
}

// The NHL fetch starts while the Yahoo fetch is still running, and the
// parent finishes only after both: the step after the join (the upcoming
// season rosters) starts once both children completed, and so does every
// later sync step (process-players first), which the sync command starts
// only after FetchSeasonsWorkflow returns.
func TestFetchSeasonsWorkflow_RunsYahooAndNHLGroupsConcurrently(t *testing.T) {
	env, tl := newGroupsTestEnv(t, seasonSyncFetch, nil, nil)

	executeSeasonSyncParent(env, seasonSyncFetch)

	require.NoError(t, env.GetWorkflowError())
	finished := env.Now()
	yahooID, nhlID := WorkflowIDFetchYahooSeason(preseasonTestYear), WorkflowIDFetchNHLSeason(preseasonTestYear)
	require.Contains(t, tl.completed, yahooID)
	require.Contains(t, tl.completed, nhlID)
	assert.True(t, tl.started[nhlID].Before(tl.completed[yahooID]),
		"%s started at %s, after %s completed at %s", nhlID, tl.started[nhlID], yahooID, tl.completed[yahooID])
	assert.True(t, tl.completed[nhlID].Before(tl.completed[yahooID]))
	require.Contains(t, tl.activity, groupsUpcomingActivity)
	for _, id := range []string{yahooID, nhlID} {
		assert.False(t, tl.activity[groupsUpcomingActivity].Before(tl.completed[id]),
			"upcoming season step started before %s completed", id)
		assert.False(t, finished.Before(tl.completed[id]), "parent finished before %s completed", id)
	}
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "1 Yahoo season(s), 1 started NHL season(s)")
	env.AssertExpectations(t)
}

// A Yahoo failure fails the sync with the Yahoo error and progress message,
// as in the serial order, and skips the upcoming season step. The NHL group,
// already running, still finishes.
func TestFetchSeasonsWorkflow_ConcurrentYahooFailureFailsSync(t *testing.T) {
	env, tl := newGroupsTestEnv(t, seasonSyncFetch, errGroupsYahooDown, nil)

	executeSeasonSyncParent(env, seasonSyncFetch)

	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.ErrorContains(t, err, errGroupsYahooDown.Error())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "Yahoo metadata partially completed for seasons 2026 onward")
	assert.NotZero(t, report.Groups[groupNHLSeasons].CompletedAt, "NHL group finished")
	assert.Zero(t, report.Groups[groupYahooMetadata].CompletedAt)
	assert.NotContains(t, tl.activity, groupsUpcomingActivity)
	env.AssertExpectations(t)
}

// An NHL failure alone fails the sync with the NHL error, after the Yahoo
// group has finished.
func TestFetchSeasonsWorkflow_ConcurrentNHLFailureWaitsForYahoo(t *testing.T) {
	env, tl := newGroupsTestEnv(t, seasonSyncFetch, nil, errGroupsNHLDown)

	executeSeasonSyncParent(env, seasonSyncFetch)

	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.ErrorContains(t, err, errGroupsNHLDown.Error())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "NHL data partially completed")
	assert.NotZero(t, report.Groups[groupYahooMetadata].CompletedAt, "Yahoo group finished")
	assert.NotContains(t, tl.activity, groupsUpcomingActivity)
	env.AssertExpectations(t)
}

// When both groups fail, the Yahoo error is returned and the message names
// both failures.
func TestFetchSeasonsWorkflow_ConcurrentBothFailReportsBoth(t *testing.T) {
	env, _ := newGroupsTestEnv(t, seasonSyncFetch, errGroupsYahooDown, errGroupsNHLDown)

	executeSeasonSyncParent(env, seasonSyncFetch)

	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.ErrorContains(t, err, errGroupsYahooDown.Error())
	assert.NotContains(t, err.Error(), errGroupsNHLDown.Error())
	report := queryProgress(t, env)
	assert.Contains(t, report.Message, "Yahoo metadata and NHL data both failed for seasons 2026 onward")
	assert.Contains(t, report.Message, errGroupsYahooDown.Error())
	assert.Contains(t, report.Message, errGroupsNHLDown.Error())
	env.AssertExpectations(t)
}

// A history recorded before the change (no version marker) keeps the serial
// order: the NHL fetch starts only after the Yahoo fetch completed.
func TestFetchSeasonsWorkflow_DefaultVersionRunsGroupsSerially(t *testing.T) {
	env, tl := newGroupsTestEnv(t, seasonSyncFetch, nil, nil)
	env.OnGetVersion(seasonSyncConcurrentGroupsChange, workflow.DefaultVersion, seasonSyncConcurrentGroupsVersion).
		Return(workflow.DefaultVersion)

	executeSeasonSyncParent(env, seasonSyncFetch)

	require.NoError(t, env.GetWorkflowError())
	assertGroupsSerial(t, tl, WorkflowIDFetchYahooSeason(preseasonTestYear), WorkflowIDFetchNHLSeason(preseasonTestYear))
	env.AssertExpectations(t)
}

// The import keeps the serial order: ImportDay writes Yahoo roster rows whose
// foreign keys need the teams the Yahoo import writes.
func TestImportSeasonsWorkflow_RunsGroupsSerially(t *testing.T) {
	env, tl := newGroupsTestEnv(t, seasonSyncImport, nil, nil)

	executeSeasonSyncParent(env, seasonSyncImport)

	require.NoError(t, env.GetWorkflowError())
	assertGroupsSerial(t, tl, WorkflowIDImportYahooSeason(preseasonTestYear), WorkflowIDImportNHLSeason(preseasonTestYear))
	assert.Contains(t, tl.activity, groupsImportUpcoming)
	env.AssertExpectations(t)
}

func assertGroupsSerial(t *testing.T, tl *groupsTimeline, yahooID, nhlID string) {
	t.Helper()
	require.Contains(t, tl.completed, yahooID)
	require.Contains(t, tl.started, nhlID)
	assert.False(t, tl.started[nhlID].Before(tl.completed[yahooID]),
		"%s started at %s, before %s completed at %s", nhlID, tl.started[nhlID], yahooID, tl.completed[yahooID])
}

package workflow

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// Fixture source IDs shared by the RefreshNewsWorkflow tests below.
const (
	newsTestSourceA = "source-a"
	newsTestSourceB = "source-b"
	newsTestSourceC = "source-c"
	// newsTestSeason is the season newsTestNow resolves to (September is
	// past seasonRolloverMonth, so the current year is used).
	newsTestSeason = 2026
)

// newsTestNow is the workflow clock used by most RefreshNewsWorkflow tests.
var newsTestNow = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

// fixedNewsSources is a small, deterministic, already-priority-sorted source
// set used in place of the real internal/news/sources.yaml.
func fixedNewsSources() []news.Source {
	return []news.Source{
		{ID: newsTestSourceA, Publisher: "Source A", Kind: news.KindOfficial, Adapter: news.AdapterRSS,
			URL: "https://a.example/feed", Priority: 1, RefreshMinutes: 30, Enabled: true},
		{ID: newsTestSourceB, Publisher: "Source B", Kind: news.KindReporting, Adapter: news.AdapterRSS,
			URL: "https://b.example/feed", Priority: 2, RefreshMinutes: 30, Enabled: true},
		{ID: newsTestSourceC, Publisher: "Source C", Kind: news.KindStructured, Adapter: news.AdapterYahooStatus,
			Priority: 3, RefreshMinutes: 60, Enabled: true},
	}
}

// withFixedNewsSources swaps loadNewsSources for the fixture set and
// restores it after the test.
func withFixedNewsSources(t *testing.T) []news.Source {
	t.Helper()
	sources := fixedNewsSources()
	previous := loadNewsSources
	loadNewsSources = func(string) ([]news.Source, error) { return sources, nil }
	t.Cleanup(func() { loadNewsSources = previous })
	return sources
}

// withNewsSources swaps loadNewsSources for a fixed result, restoring it
// after the test. Used for the unknown/disabled-source and load-error cases,
// which need a source set different from the shared fixture.
func withNewsSources(t *testing.T, sources []news.Source, err error) {
	t.Helper()
	previous := loadNewsSources
	loadNewsSources = func(string) ([]news.Source, error) { return sources, err }
	t.Cleanup(func() { loadNewsSources = previous })
}

// newRefreshNewsEnv registers RefreshNewsWorkflow and tolerates the
// progress-tracker's local-activity saves (see mockProgressSaves in
// preseason_sync_test.go), which is all the progress tracker needs here.
func newRefreshNewsEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(RefreshNewsWorkflow)
	mockProgressSaves(env)
	return env
}

// planInputMatch matches a newsfeed.PlanInput built from sources, season and
// force.
func planInputMatch(sources []news.Source, season int, force bool) any {
	return mock.MatchedBy(func(input newsfeed.PlanInput) bool {
		return assert.ObjectsAreEqual(sources, input.Sources) && input.Season == season && input.Force == force
	})
}

// mockTrivialProcessAndPrune stubs a single, empty processing batch and an
// empty prune, for tests that only care about planning and season
// resolution.
func mockTrivialProcessAndPrune(env *testsuite.TestWorkflowEnvironment) {
	var act *newsfeed.Activities
	env.OnActivity(act.ProcessNewsVersions, mock.Anything, mock.Anything).
		Return(newsfeed.ProcessResult{Remaining: false}, nil).Once()
	env.OnActivity(act.PruneNews, mock.Anything, mock.Anything).Return(newsfeed.PruneResult{}, nil).Once()
}

func TestRefreshNewsWorkflow_HappyPath(t *testing.T) {
	sources := withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	env.SetStartTime(newsTestNow)

	due := []news.Source{sources[0], sources[1]}
	notDue := []newsfeed.NotDue{{SourceID: sources[2].ID, NextDue: newsTestNow.Add(time.Hour)}}
	var act *newsfeed.Activities
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, planInputMatch(sources, newsTestSeason, false)).
		Return(newsfeed.Plan{Due: due, NotDue: notDue}, nil).Once()
	env.OnActivity(act.FetchNewsSource, mock.Anything, newsfeed.FetchInput{Source: sources[0], Season: newsTestSeason}).
		Return(newsfeed.FetchResult{Stored: news.IngestResult{Items: 2, NewVersions: 1, Unchanged: 1}}, nil).Once()
	env.OnActivity(act.FetchNewsSource, mock.Anything, newsfeed.FetchInput{Source: sources[1], Season: newsTestSeason}).
		Return(newsfeed.FetchResult{NotModified: true}, nil).Once()

	firstBatch := newsfeed.ProcessResult{Outcome: news.ProcessOutcome{Versions: 5, Resolved: 3, IncidentsCreated: 1}, Remaining: true}
	secondBatch := newsfeed.ProcessResult{Outcome: news.ProcessOutcome{Versions: 2, Resolved: 2, Repeats: 1}, Remaining: false}
	env.OnActivity(act.ProcessNewsVersions, mock.Anything, mock.Anything).Return(firstBatch, nil).Once()
	env.OnActivity(act.ProcessNewsVersions, mock.Anything, mock.Anything).Return(secondBatch, nil).Once()

	pruned := newsfeed.PruneResult{Incidents: 1, Articles: 2, Versions: 3}
	env.OnActivity(act.PruneNews, mock.Anything, mock.Anything).Return(pruned, nil).Once()

	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})

	require.NoError(t, env.GetWorkflowError())
	var result RefreshNewsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, newsTestSeason, result.Season)
	assert.Equal(t, []string{sources[0].ID}, result.Fetched)
	assert.Equal(t, []string{sources[1].ID}, result.NotModified)
	assert.Empty(t, result.Failed)
	assert.Equal(t, notDue, result.NotDue)
	assert.Equal(t, news.IngestResult{Items: 2, NewVersions: 1, Unchanged: 1}, result.Stored)
	var wantProcessed news.ProcessOutcome
	wantProcessed.Add(firstBatch.Outcome)
	wantProcessed.Add(secondBatch.Outcome)
	assert.Equal(t, wantProcessed, result.Processed)
	assert.Equal(t, pruned, result.Pruned)
	env.AssertExpectations(t)
}

func TestRefreshNewsWorkflow_FetchFailureRecordsFailureButSucceeds(t *testing.T) {
	sources := withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	env.SetStartTime(newsTestNow)

	due := []news.Source{sources[0], sources[1]}
	var act *newsfeed.Activities
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, planInputMatch(sources, newsTestSeason, false)).
		Return(newsfeed.Plan{Due: due}, nil).Once()
	env.OnActivity(act.FetchNewsSource, mock.Anything, newsfeed.FetchInput{Source: sources[0], Season: newsTestSeason}).
		Return(newsfeed.FetchResult{}, nil).Once()
	fetchErr := temporal.NewNonRetryableApplicationError("feed unreachable", newsfeed.ErrTypeHTTP, nil)
	env.OnActivity(act.FetchNewsSource, mock.Anything, newsfeed.FetchInput{Source: sources[1], Season: newsTestSeason}).
		Return(newsfeed.FetchResult{}, fetchErr).Once()
	env.OnActivity(act.RecordNewsFetchFailure, mock.Anything, newsfeed.FailureInput{
		Source: sources[1], Season: newsTestSeason, Error: "feed unreachable",
	}).Return(nil).Once()
	mockTrivialProcessAndPrune(env)

	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})

	require.NoError(t, env.GetWorkflowError())
	var result RefreshNewsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{sources[0].ID}, result.Fetched)
	assert.Equal(t, []FailedNewsSource{{SourceID: sources[1].ID, Error: "feed unreachable"}}, result.Failed)
	env.AssertExpectations(t)
}

func TestRefreshNewsWorkflow_SkippedSourceIsNeitherFetchedNorFailed(t *testing.T) {
	sources := withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	env.SetStartTime(newsTestNow)

	var act *newsfeed.Activities
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, planInputMatch(sources, newsTestSeason, false)).
		Return(newsfeed.Plan{Due: []news.Source{sources[0]}}, nil).Once()
	const reason = "all 2 leagues of the season are temporary stand-ins without a Yahoo player pool"
	env.OnActivity(act.FetchNewsSource, mock.Anything, newsfeed.FetchInput{Source: sources[0], Season: newsTestSeason}).
		Return(newsfeed.FetchResult{Skipped: reason}, nil).Once()
	mockTrivialProcessAndPrune(env)

	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})

	require.NoError(t, env.GetWorkflowError())
	var result RefreshNewsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Empty(t, result.Fetched)
	assert.Empty(t, result.Failed, "a skipped source must not be recorded as a fetch failure")
	assert.Equal(t, []SkippedNewsSource{{SourceID: sources[0].ID, Reason: reason}}, result.Skipped)
	env.AssertExpectations(t)
}

func TestRefreshNewsWorkflow_InvalidRequestedSourceFails(t *testing.T) {
	tests := []struct {
		name    string
		sources []news.Source
		request string
	}{
		{"unknown_source", fixedNewsSources(), "does-not-exist"},
		{"disabled_source", []news.Source{{
			ID: "disabled-src", Publisher: "X", Kind: news.KindOfficial, Adapter: news.AdapterRSS,
			URL: "https://x.example/feed", Priority: 1, RefreshMinutes: 30, Enabled: false,
		}}, "disabled-src"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withNewsSources(t, tt.sources, nil)
			env := newRefreshNewsEnv(t)

			env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{Sources: []string{tt.request}})

			require.Error(t, env.GetWorkflowError())
			assert.Contains(t, env.GetWorkflowError().Error(), tt.request)
		})
	}
}

func TestRefreshNewsWorkflow_SourcesLoadErrorFails(t *testing.T) {
	withNewsSources(t, nil, errors.New("disk read failed"))
	env := newRefreshNewsEnv(t)

	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})

	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "disk read failed")
}

func TestRefreshNewsWorkflow_ForceIsPassedToPlan(t *testing.T) {
	sources := withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	env.SetStartTime(newsTestNow)

	var act *newsfeed.Activities
	var captured newsfeed.PlanInput
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(newsfeed.PlanInput) }).
		Return(newsfeed.Plan{}, nil).Once()
	mockTrivialProcessAndPrune(env)

	force := true
	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{Force: &force})

	require.NoError(t, env.GetWorkflowError())
	assert.True(t, captured.Force)
	assert.Equal(t, sources, captured.Sources)
	assert.Equal(t, newsTestSeason, captured.Season)
}

func TestRefreshNewsWorkflow_SeasonDefaultsFromClock(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		input *model.RefreshNewsInput
		want  int
	}{
		{"september_uses_current_year", time.Date(2026, time.September, 25, 0, 0, 0, 0, time.UTC),
			&model.RefreshNewsInput{}, 2026},
		{"march_uses_previous_year", time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC),
			&model.RefreshNewsInput{}, 2026},
		{"explicit_season_wins", time.Date(2026, time.September, 25, 0, 0, 0, 0, time.UTC),
			&model.RefreshNewsInput{Season: intPtr(1999)}, 1999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFixedNewsSources(t)
			env := newRefreshNewsEnv(t)
			env.SetStartTime(tt.now)
			var act *newsfeed.Activities
			env.OnActivity(act.PlanNewsRefresh, mock.Anything, mock.Anything).Return(newsfeed.Plan{}, nil).Once()
			mockTrivialProcessAndPrune(env)

			env.ExecuteWorkflow(RefreshNewsWorkflow, tt.input)

			require.NoError(t, env.GetWorkflowError())
			var result RefreshNewsResult
			require.NoError(t, env.GetWorkflowResult(&result))
			assert.Equal(t, tt.want, result.Season)
		})
	}
}

func TestRefreshNewsWorkflow_ProcessLoopStopsAtBatchLimit(t *testing.T) {
	withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	env.SetStartTime(newsTestNow)

	var act *newsfeed.Activities
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, mock.Anything).Return(newsfeed.Plan{}, nil).Once()
	env.OnActivity(act.ProcessNewsVersions, mock.Anything, mock.Anything).
		Return(newsfeed.ProcessResult{Remaining: true}, nil)
	env.OnActivity(act.PruneNews, mock.Anything, mock.Anything).Return(newsfeed.PruneResult{}, nil).Once()

	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})

	require.NoError(t, env.GetWorkflowError())
	env.AssertActivityNumberOfCalls(t, "ProcessNewsVersions", maxNewsProcessBatches)
}

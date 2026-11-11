package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	fanOutTestWorkflow = "SeasonFanOutTestWorkflow"
	fanOutTestWork     = "SeasonFanOutTestWork"
	fanOutTestHeader   = "Fan-out test..."
	fanOutTestLabel    = "Ran"
	fanOutTestCounts   = "reads"
	// fanOutTestBarBase is subtracted from a start year to size its bar.
	fanOutTestBarBase = 2000
	// fanOutTestMaxConcurrency caps the season concurrency in the cap test.
	fanOutTestMaxConcurrency = 2
)

// fanOutTestSeasons is the manifest of most runner tests.
func fanOutTestSeasons() []nhl.SeasonInfo {
	return []nhl.SeasonInfo{
		testSeasonW(2021, "2021-10-12", "2021-10-14"),
		testSeasonW(2022, "2022-10-07", "2022-10-09"),
		testSeasonW(2023, "2023-10-10", "2023-10-12"),
	}
}

func fanOutTestSourceKey(startYear int) string { return fmt.Sprintf("fan-out-test-%d", startYear) }

// fanOutTestBarTotal is each season's bar total in the standard plan.
func fanOutTestBarTotal(_ workflow.Context, season nhl.SeasonInfo) (int, error) {
	return season.ID.StartYear() - fanOutTestBarBase, nil
}

// fanOutTestCountsFor is what the test work returns for a season.
func fanOutTestCountsFor(startYear int) core.OriginCounts {
	if startYear == 2023 {
		return core.OriginCounts{core.OriginFileSystem: 1, core.OriginRedis: 1}
	}
	return core.OriginCounts{core.OriginFileSystem: 1}
}

// startedSeason is one SeasonWorkStarter call.
type startedSeason struct {
	year  int
	index int
	last  bool
}

// fanOutProbe runs runSeasonFanOut in a test workflow and records what it
// did: the states Prepare saw, the starter calls, the stale report keys and
// the result.
type fanOutProbe struct {
	env     *testsuite.TestWorkflowEnvironment
	saved   *savedReports
	opts    SeasonFanOut
	plan    func(SeasonFanOutState) SeasonFanOutPlan
	workErr map[int]error

	mu        sync.Mutex
	prepared  []SeasonFanOutState
	started   []startedSeason
	staleKeys [][]string
	manifests int
	result    SeasonFanOutResult
	err       error
}

// newFanOutProbe returns a probe whose manifest lists seasons (and expects
// wantManifest as its input) and whose plan is the standard one.
func newFanOutProbe(t *testing.T, seasons []nhl.SeasonInfo, wantManifest *model.SeasonsInput) *fanOutProbe {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	p := &fanOutProbe{env: ts.NewTestWorkflowEnvironment(), workErr: map[int]error{}}
	p.opts = SeasonFanOut{
		Report:  &shared.ProgressReport{Groups: []shared.ProgressGroup{{Header: fanOutTestHeader}}},
		Prepare: p.prepare,
	}
	p.plan = func(SeasonFanOutState) SeasonFanOutPlan { return p.standardPlan() }
	p.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input *model.SeasonsInput) error {
		p.result, p.err = runSeasonFanOut(ctx, input, p.opts)
		return p.err
	}, workflow.RegisterOptions{Name: fanOutTestWorkflow})
	p.env.RegisterActivityWithOptions(func(_ context.Context, startYear int) (core.OriginCounts, error) {
		if err := p.workErr[startYear]; err != nil {
			return nil, err
		}
		return fanOutTestCountsFor(startYear), nil
	}, activity.RegisterOptions{Name: fanOutTestWork})
	p.saved = recordSavedReports(p.env)
	p.mockActivities(seasons, wantManifest)
	return p
}

func (p *fanOutProbe) mockActivities(seasons []nhl.SeasonInfo, wantManifest *model.SeasonsInput) {
	var sa *worknhl.SeasonsActivities
	manifestArg := any(mock.Anything)
	if wantManifest != nil {
		manifestArg = wantManifest
	}
	p.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, manifestArg).Return(
		func(context.Context, *model.SeasonsInput) (worknhl.FetchSeasonsManifestResult, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.manifests++
			return worknhl.FetchSeasonsManifestResult{Seasons: seasons, Origin: core.OriginFileSystem}, nil
		}).Maybe()
	p.env.OnActivity(((*shared.ProgressActivities)(nil)).DeleteBatch, mock.Anything, mock.Anything).Return(
		func(_ context.Context, keys []string) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.staleKeys = append(p.staleKeys, keys)
			return nil
		}).Maybe()
}

func (p *fanOutProbe) prepare(_ workflow.Context, state SeasonFanOutState) (SeasonFanOutPlan, error) {
	p.mu.Lock()
	p.prepared = append(p.prepared, state)
	p.mu.Unlock()
	return p.plan(state), nil
}

// standardPlan sizes bars by fanOutTestBarTotal, keys them by
// fanOutTestSourceKey, completes with the GroupLabel formatting and runs the
// test work activity for each season.
func (p *fanOutProbe) standardPlan() SeasonFanOutPlan {
	return SeasonFanOutPlan{
		Group: SeasonGroupConfig{
			Counter: fanOutTestBarTotal, SourceKeyFunc: fanOutTestSourceKey,
			GroupLabel: fanOutTestLabel, CountLabel: fanOutTestCounts,
		},
		Start: p.start,
	}
}

func (p *fanOutProbe) start(ctx workflow.Context, season nhl.SeasonInfo, index int, last bool) workflow.Future {
	p.mu.Lock()
	p.started = append(p.started, startedSeason{season.ID.StartYear(), index, last})
	p.mu.Unlock()
	return workflow.ExecuteActivity(ctx, fanOutTestWork, season.ID.StartYear())
}

func (p *fanOutProbe) run(t *testing.T, input *model.SeasonsInput) {
	t.Helper()
	p.env.ExecuteWorkflow(fanOutTestWorkflow, input)
	require.True(t, p.env.IsWorkflowCompleted())
}

func (p *fanOutProbe) group(t *testing.T) shared.ProgressGroup {
	t.Helper()
	report := queryProgressReport(t, p.env)
	require.Len(t, report.Groups, 1)
	return report.Groups[0]
}

// The runner normalizes an omitted input to the empty input (every
// configured season) and passes an explicit one through unchanged; Prepare
// and the result see the input the manifest received.
func TestRunSeasonFanOutInput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		input, wantM *model.SeasonsInput
	}{
		{"omitted", nil, &model.SeasonsInput{}},
		{"explicit", explicitSeasonsInput(), explicitSeasonsInput()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFanOutProbe(t, fanOutTestSeasons(), tc.wantM)
			p.run(t, tc.input)
			require.NoError(t, p.env.GetWorkflowError())
			assert.Equal(t, 1, p.manifests)
			assert.Equal(t, tc.wantM, p.result.Input)
			require.Len(t, p.prepared, 1)
			assert.Same(t, p.result.Input, p.prepared[0].Input)
		})
	}
}

// Prepare sees the snapshotted season concurrency: the configured default,
// an explicit override, and an override capped by the maximum flag.
func TestRunSeasonFanOutConcurrencySnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override *int
		max      int
		want     int
	}{
		{"default", nil, 0, config.DefaultSeasonConcurrency},
		{"override", intPtr(1), 0, 1},
		{"capped", intPtr(fanOutTestMaxConcurrency + 1), fanOutTestMaxConcurrency, fanOutTestMaxConcurrency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.max > 0 {
				setViperInt(t, config.FlagMaxSeasonConcurrency, tc.max)
			}
			p := newFanOutProbe(t, fanOutTestSeasons(), nil)
			p.run(t, &model.SeasonsInput{SeasonConcurrency: tc.override})
			require.NoError(t, p.env.GetWorkflowError())
			require.Len(t, p.prepared, 1)
			assert.Equal(t, tc.want, p.prepared[0].Concurrency)
		})
	}
}

// A failed manifest fails the run before Prepare and any progress work.
func TestRunSeasonFanOutManifestError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	prepared := false
	env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input *model.SeasonsInput) error {
		_, err := runSeasonFanOut(ctx, input, SeasonFanOut{
			Report: &shared.ProgressReport{Groups: []shared.ProgressGroup{{Header: fanOutTestHeader}}},
			Prepare: func(workflow.Context, SeasonFanOutState) (SeasonFanOutPlan, error) {
				prepared = true
				return SeasonFanOutPlan{}, nil
			},
		})
		return err
	}, workflow.RegisterOptions{Name: fanOutTestWorkflow})
	var sa *worknhl.SeasonsActivities
	env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).
		Return(worknhl.FetchSeasonsManifestResult{}, fanOutFailure("manifest down"))

	env.ExecuteWorkflow(fanOutTestWorkflow, nil)
	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "manifest down")
	assert.False(t, prepared)
}

// An empty manifest, or a selection that keeps no season, is a successful
// no-op after the manifest: the query handler answers, the result carries
// the input and tracker, and Prepare, stale report cleanup, bars, progress
// saves and work never run.
func TestRunSeasonFanOutEmptySelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seasons []nhl.SeasonInfo
		sel     func([]nhl.SeasonInfo) []nhl.SeasonInfo
	}{
		{"empty manifest", nil, nil},
		{"selection to empty", fanOutTestSeasons(), func([]nhl.SeasonInfo) []nhl.SeasonInfo { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFanOutProbe(t, tc.seasons, nil)
			p.opts.Select = tc.sel
			p.run(t, nil)
			require.NoError(t, p.env.GetWorkflowError())
			assert.Equal(t, 1, p.manifests)
			assert.Empty(t, p.prepared)
			assert.Empty(t, p.staleKeys)
			assert.Empty(t, p.started)
			assert.Empty(t, p.saved.last)
			assert.Empty(t, p.result.Seasons)
			assert.NotNil(t, p.result.Tracker)
			assert.Equal(t, &model.SeasonsInput{}, p.result.Input)
			assert.Empty(t, p.result.Counts)
			group := p.group(t)
			assert.Empty(t, group.Bars)
			assert.Zero(t, group.StartedAt)
		})
	}
}

// selectOddYears keeps the seasons with an odd start year, in order.
func selectOddYears(seasons []nhl.SeasonInfo) []nhl.SeasonInfo {
	var kept []nhl.SeasonInfo
	for _, s := range seasons {
		if s.ID.StartYear()%2 == 1 {
			kept = append(kept, s)
		}
	}
	return kept
}

// The runner works on the selected seasons only, in manifest order: Prepare
// runs once with them, the starter gets each with its selection index and
// the last flag, stale reports and bars (label, total, source key) follow
// the selection, and the counts aggregate every season's.
func TestRunSeasonFanOutRunsSelectedSeasons(t *testing.T) {
	p := newFanOutProbe(t, fanOutTestSeasons(), nil)
	p.opts.Select = selectOddYears
	p.run(t, nil)
	require.NoError(t, p.env.GetWorkflowError())

	want := selectOddYears(fanOutTestSeasons())
	require.Len(t, p.prepared, 1)
	assert.Equal(t, want, p.prepared[0].Seasons)
	assert.Same(t, p.result.Tracker, p.prepared[0].Tracker)
	assert.Equal(t, want, p.result.Seasons)
	assert.Equal(t, []startedSeason{{2021, 0, false}, {2023, 1, true}}, p.started)
	assert.Equal(t, [][]string{{"fan-out-test-2021", "fan-out-test-2023"}}, p.staleKeys)

	wantCounts := core.OriginCounts{core.OriginFileSystem: 2, core.OriginRedis: 1}
	assert.Equal(t, wantCounts, p.result.Counts)
	report := queryProgressReport(t, p.env)
	assert.Equal(t, []shared.ProgressBar{
		{Label: want[0].Label(), Total: 21, Current: 21, Started: true, ProgressSourceKey: "fan-out-test-2021"},
		{Label: want[1].Label(), Total: 23, Current: 23, Started: true, ProgressSourceKey: "fan-out-test-2023"},
	}, report.Groups[0].Bars)
	assert.Equal(t, 21+23, report.Total)
}

// With GroupLabel and CountLabel the group completes with the standard
// message; a Completion replaces it with its own, given the season count,
// the elapsed time and the aggregated counts; with neither the group is
// left incomplete for the caller.
func TestRunSeasonFanOutCompletion(t *testing.T) {
	type call struct {
		n       int
		elapsed string
		counts  core.OriginCounts
	}
	var got []call
	completion := func(n int, elapsed string, counts core.OriginCounts) string {
		got = append(got, call{n, elapsed, counts})
		return fmt.Sprintf("Custom: %d seasons.", n)
	}
	for _, tc := range []struct {
		name  string
		group func(SeasonGroupConfig) SeasonGroupConfig
		want  func(elapsed string) string
	}{
		{"labels", func(g SeasonGroupConfig) SeasonGroupConfig { return g },
			func(elapsed string) string {
				return "Ran 3 seasons in " + elapsed + ". (reads: 75% filesystem, 25% redis)"
			}},
		{"completion", func(g SeasonGroupConfig) SeasonGroupConfig {
			g.GroupLabel, g.CountLabel, g.Completion = "", "", completion
			return g
		}, func(string) string { return "Custom: 3 seasons." }},
		{"none", func(g SeasonGroupConfig) SeasonGroupConfig {
			g.GroupLabel, g.CountLabel = "", ""
			return g
		}, func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			p := newFanOutProbe(t, fanOutTestSeasons(), nil)
			p.plan = func(SeasonFanOutState) SeasonFanOutPlan {
				plan := p.standardPlan()
				plan.Group = tc.group(plan.Group)
				return plan
			}
			p.run(t, nil)
			require.NoError(t, p.env.GetWorkflowError())
			group := p.group(t)
			elapsed := shared.FormatElapsedMilli(group.CompletedAt - group.StartedAt)
			assert.Equal(t, tc.want(elapsed), group.CompletedMsg)
			if tc.name == "completion" {
				assert.Equal(t, []call{{3, elapsed, core.OriginCounts{core.OriginFileSystem: 3, core.OriginRedis: 1}}}, got)
			}
		})
	}
}

// fanOutFailure is the non-retryable error the failure tests inject.
func fanOutFailure(msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, "test", nil)
}

// A failing Prepare stops the run before the group; a failing counter
// after the stale report cleanup but before any work; a failing season's
// work fails the run without completing the group.
func TestRunSeasonFanOutFailures(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		p := newFanOutProbe(t, fanOutTestSeasons(), nil)
		p.opts.Prepare = func(ctx workflow.Context, state SeasonFanOutState) (SeasonFanOutPlan, error) {
			_, _ = p.prepare(ctx, state)
			return SeasonFanOutPlan{}, errors.New("scan failed")
		}
		p.run(t, nil)
		require.ErrorContains(t, p.env.GetWorkflowError(), "scan failed")
		assert.Len(t, p.prepared, 1)
		assert.Empty(t, p.staleKeys)
		assert.Empty(t, p.started)
	})
	t.Run("counter", func(t *testing.T) {
		p := newFanOutProbe(t, fanOutTestSeasons(), nil)
		p.plan = func(SeasonFanOutState) SeasonFanOutPlan {
			plan := p.standardPlan()
			plan.Group.Counter = func(workflow.Context, nhl.SeasonInfo) (int, error) {
				return 0, errors.New("count failed")
			}
			return plan
		}
		p.run(t, nil)
		require.ErrorContains(t, p.env.GetWorkflowError(), "count failed")
		assert.Len(t, p.staleKeys, 1, "stale reports are cleaned before the counter runs")
		assert.Empty(t, p.started)
		assert.Zero(t, p.group(t).StartedAt)
	})
	t.Run("work", func(t *testing.T) {
		p := newFanOutProbe(t, fanOutTestSeasons(), nil)
		p.workErr[2022] = fanOutFailure("season 2022 failed")
		p.run(t, nil)
		require.ErrorContains(t, p.env.GetWorkflowError(), "season 2022 failed")
		group := p.group(t)
		assert.NotZero(t, group.StartedAt)
		assert.Empty(t, group.CompletedMsg, "a failed season never completes the group")
		assert.Zero(t, group.CompletedAt)
	})
}

// Missing options fail the run before its first command (no manifest), and
// an invalid plan fails it before the group's first command (no stale
// report cleanup).
func TestRunSeasonFanOutValidation(t *testing.T) {
	t.Run("options", func(t *testing.T) {
		for name, mutate := range map[string]func(*SeasonFanOut){
			"no report":  func(o *SeasonFanOut) { o.Report = nil },
			"no prepare": func(o *SeasonFanOut) { o.Prepare = nil },
		} {
			t.Run(name, func(t *testing.T) {
				p := newFanOutProbe(t, fanOutTestSeasons(), nil)
				mutate(&p.opts)
				p.run(t, nil)
				require.ErrorContains(t, p.env.GetWorkflowError(), "season fan-out: no ")
				assert.Zero(t, p.manifests)
				assert.Empty(t, p.saved.last)
			})
		}
	})
	t.Run("plan", func(t *testing.T) {
		for name, tc := range fanOutInvalidPlans() {
			t.Run(name, func(t *testing.T) {
				p := newFanOutProbe(t, fanOutTestSeasons(), nil)
				p.plan = func(SeasonFanOutState) SeasonFanOutPlan {
					plan := p.standardPlan()
					tc.mutate(&plan)
					return plan
				}
				p.run(t, nil)
				require.ErrorContains(t, p.env.GetWorkflowError(), tc.err)
				assert.Len(t, p.prepared, 1)
				assert.Empty(t, p.staleKeys)
				assert.Empty(t, p.started)
			})
		}
	})
}

// fanOutInvalidPlans are plans the runner must reject, with the error each
// gets.
func fanOutInvalidPlans() map[string]struct {
	mutate func(*SeasonFanOutPlan)
	err    string
} {
	noCompletion := func(int, string, core.OriginCounts) string { return "" }
	return map[string]struct {
		mutate func(*SeasonFanOutPlan)
		err    string
	}{
		"no start":   {func(p *SeasonFanOutPlan) { p.Start = nil }, errSeasonFanOutNoStart.Error()},
		"no counter": {func(p *SeasonFanOutPlan) { p.Group.Counter = nil }, errSeasonGroupNoCounter.Error()},
		"completion and group label": {func(p *SeasonFanOutPlan) {
			p.Group.CountLabel, p.Group.Completion = "", noCompletion
		}, errSeasonGroupTwoCompletions.Error()},
		"completion and count label": {func(p *SeasonFanOutPlan) {
			p.Group.GroupLabel, p.Group.Completion = "", noCompletion
		}, errSeasonGroupTwoCompletions.Error()},
		"group past the report": {func(p *SeasonFanOutPlan) { p.Group.GroupIdx = 1 }, "group 1 outside the report's 1 groups"},
		"negative group":        {func(p *SeasonFanOutPlan) { p.Group.GroupIdx = -1 }, "group -1 outside the report's 1 groups"},
	}
}

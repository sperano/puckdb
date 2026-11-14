package workflow

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"
)

// edgeTestBarTotal is every Edge season's bar: 32 teams × 3 activities × 2
// game types.
const edgeTestBarTotal = 192

// edgeTestManifest lists two seasons before Edge tracking and the two
// eligible seasons edgeTestEligible keeps.
func edgeTestManifest() []nhl.SeasonInfo {
	return []nhl.SeasonInfo{
		testSeasonW(2019, "2019-10-02", "2019-10-04"),
		testSeasonW(2020, "2021-01-13", "2021-01-15"),
		testSeasonW(2021, "2021-10-12", "2021-10-14"),
		testSeasonW(2022, "2022-10-07", "2022-10-09"),
	}
}

func edgeTestEligible() []nhl.SeasonInfo { return edgeTestManifest()[2:] }

func edgeTestBar(int) int { return edgeTestBarTotal }

// newFetchEdgeParent stubs each FetchEdgeWorkflow child to record its ID and
// input and to return one endpoint per bar unit; the tests check which
// children started.
func newFetchEdgeParent(t *testing.T, seasons []nhl.SeasonInfo) *fanOutParent {
	p := newFanOutParent(t, FetchEdgeSeasonsWorkflow, seasons)
	p.env.RegisterWorkflow(FetchEdgeWorkflow)
	p.env.OnWorkflow(FetchEdgeWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, in FetchEdgeWorkflowInput) (core.OriginCounts, error) {
			p.recordChild(ctx, in)
			return core.OriginCounts{core.OriginRemoteNHLAPI: edgeTestBarTotal}, nil
		}).Maybe()
	return p
}

// FetchEdgeSeasonsWorkflow fetches only the Edge-eligible seasons, the
// last of them being the one a range-less refresh applies to, and keys each
// bar and stale report to the child's workflow ID.
func TestFetchEdgeSeasonsWorkflowFetchesEligibleSeasons(t *testing.T) {
	for _, tc := range fanOutRefreshCases(2021) {
		t.Run(tc.name, func(t *testing.T) {
			p := newFetchEdgeParent(t, edgeTestManifest())
			group := p.run(t, FetchEdgeSeasonsWorkflow, &model.SeasonsInput{
				StartSeason: tc.start, EndSeason: tc.end, RefreshCurrentEdge: tc.flag,
			})

			var want []fanOutChild
			for i, s := range edgeTestEligible() {
				want = append(want, fanOutChild{WorkflowIDFetchEdge(s.ID.StartYear()),
					FetchEdgeWorkflowInput{Season: s, RefreshCurrent: tc.want[i]}})
			}
			assert.ElementsMatch(t, want, p.children)
			assert.Equal(t, fanOutSourceKeys(edgeTestEligible(), WorkflowIDFetchEdge), p.staleKeys)
			assert.Equal(t, fanOutBars(edgeTestEligible(), edgeTestBar, WorkflowIDFetchEdge), group.Bars)
			elapsed := shared.FormatElapsedMilli(group.CompletedAt - group.StartedAt)
			assert.Equal(t, "Fetched Edge stats for 2 seasons in "+elapsed+". (endpoints: 100% remotenhlapi)",
				group.CompletedMsg)
		})
	}
}

// A manifest without an Edge-eligible season starts nothing: no stale
// report cleanup, no bar, no child and no completion.
func TestFetchEdgeSeasonsWorkflowWithoutEligibleSeason(t *testing.T) {
	p := newFetchEdgeParent(t, edgeTestManifest()[:2])
	group := p.run(t, FetchEdgeSeasonsWorkflow, nil)

	assert.Empty(t, p.children)
	assert.Empty(t, p.staleKeys)
	assert.Empty(t, group.Bars)
	assert.Zero(t, group.StartedAt)
	assert.Empty(t, group.CompletedMsg)
}
